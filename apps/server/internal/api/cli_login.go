// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"wardlock.nz/server/internal/owners"
)

// retryAfter is a duration in whole seconds, at least 1: Retry-After and
// the CLI login poll interval.
func retryAfter(d time.Duration) *int32 {
	secs := max(int32(min(math.MaxInt32, math.Ceil(d.Seconds()))), 1)
	return &secs
}

func loginExpiredError() Error {
	return Error{Code: "login_expired", Message: "This login has expired, was denied or was already used. Run wardlock login again."}
}

// StartCliLogin begins a CLI device login (`wardlock login`).
func (s *Server) StartCliLogin(ctx context.Context, req StartCliLoginRequestObject) (StartCliLoginResponseObject, error) {
	name := ""
	if req.Body != nil && req.Body.ClientName != nil {
		name = *req.Body.ClientName
	}
	l, err := s.Tokens.StartCLILogin(ctx, client(ctx), name)
	if rl, ok := errors.AsType[*owners.RateLimitError](err); ok {
		return StartCliLogin429JSONResponse{RateLimitedJSONResponse{
			Body:    Error{Code: "rate_limited", Message: "Too many logins started. Try again later."},
			Headers: RateLimitedResponseHeaders{RetryAfter: retryAfter(rl.RetryAfter)},
		}}, nil
	}
	if err != nil {
		return nil, err
	}
	return StartCliLogin201JSONResponse{
		DeviceCode:              l.DeviceCode,
		UserCode:                l.UserCode,
		VerificationUrl:         l.VerificationURL,
		VerificationUrlComplete: l.VerificationURLComplete,
		ExpiresAt:               l.ExpiresAt.UnixMilli(),
		Interval:                *retryAfter(l.Interval),
	}, nil
}

// PollCliLogin answers a waiting CLI: pending, its token once, or gone.
func (s *Server) PollCliLogin(ctx context.Context, req PollCliLoginRequestObject) (PollCliLoginResponseObject, error) {
	if req.Body == nil || req.Body.DeviceCode == "" || len(req.Body.DeviceCode) > 128 {
		return PollCliLogin400JSONResponse{BadRequestJSONResponse(invalidField("deviceCode", "deviceCode is required."))}, nil
	}
	t, token, err := s.Tokens.PollCLILogin(ctx, client(ctx), req.Body.DeviceCode)
	if sd, ok := errors.AsType[*owners.SlowDownError](err); ok {
		return PollCliLogin429JSONResponse{RateLimitedJSONResponse{
			Body:    Error{Code: "slow_down", Message: "Polling too often. Wait longer between polls."},
			Headers: RateLimitedResponseHeaders{RetryAfter: retryAfter(sd.RetryAfter)},
		}}, nil
	}
	if rl, ok := errors.AsType[*owners.RateLimitError](err); ok {
		return PollCliLogin429JSONResponse{RateLimitedJSONResponse{
			Body:    Error{Code: "rate_limited", Message: "Too many requests. Try again later."},
			Headers: RateLimitedResponseHeaders{RetryAfter: retryAfter(rl.RetryAfter)},
		}}, nil
	}
	switch {
	case errors.Is(err, owners.ErrLoginPending):
		return PollCliLogin202JSONResponse{Status: CliLoginPendingStatusPending}, nil
	case errors.Is(err, owners.ErrLoginExpired):
		return PollCliLogin410JSONResponse{GoneJSONResponse(loginExpiredError())}, nil
	case err != nil:
		return nil, err
	}
	return PollCliLogin200JSONResponse{ApiToken: apiTokenBody(t), Token: token}, nil
}

// decideError maps an approval or denial failure; ok is false for
// unexpected errors.
func decideError(err error) (status int, body Error, wait *int32, ok bool) {
	if rl, isRL := errors.AsType[*owners.RateLimitError](err); isRL {
		return http.StatusTooManyRequests, Error{Code: "rate_limited", Message: "Too many attempts. Try again later."}, retryAfter(rl.RetryAfter), true
	}
	if errors.Is(err, owners.ErrInvalidCode) {
		return http.StatusBadRequest, Error{Code: "invalid_code", Message: "This code is wrong, expired or already used. Run wardlock login again for a new one."}, nil, true
	}
	if body, isTok := tokenError(err); isTok {
		return http.StatusBadRequest, body, nil, true
	}
	return 0, Error{}, nil, false
}

// ApproveCliLogin lets a waiting CLI receive a token for the signed-in
// owner.
func (s *Server) ApproveCliLogin(ctx context.Context, req ApproveCliLoginRequestObject) (ApproveCliLoginResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return ApproveCliLogin401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil || req.Body.UserCode == "" {
		return ApproveCliLogin400JSONResponse{BadRequestJSONResponse(invalidField("userCode", "userCode is required."))}, nil
	}
	scopes, ok := domainScopes(req.Body.Scopes)
	if !ok {
		return ApproveCliLogin400JSONResponse{BadRequestJSONResponse(invalidField("scopes", "scopes must list deploy, manage or both."))}, nil
	}
	name := ""
	if req.Body.Name != nil {
		if name = *req.Body.Name; name == "" {
			return ApproveCliLogin400JSONResponse{BadRequestJSONResponse(invalidField("name", "name must be 1 to 100 characters."))}, nil
		}
	}
	err := s.Tokens.ApproveCLILogin(ctx, ownerID, req.Body.UserCode, scopes, name)
	if err == nil {
		return ApproveCliLogin204Response{}, nil
	}
	if status, body, wait, ok := decideError(err); ok {
		if status == http.StatusTooManyRequests {
			return ApproveCliLogin429JSONResponse{RateLimitedJSONResponse{Body: body, Headers: RateLimitedResponseHeaders{RetryAfter: wait}}}, nil
		}
		return ApproveCliLogin400JSONResponse{BadRequestJSONResponse(body)}, nil
	}
	return nil, err
}

// DenyCliLogin ends a waiting CLI login.
func (s *Server) DenyCliLogin(ctx context.Context, req DenyCliLoginRequestObject) (DenyCliLoginResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return DenyCliLogin401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil || req.Body.UserCode == "" {
		return DenyCliLogin400JSONResponse{BadRequestJSONResponse(invalidField("userCode", "userCode is required."))}, nil
	}
	err := s.Tokens.DenyCLILogin(ctx, ownerID, req.Body.UserCode)
	if err == nil {
		return DenyCliLogin204Response{}, nil
	}
	if status, body, wait, ok := decideError(err); ok {
		if status == http.StatusTooManyRequests {
			return DenyCliLogin429JSONResponse{RateLimitedJSONResponse{Body: body, Headers: RateLimitedResponseHeaders{RetryAfter: wait}}}, nil
		}
		return DenyCliLogin400JSONResponse{BadRequestJSONResponse(body)}, nil
	}
	return nil, err
}
