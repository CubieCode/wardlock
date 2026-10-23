// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"wardlock.nz/server/internal/owners"
)

// An API token is checked against three tables, kept in step with
// packages/api-spec/openapi.yaml by TestTokenScopesMatchContract:
//
//   - publicOperations take no credentials, so a token makes no difference;
//   - tokenScopes lists what a token needs for each operation that accepts
//     one: any one entry is enough, and "" means any token;
//   - sessionOnly lists the operations that take only a dashboard session:
//     tokens never manage owners, sessions, passkeys or other tokens.
//
// The check is default-deny: a token-authenticated request for an operation
// in no table (one added to the contract but not here) answers 403.
var publicOperations = map[string]bool{
	"GetHealth":           true,
	"CompleteSetup":       true,
	"RedeemSignInLink":    true,
	"SendEmailCode":       true,
	"VerifyEmailCode":     true,
	"StartPasskeySignIn":  true,
	"VerifyPasskeySignIn": true,
	"StartCliLogin":       true,
	"PollCliLogin":        true,
}

var tokenScopes = map[string][]string{
	"GetMe":   {""},
	"SignOut": {""},

	"ListSites":          {"deploy", "manage"},
	"GetSite":            {"deploy", "manage"},
	"CreateSite":         {"deploy"},
	"DeleteSite":         {"manage"},
	"ListDeploys":        {"deploy", "manage"},
	"ListDeployFiles":    {"deploy", "manage"},
	"DownloadDeployBlob": {"deploy", "manage"},
	"CreateDeploy":       {"deploy"},
	"UploadDeployBlob":   {"deploy"},
	"ActivateDeploy":     {"deploy"},
	"RollbackSite":       {"deploy"},

	"ListDomains":  {"deploy", "manage"},
	"GetDomain":    {"deploy", "manage"},
	"AddDomain":    {"manage"},
	"RemoveDomain": {"manage"},
	"VerifyDomain": {"manage"},

	"ListPolicies": {"deploy", "manage"},
	"SetPolicy":    {"manage"},
	"DeletePolicy": {"manage"},

	"ListShareLinks":  {"deploy", "manage"},
	"CreateShareLink": {"manage"},
	"RevokeShareLink": {"manage"},
	"RotateShareLink": {"manage"},

	"ListShareLinkAccessLog": {"deploy", "manage"},
	"ListSiteAccessLog":      {"deploy", "manage"},

	"ListGroups":  {"deploy", "manage"},
	"CreateGroup": {"manage"},
	"UpdateGroup": {"manage"},
	"DeleteGroup": {"manage"},
	"ListTags":    {"deploy", "manage"},
	"CreateTag":   {"manage"},
	"UpdateTag":   {"manage"},
	"DeleteTag":   {"manage"},
	// A deploy may file the site it publishes under existing groups and
	// tags (V5.15); everything else here needs manage.
	"SetSiteOrganisation": {"deploy", "manage"},
	"PinSite":             {"manage"},
	"UnpinSite":           {"manage"},
	"OrganiseSites":       {"manage"},

	"GetAnalytics": {"deploy", "manage"},
}

var sessionOnly = map[string]bool{
	"InviteOwner":     true,
	"ListSessions":    true,
	"RevokeSession":   true,
	"ListApiTokens":   true,
	"CreateApiToken":  true,
	"RevokeApiToken":  true,
	"ApproveCliLogin": true,
	"DenyCliLogin":    true,

	"ListPasskeys":             true,
	"StartPasskeyRegistration": true,
	"CreatePasskey":            true,
	"DeletePasskey":            true,
}

// errTokenScope means the request's API token may not use the operation.
var errTokenScope = errors.New("API token lacks the scope")

// requireTokenScopes refuses an API-token request unless the operation is
// public or the token has a scope the operation accepts. Session and
// unauthenticated requests pass through; handlers answer 401 when they need
// an owner and have none.
func requireTokenScopes(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		ri := info(ctx)
		if ri.tokenID != "" && !tokenAllowed(operationID, ri.scopes) {
			return nil, errTokenScope
		}
		return next(ctx, w, r, request)
	}
}

// tokenAllowed reports whether a token with scopes may call operationID.
// sessionOnly operations and unknown ones are refused.
func tokenAllowed(operationID string, scopes owners.Scopes) bool {
	return publicOperations[operationID] || scopesAllow(scopes, tokenScopes[operationID])
}

func scopesAllow(have owners.Scopes, accepted []string) bool {
	for _, need := range accepted {
		switch need {
		case "":
			return true
		case "deploy":
			if have.Deploy {
				return true
			}
		case "manage":
			if have.Manage {
				return true
			}
		}
	}
	return false
}

// bearerToken returns the token from an "Authorization: Bearer" header.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func scopesBody(s owners.Scopes) Scopes {
	out := Scopes{}
	if s.Deploy {
		out = append(out, ScopeDeploy)
	}
	if s.Manage {
		out = append(out, ScopeManage)
	}
	return out
}

// domainScopes reads a contract scope list; ok is false when it is empty
// or names a scope twice or one the server does not know.
func domainScopes(in Scopes) (owners.Scopes, bool) {
	var s owners.Scopes
	for _, sc := range in {
		switch {
		case sc == ScopeDeploy && !s.Deploy:
			s.Deploy = true
		case sc == ScopeManage && !s.Manage:
			s.Manage = true
		default:
			return owners.Scopes{}, false
		}
	}
	return s, s.Any()
}

func apiTokenBody(t owners.APITokenInfo) ApiToken {
	out := ApiToken{
		Id: t.ID, PublicId: t.PublicID, Name: t.Name, Scopes: scopesBody(t.Scopes), CreatedAt: t.CreatedAt.UnixMilli(),
	}
	if !t.ExpiresAt.IsZero() {
		ms := t.ExpiresAt.UnixMilli()
		out.ExpiresAt = &ms
	}
	if !t.LastUsedAt.IsZero() {
		ms := t.LastUsedAt.UnixMilli()
		out.LastUsedAt = &ms
	}
	return out
}

// tokenError maps owners' token validation errors to a 400 body.
func tokenError(err error) (Error, bool) {
	switch {
	case errors.Is(err, owners.ErrInvalidName):
		return invalidField("name", "name must be 1 to 100 characters."), true
	case errors.Is(err, owners.ErrInvalidScopes):
		return invalidField("scopes", "scopes must list deploy, manage or both."), true
	case errors.Is(err, owners.ErrInvalidExpiry):
		return invalidField("expiresAt", "expiresAt must be in the future."), true
	}
	return Error{}, false
}

// ListApiTokens lists the signed-in owner's usable API tokens.
func (s *Server) ListApiTokens(ctx context.Context, req ListApiTokensRequestObject) (ListApiTokensResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return ListApiTokens401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = int(*req.Params.Limit)
	}
	if limit < 1 || limit > 100 {
		return ListApiTokens400JSONResponse{BadRequestJSONResponse(invalidField("limit", "limit must be between 1 and 100."))}, nil
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	items, next, err := s.Tokens.List(ctx, ownerID, cursor, limit)
	if err != nil {
		if e, ok := classifyOwnerError(err); ok && e.status == http.StatusBadRequest {
			return ListApiTokens400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		}
		return nil, err
	}
	page := ListApiTokens200JSONResponse{Items: make([]ApiToken, len(items))}
	for i, it := range items {
		page.Items[i] = apiTokenBody(it)
	}
	if next != "" {
		page.NextCursor = &next
	}
	return page, nil
}

// CreateApiToken creates an API token and returns it, the only time it is
// shown.
func (s *Server) CreateApiToken(ctx context.Context, req CreateApiTokenRequestObject) (CreateApiTokenResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return CreateApiToken401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil {
		return CreateApiToken400JSONResponse{BadRequestJSONResponse(invalidField("name", "name is required."))}, nil
	}
	scopes, ok := domainScopes(req.Body.Scopes)
	if !ok {
		return CreateApiToken400JSONResponse{BadRequestJSONResponse(invalidField("scopes", "scopes must list deploy, manage or both."))}, nil
	}
	var expires time.Time
	if req.Body.ExpiresAt != nil {
		expires = time.UnixMilli(*req.Body.ExpiresAt).UTC()
	}
	t, token, err := s.Tokens.Create(ctx, ownerID, req.Body.Name, scopes, expires)
	if body, ok := tokenError(err); ok {
		return CreateApiToken400JSONResponse{BadRequestJSONResponse(body)}, nil
	}
	if err != nil {
		return nil, err
	}
	return CreateApiToken201JSONResponse{ApiToken: apiTokenBody(t), Token: token}, nil
}

// RevokeApiToken revokes one of the signed-in owner's API tokens.
func (s *Server) RevokeApiToken(ctx context.Context, req RevokeApiTokenRequestObject) (RevokeApiTokenResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return RevokeApiToken401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	err := s.Tokens.Revoke(ctx, ownerID, req.TokenId)
	if errors.Is(err, owners.ErrNotFound) {
		return RevokeApiToken404JSONResponse{NotFoundJSONResponse(Error{Code: "not_found", Message: "Not found."})}, nil
	}
	if err != nil {
		return nil, err
	}
	return RevokeApiToken204Response{}, nil
}
