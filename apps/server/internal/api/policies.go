// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"

	"wardlock.nz/server/internal/access"
	"wardlock.nz/server/internal/store"
)

// Policy operations (A2.1). Every handler takes the owner from the verified
// request context, never from the request.

// policyFailed maps an access error to its response, or returns it as is
// for 500 internal.
func policyFailed(err error) (*failure, error) {
	var te *access.TargetError
	target, message := "", ""
	if errors.As(err, &te) {
		target, message = te.Target, te.Message
	}
	switch {
	case errors.Is(err, access.ErrNotFound):
		return fail(http.StatusNotFound, "not_found", "Not found.", ""), nil
	case errors.Is(err, access.ErrInvalidPath):
		return fail(http.StatusBadRequest, "invalid_policy_path", message, target), nil
	case errors.Is(err, access.ErrInvalidPolicy):
		return fail(http.StatusBadRequest, "invalid_policy", message, target), nil
	case errors.Is(err, access.ErrDefaultPolicy):
		return fail(http.StatusConflict, "default_policy", "The site default policy cannot be deleted; set it instead.", ""), nil
	case errors.Is(err, access.ErrSiteUnavailable):
		return fail(http.StatusConflict, "site_unavailable", siteUnavailable, ""), nil
	}
	return nil, err
}

func (s *Server) policyBody(ctx context.Context, p store.Policy) Policy {
	out := Policy{
		Id:         p.ID,
		Path:       p.PathPrefix,
		Visibility: PolicyVisibility(p.Visibility),
		Methods:    policyMethods(p),
		Allowlist:  PolicyAllowlist{Emails: []string{}, Domains: []string{}, GithubOrgs: &[]string{}},
		Version:    math.MaxInt32, // a policy changed two billion times stays valid
		CreatedAt:  p.CreatedAt.UnixMilli(),
		UpdatedAt:  p.UpdatedAt.UnixMilli(),
		CreatedBy:  s.creator(ctx, p.CreatedBy, p.CreatedByToken),
	}
	if p.Version >= 1 && p.Version <= math.MaxInt32 {
		out.Version = int32(p.Version)
	}
	out.Allowlist.Emails = append(out.Allowlist.Emails, p.Allowlist.Emails...)
	out.Allowlist.Domains = append(out.Allowlist.Domains, p.Allowlist.Domains...)
	*out.Allowlist.GithubOrgs = append(*out.Allowlist.GithubOrgs, p.Allowlist.GitHubOrgs...)
	return out
}

// policyMethods lists a policy's enabled methods.
func policyMethods(p store.Policy) []PolicyMethod {
	out := []PolicyMethod{}
	if p.LinksEnabled {
		out = append(out, PolicyMethodLinks)
	}
	if p.PasswordEnabled {
		out = append(out, PolicyMethodPassword)
	}
	if p.EmailEnabled {
		out = append(out, PolicyMethodEmail)
	}
	if p.OAuthEnabled {
		out = append(out, PolicyMethodOauth)
	}
	return out
}

// ListPolicies lists a site's policies by path, the default first.
func (s *Server) ListPolicies(ctx context.Context, req ListPoliciesRequestObject) (ListPoliciesResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return ListPoliciesdefaultJSONResponse(*unauthenticated), nil
	}
	limit, f := pageLimit(req.Params.Limit)
	if f != nil {
		return ListPoliciesdefaultJSONResponse(*f), nil
	}
	after, ok := decodeCursor(req.Params.Cursor)
	if !ok || (after != "" && access.CheckPrefix(after) != nil) {
		return ListPoliciesdefaultJSONResponse(*badCursor), nil
	}
	list, err := s.Access.List(ctx, actor, req.Site, after, limit+1)
	if err != nil {
		f, err := policyFailed(err)
		if f == nil {
			return nil, err
		}
		return ListPoliciesdefaultJSONResponse(*f), nil
	}
	page := ListPolicies200JSONResponse{Items: []Policy{}}
	if len(list) > limit {
		list = list[:limit]
		page.NextCursor = encodeCursor(list[limit-1].PathPrefix)
	}
	for _, p := range list {
		page.Items = append(page.Items, s.policyBody(ctx, p))
	}
	return page, nil
}

// SetPolicy creates or replaces the policy for a path.
func (s *Server) SetPolicy(ctx context.Context, req SetPolicyRequestObject) (SetPolicyResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return SetPolicydefaultJSONResponse(*unauthenticated), nil
	}
	if req.Body == nil {
		return SetPolicydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "path and visibility are required.", "")), nil
	}
	rule := access.Rule{PathPrefix: req.Body.Path}
	switch req.Body.Visibility {
	case PolicyVisibilityPublic:
		rule.Visibility = store.Public
	case PolicyVisibilityProtected:
		rule.Visibility = store.Protected
		// Share links are on by default (SPEC "Policies").
		rule.Links = req.Body.Methods == nil
	default:
		return SetPolicydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "visibility must be public or protected.", "visibility")), nil
	}
	if req.Body.Methods != nil {
		seen := map[PolicyMethod]bool{}
		for _, m := range *req.Body.Methods {
			if !m.Valid() || seen[m] {
				return SetPolicydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_request", "methods must be distinct, from: links, password, email, oauth.", "methods")), nil
			}
			seen[m] = true
		}
		rule.Links = seen[PolicyMethodLinks]
		rule.Password = seen[PolicyMethodPassword]
		rule.Email = seen[PolicyMethodEmail]
		rule.OAuth = seen[PolicyMethodOauth]
	}
	if a := req.Body.Allowlist; a != nil {
		rule.Allowlist = &store.Allowlist{Emails: a.Emails, Domains: a.Domains}
		// An omitted githubOrgs keeps the policy's organisations.
		if a.GithubOrgs != nil {
			rule.Allowlist.GitHubOrgs = *a.GithubOrgs
		} else {
			rule.KeepGitHubOrgs = true
		}
	}
	// Write-only: hashed by Set, never echoed, logged or returned.
	if req.Body.Password != nil {
		rule.NewPassword = *req.Body.Password
		if rule.NewPassword == "" {
			return SetPolicydefaultJSONResponse(*fail(http.StatusBadRequest, "invalid_policy", "The password cannot be empty; omit it to keep the current one.", "password")), nil
		}
	}
	p, err := s.Access.Set(ctx, actor, req.Site, rule)
	if err != nil {
		f, err := policyFailed(err)
		if f == nil {
			return nil, err
		}
		return SetPolicydefaultJSONResponse(*f), nil
	}
	return SetPolicy200JSONResponse(s.policyBody(ctx, p)), nil
}

// DeletePolicy removes a path policy.
func (s *Server) DeletePolicy(ctx context.Context, req DeletePolicyRequestObject) (DeletePolicyResponseObject, error) {
	actor, ok := ActorOf(ctx)
	if !ok {
		return DeletePolicydefaultJSONResponse(*unauthenticated), nil
	}
	if err := s.Access.Delete(ctx, actor, req.Site, req.PolicyId); err != nil {
		f, err := policyFailed(err)
		if f == nil {
			return nil, err
		}
		return DeletePolicydefaultJSONResponse(*f), nil
	}
	return DeletePolicy204Response{}, nil
}

// signInProviderOrder is the order listSignInProviders answers in.
var signInProviderOrder = []SignInProviderId{SignInProviderIdGithub, SignInProviderIdGoogle, SignInProviderIdOidc}

// ListSignInProviders lists the OAuth and OIDC providers the operator
// configured, by ID and button name only: never a client ID, secret or
// issuer.
func (s *Server) ListSignInProviders(ctx context.Context, _ ListSignInProvidersRequestObject) (ListSignInProvidersResponseObject, error) {
	if _, ok := ActorOf(ctx); !ok {
		return ListSignInProvidersdefaultJSONResponse(*unauthenticated), nil
	}
	out := ListSignInProviders200JSONResponse{Items: []SignInProvider{}}
	for _, p := range s.Access.SignInProviders() {
		if id := SignInProviderId(p.ID); slices.Contains(signInProviderOrder, id) {
			out.Items = append(out.Items, SignInProvider{Id: id, Name: p.Name})
		}
	}
	slices.SortStableFunc(out.Items, func(a, b SignInProvider) int {
		return slices.Index(signInProviderOrder, a.Id) - slices.Index(signInProviderOrder, b.Id)
	})
	return out, nil
}
