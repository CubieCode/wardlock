// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/server/internal/platform"
)

// OpenAIModel is the moderation model Wardlock asks for.
const OpenAIModel = "omni-moderation-latest"

// DefaultOpenAIURL is OpenAI's moderation endpoint.
const DefaultOpenAIURL = "https://api.openai.com/v1/moderations"

// openAITimeout bounds one request, a 20 MB image upload included.
const openAITimeout = 2 * time.Minute

// maxOpenAIResponse bounds the response body read.
const maxOpenAIResponse = 1 << 20

// maxRetryAfter bounds the wait a Retry-After header can ask for.
const maxRetryAfter = 24 * time.Hour

// OpenAI is a Moderator over OpenAI's moderation endpoint
// (https://platform.openai.com/docs/api-reference/moderations). It sends
// text as an array of strings and an image as a base64 data URL, and never
// puts the key, the content or the provider's error message (which can
// quote either) in an error or log line.
type OpenAI struct {
	key    platform.Secret
	url    string
	client *http.Client
}

// OpenAIConfig configures OpenAI.
type OpenAIConfig struct {
	Key platform.Secret
	// URL overrides DefaultOpenAIURL (tests).
	URL string
	// Client defaults to a client with no overall timeout; each request is
	// bounded by its context and openAITimeout.
	Client *http.Client
}

// NewOpenAI returns the OpenAI Moderator.
func NewOpenAI(c OpenAIConfig) *OpenAI {
	o := &OpenAI{key: c.Key, url: c.URL, client: c.Client}
	if o.url == "" {
		o.url = DefaultOpenAIURL
	}
	if o.client == nil {
		o.client = &http.Client{}
	}
	return o
}

type openAIRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type openAIImagePart struct {
	Type     string `json:"type"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

type openAIError struct {
	Error struct {
		Type  string `json:"type"`
		Param string `json:"param"`
		Code  string `json:"code"`
	} `json:"error"`
}

type openAIResponse struct {
	Results []struct {
		Flagged    bool            `json:"flagged"`
		Categories map[string]bool `json:"categories"`
	} `json:"results"`
}

// Moderate sends in and returns the categories flagged in any result.
func (o *OpenAI) Moderate(ctx context.Context, in Input) (Verdict, error) {
	req := openAIRequest{Model: OpenAIModel}
	switch {
	case len(in.Image) > 0:
		part := openAIImagePart{Type: "image_url"}
		part.ImageURL.URL = "data:" + in.ImageType + ";base64," + base64.StdEncoding.EncodeToString(in.Image)
		req.Input = []openAIImagePart{part}
	case len(in.Text) > 0:
		req.Input = in.Text
	default:
		return Verdict{}, nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Verdict{}, fmt.Errorf("encode moderation request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, openAITimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, fmt.Errorf("moderation request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+string(o.key))
	res, err := o.client.Do(r)
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
			return Verdict{}, fmt.Errorf("moderation request: %w", ctx.Err())
		}
		// The transport's error can name the URL but never the key or body;
		// it is still left out, so nothing of the request reaches a log.
		return Verdict{}, &RetryError{}
	}
	defer func() { _ = res.Body.Close() }()
	after := retryAfter(res.Header.Get("Retry-After"), time.Now())
	switch s := res.StatusCode; {
	case s == http.StatusOK:
	case s == http.StatusRequestEntityTooLarge:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxOpenAIResponse))
		return Verdict{}, fmt.Errorf("%w: provider answered %d", ErrRejected, s)
	case s == http.StatusBadRequest:
		// Only an error about this input (an image it cannot read, text
		// too long) rejects the input; a request the provider calls
		// malformed otherwise (a model it does not know, a field it does
		// not take) would fail every input alike.
		var e openAIError
		_ = json.NewDecoder(io.LimitReader(res.Body, maxOpenAIResponse)).Decode(&e)
		code := safeCode(e.Error.Code)
		if inputError(e.Error.Param, e.Error.Code) {
			return Verdict{}, fmt.Errorf("%w: provider answered %d (%s)", ErrRejected, s, code)
		}
		return Verdict{}, &RetryError{Status: s, Global: true, Code: code, After: after}
	case s >= 500, s == http.StatusRequestTimeout, s == http.StatusConflict:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxOpenAIResponse))
		return Verdict{}, &RetryError{Status: s, After: after}
	default:
		// A refused key (401, 403), a rate limit (429) and anything else
		// the provider says about the request rather than the input.
		var e openAIError
		_ = json.NewDecoder(io.LimitReader(res.Body, maxOpenAIResponse)).Decode(&e)
		return Verdict{}, &RetryError{Status: s, Global: true, Code: safeCode(e.Error.Code), After: after}
	}
	var out openAIResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, maxOpenAIResponse)).Decode(&out); err != nil {
		return Verdict{}, &RetryError{Status: res.StatusCode}
	}
	if len(out.Results) == 0 {
		return Verdict{}, &RetryError{Status: res.StatusCode}
	}
	var v Verdict
	for _, result := range out.Results {
		for c, on := range result.Categories {
			if on && !slices.Contains(v.Flagged, c) {
				v.Flagged = append(v.Flagged, c)
			}
		}
	}
	slices.Sort(v.Flagged)
	return v, nil
}

// inputError reports whether a 400's error is about the input itself: the
// parameter it names is the input (input, input[0], input[0].image_url)
// or its code is about an image (invalid_image, image_too_large,
// invalid_image_format, ...).
func inputError(param, code string) bool {
	return param == "input" || strings.HasPrefix(param, "input[") || strings.HasPrefix(param, "input.") ||
		strings.Contains(strings.ToLower(code), "image")
}

// safeCode keeps a provider error code fit for a log line: at most 64 of
// [A-Za-z0-9_.-], "" otherwise.
func safeCode(c string) string {
	if len(c) > 64 {
		return ""
	}
	for _, r := range c {
		if !codeChar(r) {
			return ""
		}
	}
	return c
}

func codeChar(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_' || r == '-' || r == '.'
}

// retryAfter reads a Retry-After header: seconds or an HTTP date, at most
// maxRetryAfter. 0 when absent or unreadable.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(h, 64); err == nil {
		switch {
		case math.IsNaN(s) || s <= 0:
			return 0
		case s >= maxRetryAfter.Seconds():
			return maxRetryAfter
		}
		return time.Duration(s * float64(time.Second))
	}
	if t, err := http.ParseTime(h); err == nil && t.After(now) {
		return min(t.Sub(now), maxRetryAfter)
	}
	return 0
}
