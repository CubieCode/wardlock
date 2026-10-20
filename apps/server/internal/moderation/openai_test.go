// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package moderation

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const testKey = "sk-test-0123456789abcdef"

// TestOpenAI checks the client against a fake endpoint: the request shape
// for text and images, the key in the Authorization header only, how each
// status is classified, Retry-After, and that neither the key nor the
// content reaches an error.
func TestOpenAI(t *testing.T) {
	var got struct {
		auth, ctype string
		body        map[string]any
	}
	status, retry, answer := http.StatusOK, "", `{"id":"modr-1","model":"omni-moderation-latest","results":[
		{"flagged":true,"categories":{"sexual":true,"violence":false,"hate":false},"category_scores":{"sexual":0.9}},
		{"flagged":true,"categories":{"sexual":false,"violence":true},"category_scores":{"violence":0.8}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.ctype = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		got.body = nil
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got.body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/moderations" {
			http.NotFound(w, r)
			return
		}
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	o := NewOpenAI(OpenAIConfig{Key: testKey, URL: srv.URL + "/v1/moderations"})
	ctx := t.Context()

	v, err := o.Moderate(ctx, Input{Text: []string{"first piece", "second piece"}})
	if err != nil || !slices.Equal(v.Flagged, []string{"sexual", "violence"}) {
		t.Fatalf("text: %+v, %v", v, err)
	}
	if got.auth != "Bearer "+testKey || got.ctype != "application/json" || got.body["model"] != OpenAIModel {
		t.Fatalf("request %+v", got)
	}
	if in, ok := got.body["input"].([]any); !ok || len(in) != 2 || in[0] != "first piece" {
		t.Fatalf("text input %#v", got.body["input"])
	}

	answer = `{"results":[{"flagged":false,"categories":{"sexual":false}}]}`
	img := []byte("\x89PNG\r\n\x1a\nimage-bytes")
	v, err = o.Moderate(ctx, Input{Image: img, ImageType: "image/png"})
	if err != nil || len(v.Flagged) != 0 {
		t.Fatalf("image: %+v, %v", v, err)
	}
	parts, _ := got.body["input"].([]any)
	part, _ := parts[0].(map[string]any)
	url, _ := part["image_url"].(map[string]any)
	if len(parts) != 1 || part["type"] != "image_url" || url["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(img) {
		t.Fatalf("image input %#v", got.body["input"])
	}

	for _, c := range []struct {
		status    int
		retry     string
		answer    string
		rejected  bool
		wantAfter time.Duration
	}{
		{status: http.StatusBadRequest, answer: `{"error":{"message":"bad image"}}`, rejected: true},
		{status: http.StatusRequestEntityTooLarge, rejected: true},
		{status: http.StatusTooManyRequests, retry: "7", wantAfter: 7 * time.Second},
		{status: http.StatusTooManyRequests, retry: "soon"},
		{status: http.StatusServiceUnavailable, retry: "1.5", wantAfter: 1500 * time.Millisecond},
		{status: http.StatusInternalServerError},
		{status: http.StatusUnauthorized, answer: `{"error":{"message":"Incorrect API key provided: sk-test-0123"}}`},
		{status: http.StatusOK, answer: `not json`},
		{status: http.StatusOK, answer: `{"results":[]}`},
	} {
		status, retry, answer = c.status, c.retry, c.answer
		_, err := o.Moderate(ctx, Input{Text: []string{"private words"}})
		var re *RetryError
		switch {
		case c.rejected && !errors.Is(err, ErrRejected):
			t.Errorf("%d: %v, want ErrRejected", c.status, err)
		case !c.rejected && (!errors.As(err, &re) || !errors.Is(err, ErrUnavailable) || re.After != c.wantAfter):
			t.Errorf("%d %q: %v (%+v), want a RetryError after %s", c.status, c.retry, err, re, c.wantAfter)
		}
		if err != nil && (strings.Contains(err.Error(), "sk-test") || strings.Contains(err.Error(), "private words")) {
			t.Errorf("%d: error carries the key or content: %v", c.status, err)
		}
	}

	// Unreachable: retried, and the error names neither the URL's
	// credentials nor the content.
	srv.Close()
	_, err = o.Moderate(ctx, Input{Text: []string{"private words"}})
	var re *RetryError
	if !errors.As(err, &re) || re.Status != 0 || strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("unreachable: %v", err)
	}
	// Nothing to send sends nothing.
	if v, err := o.Moderate(ctx, Input{}); err != nil || len(v.Flagged) != 0 {
		t.Fatalf("empty: %+v, %v", v, err)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for h, want := range map[string]time.Duration{
		"": 0, "0": 0, "-3": 0, "20": 20 * time.Second, "0.25": 250 * time.Millisecond, "later": 0,
		now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Hour).Format(http.TimeFormat):       0,
	} {
		if got := retryAfter(h, now); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", h, got, want)
		}
	}
}

func TestImageType(t *testing.T) {
	still := "GIF89a\x01\x00\x01\x00\x80\x00\x00" + "\x00\x00\x00\xff\xff\xff" + // global colour table of 2
		"\x21\xff\x0bNETSCAPE2.0\x03\x01\x00\x00\x00" +
		"\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00" + "\x3b"
	two := still[:len(still)-1] + "\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00" + "\x3b"
	for b, want := range map[string]string{
		"\x89PNG\r\n\x1a\nrest":      "image/png",
		"\xff\xd8\xff\xe0rest":       "image/jpeg",
		"RIFF\x00\x00\x00\x00WEBPVP": "image/webp",
		still:                        "image/gif",
		two:                          "",
		"GIF89a\x01":                 "",
		still[:len(still)-4]:         "",
		"<svg></svg>":                "",
		"BM bitmap":                  "",
	} {
		got, ok := imageType([]byte(b))
		if got := map[bool]string{true: got, false: ""}[ok]; got != want {
			t.Errorf("imageType(%.12q) = %q, want %q", b, got, want)
		}
	}
}

func TestChunkText(t *testing.T) {
	if got := chunkText(" \n\t "); len(got) != 0 {
		t.Fatalf("blank: %q", got)
	}
	if got := chunkText("  short text \n"); !slices.Equal(got, []string{"short text"}) {
		t.Fatalf("short: %q", got)
	}
	// No whitespace to break at: cut at the limit, in characters, not
	// bytes.
	solid := strings.Repeat("é", TextChunk+10)
	got := chunkText(solid)
	if len(got) != 2 || len([]rune(got[0])) != TextChunk || len([]rune(got[1])) != 10 {
		t.Fatalf("solid: %d pieces", len(got))
	}
	// Words break at whitespace and nothing beyond the limit is kept.
	words := strings.Repeat("abcdefghi ", MaxTextChunks*TextChunk/10+500)
	got = chunkText(words)
	if len(got) != MaxTextChunks {
		t.Fatalf("words: %d pieces", len(got))
	}
	for _, c := range got {
		if !strings.HasPrefix(c, "abcdefghi") || !strings.HasSuffix(c, "abcdefghi") || len(c) > TextChunk {
			t.Fatalf("piece %.20q…%q", c, c[len(c)-12:])
		}
	}
	if got := chunkText("bad \xff utf8"); !slices.Equal(got, []string{"bad � utf8"}) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}
