// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"wardlock.nz/server/internal/owners"
)

func invalidPasskeyError() Error {
	return Error{Code: "invalid_passkey", Message: "The passkey was not accepted. Try again."}
}

func passkeyLimitError() Error {
	return Error{Code: "passkey_limit", Message: fmt.Sprintf("You have %d passkeys, the most allowed. Remove one first.", owners.MaxPasskeys)}
}

// sessionOwner is the owner of the dashboard session making the request.
// Passkeys are managed only from a signed-in dashboard, never with an API
// token.
func sessionOwner(ctx context.Context) (string, bool) {
	ownerID, ok := OwnerID(ctx)
	return ownerID, ok && info(ctx).sessionID != ""
}

// challengeBody turns a ceremony's options into the response body.
func challengeBody(c owners.PasskeyChallenge) (PasskeyChallenge, error) {
	body := PasskeyChallenge{CeremonyId: c.CeremonyID}
	if err := json.Unmarshal(c.Options, &body.Options); err != nil {
		return PasskeyChallenge{}, fmt.Errorf("decode passkey options: %w", err)
	}
	return body, nil
}

// StartPasskeySignIn returns a challenge for a discoverable passkey sign-in.
func (s *Server) StartPasskeySignIn(ctx context.Context, _ StartPasskeySignInRequestObject) (StartPasskeySignInResponseObject, error) {
	c, err := s.Passkeys.BeginSignIn(ctx, client(ctx))
	if err != nil {
		if e, ok := classifyOwnerError(err); ok && e.status == http.StatusTooManyRequests {
			return StartPasskeySignIn429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
		return nil, err
	}
	body, err := challengeBody(c)
	if err != nil {
		return nil, err
	}
	return StartPasskeySignIn200JSONResponse(body), nil
}

// VerifyPasskeySignIn signs in with a passkey assertion.
func (s *Server) VerifyPasskeySignIn(ctx context.Context, req VerifyPasskeySignInRequestObject) (VerifyPasskeySignInResponseObject, error) {
	if req.Body == nil || req.Body.CeremonyId == "" || len(req.Body.CeremonyId) > 128 {
		return VerifyPasskeySignIn400JSONResponse{BadRequestJSONResponse(invalidField("ceremonyId", "ceremonyId is required."))}, nil
	}
	credential, err := json.Marshal(req.Body.Credential)
	if err != nil {
		return nil, fmt.Errorf("encode passkey credential: %w", err)
	}
	o, sess, err := s.Passkeys.FinishSignIn(ctx, client(ctx), req.Body.CeremonyId, credential)
	switch {
	case err == nil:
		cookie := sessionCookie(sess.Token, sess.TTL)
		return VerifyPasskeySignIn200JSONResponse{Body: meBody(o), Headers: VerifyPasskeySignIn200ResponseHeaders{SetCookie: &cookie}}, nil
	case errors.Is(err, owners.ErrInvalidPasskey):
		return VerifyPasskeySignIn400JSONResponse{BadRequestJSONResponse(invalidPasskeyError())}, nil
	}
	if e, ok := classifyOwnerError(err); ok && e.status == http.StatusTooManyRequests {
		return VerifyPasskeySignIn429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
	}
	return nil, err
}

// ListPasskeys lists the signed-in owner's passkeys.
func (s *Server) ListPasskeys(ctx context.Context, _ ListPasskeysRequestObject) (ListPasskeysResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return ListPasskeys401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	items, err := s.Passkeys.List(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	page := ListPasskeys200JSONResponse{Items: make([]Passkey, len(items))}
	for i, it := range items {
		page.Items[i] = passkeyBody(it)
	}
	return page, nil
}

func passkeyBody(p owners.PasskeyInfo) Passkey {
	body := Passkey{Id: p.ID, Name: p.Name, CreatedAt: p.CreatedAt.UnixMilli()}
	if !p.LastUsedAt.IsZero() {
		used := p.LastUsedAt.UnixMilli()
		body.LastUsedAt = &used
	}
	return body
}

// StartPasskeyRegistration returns a challenge to add a passkey.
func (s *Server) StartPasskeyRegistration(ctx context.Context, _ StartPasskeyRegistrationRequestObject) (StartPasskeyRegistrationResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return StartPasskeyRegistration401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	c, err := s.Passkeys.BeginRegistration(ctx, ownerID)
	switch {
	case errors.Is(err, owners.ErrPasskeyLimit):
		return StartPasskeyRegistration409JSONResponse{PasskeyLimitJSONResponse(passkeyLimitError())}, nil
	case errors.Is(err, owners.ErrUnauthenticated):
		return StartPasskeyRegistration401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	case err != nil:
		return nil, err
	}
	body, err := challengeBody(c)
	if err != nil {
		return nil, err
	}
	return StartPasskeyRegistration200JSONResponse(body), nil
}

// CreatePasskey finishes adding a passkey.
func (s *Server) CreatePasskey(ctx context.Context, req CreatePasskeyRequestObject) (CreatePasskeyResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return CreatePasskey401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil || req.Body.CeremonyId == "" || len(req.Body.CeremonyId) > 128 {
		return CreatePasskey400JSONResponse{BadRequestJSONResponse(invalidField("ceremonyId", "ceremonyId is required."))}, nil
	}
	name := ""
	if req.Body.Name != nil {
		name = *req.Body.Name
	}
	credential, err := json.Marshal(req.Body.Credential)
	if err != nil {
		return nil, fmt.Errorf("encode passkey credential: %w", err)
	}
	p, err := s.Passkeys.FinishRegistration(ctx, ownerID, req.Body.CeremonyId, name, credential)
	switch {
	case err == nil:
		return CreatePasskey201JSONResponse(passkeyBody(p)), nil
	case errors.Is(err, owners.ErrInvalidPasskeyName):
		return CreatePasskey400JSONResponse{BadRequestJSONResponse(invalidField("name", fmt.Sprintf("name must be at most %d characters with no control characters.", owners.MaxPasskeyName)))}, nil
	case errors.Is(err, owners.ErrInvalidPasskey):
		return CreatePasskey400JSONResponse{BadRequestJSONResponse(invalidPasskeyError())}, nil
	case errors.Is(err, owners.ErrPasskeyLimit):
		return CreatePasskey409JSONResponse{PasskeyLimitJSONResponse(passkeyLimitError())}, nil
	case errors.Is(err, owners.ErrUnauthenticated):
		return CreatePasskey401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	return nil, err
}

// DeletePasskey removes one of the signed-in owner's passkeys.
func (s *Server) DeletePasskey(ctx context.Context, req DeletePasskeyRequestObject) (DeletePasskeyResponseObject, error) {
	ownerID, ok := sessionOwner(ctx)
	if !ok {
		return DeletePasskey401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	err := s.Passkeys.Remove(ctx, ownerID, req.PasskeyId)
	if errors.Is(err, owners.ErrNotFound) {
		return DeletePasskey404JSONResponse{NotFoundJSONResponse(Error{Code: "not_found", Message: "Not found."})}, nil
	}
	if err != nil {
		return nil, err
	}
	return DeletePasskey204Response{}, nil
}
