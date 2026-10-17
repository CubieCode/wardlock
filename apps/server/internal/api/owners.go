// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wardlock.nz/server/internal/owners"
	"wardlock.nz/server/internal/store"
)

// SessionCookie is the dashboard session cookie. The __Host- prefix makes
// browsers insist on Secure, Path=/ and no Domain, so no other host can set
// or overwrite it.
const SessionCookie = "__Host-wardlock_session"

type requestInfoKey struct{}

// requestInfo is what the authentication middleware learnt about a request
// beyond the owner ID.
type requestInfo struct {
	requestID string // also sent as X-Request-Id
	clientIP  string
	userAgent string
	sessionID string // the dashboard session that authenticated it, if any
	// tokenID and scopes describe the API token that authenticated it, if
	// any.
	tokenID string
	scopes  owners.Scopes
}

func info(ctx context.Context) requestInfo {
	ri, _ := ctx.Value(requestInfoKey{}).(requestInfo)
	return ri
}

func client(ctx context.Context) owners.Client {
	ri := info(ctx)
	return owners.Client{IP: ri.clientIP, UserAgent: ri.userAgent}
}

// authenticate assigns a request ID, caps the request body, derives the
// owner context from the session cookie and
// enforces the contract's CSRF rule on every unsafe method, with or without
// the cookie (so sign-in endpoints cannot be driven cross-site either). An
// invalid cookie authenticates nothing and is cleared; handlers answer 401
// when no owner is set.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := requestInfo{requestID: rand.Text(), clientIP: remoteIP(r), userAgent: r.UserAgent()}
		w.Header().Set("X-Request-Id", ri.requestID)
		ctx := r.Context()
		if r.Method != http.MethodPut || !blobUpload.MatchString(r.URL.Path) {
			if r.ContentLength > MaxBodyBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "limit_exceeded", fmt.Sprintf("The request body is larger than %d bytes.", MaxBodyBytes))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		}
		c, err := r.Cookie(SessionCookie)
		hasCookie := err == nil
		if !safeMethod(r.Method) && !sameOrigin(r, hasCookie) {
			writeError(w, http.StatusForbidden, "forbidden", "Cross-origin requests are refused.")
			return
		}
		if bearer, ok := bearerToken(r); ok {
			// A bearer request is authenticated by its token alone, even
			// when the browser also sent the session cookie.
			if s.Tokens != nil {
				auth, err := s.Tokens.Authenticate(ctx, owners.Client{IP: ri.clientIP, UserAgent: ri.userAgent}, bearer)
				switch {
				case err == nil:
					ctx = WithOwner(ctx, auth.OwnerID)
					ri.tokenID, ri.scopes = auth.TokenID, auth.Scopes
				case errors.Is(err, owners.ErrUnauthenticated):
				case errors.Is(err, owners.ErrRateLimited):
					rl, _ := errors.AsType[*owners.RateLimitError](err)
					w.Header().Set("Retry-After", strconv.Itoa(int(*retryAfter(rl.RetryAfter))))
					writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many failed attempts. Try again later.")
					return
				default:
					logInternal(s.logger(), r.WithContext(context.WithValue(ctx, requestInfoKey{}, ri)), "token middleware", err)
					writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
					return
				}
			}
		} else if hasCookie && s.Owners != nil {
			sess, err := s.Owners.Authenticate(ctx, c.Value)
			switch {
			case err == nil:
				ctx = WithOwner(ctx, sess.OwnerID)
				ri.sessionID = sess.ID
				// Sign-out answers with the clearing cookie only.
				if sess.Refreshed && r.URL.Path != signOutPath {
					w.Header().Add("Set-Cookie", sessionCookie(c.Value, sess.TTL))
				}
			case errors.Is(err, owners.ErrUnauthenticated):
				w.Header().Add("Set-Cookie", clearSessionCookie())
			default:
				logInternal(s.logger(), r.WithContext(context.WithValue(ctx, requestInfoKey{}, ri)), "session middleware", err)
				writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, requestInfoKey{}, ri)))
	})
}

const signOutPath = "/api/v0/auth/sign-out"

// MaxBodyBytes caps every request body except blob uploads, whose size
// the deploy limits govern. It covers createDeploy's largest legitimate
// manifest (about 20,000 entries).
const MaxBodyBytes = 32 << 20

// blobUpload matches PUT /api/v0/sites/{site}/deploys/{deployId}/blobs/{sha256}.
var blobUpload = regexp.MustCompile(`^/api/v0/sites/[^/]+/deploys/[^/]+/blobs/[^/]+$`)

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin applies the contract's rule to an unsafe request: Origin, when
// present, must be the request's own origin, and Sec-Fetch-Site, when
// present, must be same-origin. With neither header the request passes only
// if it carries no session cookie (non-browser clients such as the CLI).
func sameOrigin(r *http.Request, hasCookie bool) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return site == "same-origin" || !hasCookie
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// remoteIP is the client address. Behind a trusted proxy, the tls
// package's middleware has already set RemoteAddr from X-Forwarded-For.
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func sessionCookie(token string, ttl time.Duration) string {
	return (&http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: max(1, int(ttl/time.Second)),
	}).String()
}

func clearSessionCookie() string {
	return (&http.Cookie{
		Name: SessionCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	}).String()
}

// ownerError is a domain error translated for the owner operations.
type ownerError struct {
	status     int
	body       Error
	retryAfter *int32
}

// classifyOwnerError maps owners sentinels to responses; ok is false for
// unexpected errors, which become 500.
func classifyOwnerError(err error) (ownerError, bool) {
	var rl *owners.RateLimitError
	switch {
	case errors.As(err, &rl):
		secs := int32(min(math.MaxInt32, math.Ceil(rl.RetryAfter.Seconds())))
		secs = max(secs, 1)
		return ownerError{http.StatusTooManyRequests, Error{Code: "rate_limited", Message: "Too many attempts. Try again later."}, &secs}, true
	case errors.Is(err, owners.ErrInvalidCode):
		return ownerError{status: http.StatusBadRequest, body: Error{Code: "invalid_code", Message: "The code is wrong, used or expired."}}, true
	case errors.Is(err, owners.ErrInvalidEmail):
		return ownerError{status: http.StatusBadRequest, body: invalidField("email", "email must be a plain email address.")}, true
	case errors.Is(err, owners.ErrInvalidCursor):
		return ownerError{status: http.StatusBadRequest, body: invalidField("cursor", "cursor is not a value this server issued.")}, true
	case errors.Is(err, owners.ErrUnauthenticated):
		return ownerError{status: http.StatusUnauthorized, body: unauthenticatedError()}, true
	case errors.Is(err, owners.ErrNotFound):
		return ownerError{status: http.StatusNotFound, body: Error{Code: "not_found", Message: "Not found."}}, true
	}
	return ownerError{}, false
}

func invalidField(target, message string) Error {
	return Error{Code: "invalid_request", Message: message, Target: &target}
}

func unauthenticatedError() Error {
	return Error{Code: "unauthenticated", Message: "Sign in or provide an API token."}
}

func meBody(o store.Owner) Me {
	return Me{
		Owner:      Owner{Id: o.ID, Email: Email(o.Email), CreatedAt: o.CreatedAt.UnixMilli()},
		Credential: MeCredentialSession,
		Scopes:     Scopes{ScopeDeploy, ScopeManage},
	}
}

// CompleteSetup creates the first owner from the setup code.
func (s *Server) CompleteSetup(ctx context.Context, req CompleteSetupRequestObject) (CompleteSetupResponseObject, error) {
	if req.Body == nil || req.Body.Code == "" || len(req.Body.Code) > 128 {
		return CompleteSetup400JSONResponse{BadRequestJSONResponse(invalidField("code", "code is required."))}, nil
	}
	if req.Body.Email == "" {
		return CompleteSetup400JSONResponse{BadRequestJSONResponse(invalidField("email", "email is required."))}, nil
	}
	o, sess, err := s.Owners.CompleteSetup(ctx, client(ctx), req.Body.Code, string(req.Body.Email))
	if err == nil {
		cookie := sessionCookie(sess.Token, sess.TTL)
		return CompleteSetup201JSONResponse{Body: meBody(o), Headers: CompleteSetup201ResponseHeaders{SetCookie: &cookie}}, nil
	}
	if e, ok := classifyOwnerError(err); ok {
		switch e.status {
		case http.StatusBadRequest:
			return CompleteSetup400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		case http.StatusTooManyRequests:
			return CompleteSetup429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
	}
	return nil, err
}

// RedeemSignInLink signs in with a one-time sign-in URL code.
func (s *Server) RedeemSignInLink(ctx context.Context, req RedeemSignInLinkRequestObject) (RedeemSignInLinkResponseObject, error) {
	if req.Body == nil || req.Body.Code == "" || len(req.Body.Code) > 128 {
		return RedeemSignInLink400JSONResponse{BadRequestJSONResponse(invalidField("code", "code is required."))}, nil
	}
	o, sess, err := s.Owners.RedeemSignInLink(ctx, client(ctx), req.Body.Code)
	if err == nil {
		cookie := sessionCookie(sess.Token, sess.TTL)
		return RedeemSignInLink200JSONResponse{Body: meBody(o), Headers: RedeemSignInLink200ResponseHeaders{SetCookie: &cookie}}, nil
	}
	if e, ok := classifyOwnerError(err); ok {
		switch e.status {
		case http.StatusBadRequest:
			return RedeemSignInLink400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		case http.StatusTooManyRequests:
			return RedeemSignInLink429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
	}
	return nil, err
}

// SendEmailCode emails a sign-in code; the answer is the same whether or
// not the address is an owner's.
func (s *Server) SendEmailCode(ctx context.Context, req SendEmailCodeRequestObject) (SendEmailCodeResponseObject, error) {
	if req.Body == nil || req.Body.Email == "" {
		return SendEmailCode400JSONResponse{BadRequestJSONResponse(invalidField("email", "email is required."))}, nil
	}
	err := s.Owners.SendEmailCode(ctx, client(ctx), string(req.Body.Email))
	if err == nil {
		return SendEmailCode202Response{}, nil
	}
	if e, ok := classifyOwnerError(err); ok {
		switch e.status {
		case http.StatusBadRequest:
			return SendEmailCode400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		case http.StatusTooManyRequests:
			return SendEmailCode429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
	}
	return nil, err
}

// VerifyEmailCode signs in with an emailed code.
func (s *Server) VerifyEmailCode(ctx context.Context, req VerifyEmailCodeRequestObject) (VerifyEmailCodeResponseObject, error) {
	if req.Body == nil || req.Body.Email == "" {
		return VerifyEmailCode400JSONResponse{BadRequestJSONResponse(invalidField("email", "email is required."))}, nil
	}
	if req.Body.Code == "" {
		return VerifyEmailCode400JSONResponse{BadRequestJSONResponse(invalidField("code", "code is required."))}, nil
	}
	o, sess, err := s.Owners.VerifyEmailCode(ctx, client(ctx), string(req.Body.Email), req.Body.Code)
	if err == nil {
		cookie := sessionCookie(sess.Token, sess.TTL)
		return VerifyEmailCode200JSONResponse{Body: meBody(o), Headers: VerifyEmailCode200ResponseHeaders{SetCookie: &cookie}}, nil
	}
	if e, ok := classifyOwnerError(err); ok {
		switch e.status {
		case http.StatusBadRequest:
			return VerifyEmailCode400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		case http.StatusTooManyRequests:
			return VerifyEmailCode429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
	}
	return nil, err
}

// SignOut ends the current dashboard session, or with an API token
// revokes that token (`wardlock logout`).
func (s *Server) SignOut(ctx context.Context, _ SignOutRequestObject) (SignOutResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	ri := info(ctx)
	if ok && ri.tokenID != "" {
		if err := s.Tokens.Revoke(ctx, ownerID, ri.tokenID); err != nil && !errors.Is(err, owners.ErrNotFound) {
			return nil, err
		}
		return SignOut204Response{}, nil
	}
	if !ok || ri.sessionID == "" {
		return SignOut401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if err := s.Owners.SignOut(ctx, ownerID, ri.sessionID); err != nil && !errors.Is(err, owners.ErrNotFound) {
		return nil, err
	}
	cookie := clearSessionCookie()
	return SignOut204Response{Headers: SignOut204ResponseHeaders{SetCookie: &cookie}}, nil
}

// GetMe describes the signed-in owner.
func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return GetMe401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	o, err := s.Owners.Me(ctx, ownerID)
	if errors.Is(err, owners.ErrUnauthenticated) {
		return GetMe401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if err != nil {
		return nil, err
	}
	me := meBody(o)
	if ri := info(ctx); ri.tokenID != "" {
		me.Credential, me.Scopes = MeCredentialApiToken, scopesBody(ri.scopes)
	}
	return GetMe200JSONResponse(me), nil
}

// InviteOwner invites another owner by email.
func (s *Server) InviteOwner(ctx context.Context, req InviteOwnerRequestObject) (InviteOwnerResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return InviteOwner401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	if req.Body == nil || req.Body.Email == "" {
		return InviteOwner400JSONResponse{BadRequestJSONResponse(invalidField("email", "email is required."))}, nil
	}
	err := s.Owners.Invite(ctx, ownerID, string(req.Body.Email))
	if err == nil {
		return InviteOwner202Response{}, nil
	}
	if e, ok := classifyOwnerError(err); ok {
		switch e.status {
		case http.StatusBadRequest:
			return InviteOwner400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		case http.StatusTooManyRequests:
			return InviteOwner429JSONResponse{RateLimitedJSONResponse{Body: e.body, Headers: RateLimitedResponseHeaders{RetryAfter: e.retryAfter}}}, nil
		}
	}
	return nil, err
}

// ListSessions lists the signed-in owner's dashboard sessions.
func (s *Server) ListSessions(ctx context.Context, req ListSessionsRequestObject) (ListSessionsResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return ListSessions401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	limit := 50
	if req.Params.Limit != nil {
		limit = int(*req.Params.Limit)
	}
	if limit < 1 || limit > 100 {
		return ListSessions400JSONResponse{BadRequestJSONResponse(invalidField("limit", "limit must be between 1 and 100."))}, nil
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	items, next, err := s.Owners.ListSessions(ctx, ownerID, info(ctx).sessionID, cursor, limit)
	if err != nil {
		if e, ok := classifyOwnerError(err); ok && e.status == http.StatusBadRequest {
			return ListSessions400JSONResponse{BadRequestJSONResponse(e.body)}, nil
		}
		return nil, err
	}
	page := ListSessions200JSONResponse{Items: make([]OwnerSession, len(items))}
	for i, it := range items {
		page.Items[i] = OwnerSession{
			Id:         it.ID,
			CreatedAt:  it.CreatedAt.UnixMilli(),
			LastUsedAt: it.LastUsedAt.UnixMilli(),
			ExpiresAt:  it.ExpiresAt.UnixMilli(),
			Current:    it.Current,
		}
		if it.UserAgent != "" {
			page.Items[i].UserAgent = &it.UserAgent
		}
	}
	if next != "" {
		page.NextCursor = &next
	}
	return page, nil
}

// RevokeSession revokes one of the signed-in owner's sessions.
func (s *Server) RevokeSession(ctx context.Context, req RevokeSessionRequestObject) (RevokeSessionResponseObject, error) {
	ownerID, ok := OwnerID(ctx)
	if !ok {
		return RevokeSession401JSONResponse{UnauthenticatedJSONResponse(unauthenticatedError())}, nil
	}
	err := s.Owners.RevokeSession(ctx, ownerID, req.SessionId)
	if errors.Is(err, owners.ErrNotFound) {
		return RevokeSession404JSONResponse{NotFoundJSONResponse(Error{Code: "not_found", Message: "Not found."})}, nil
	}
	if err != nil {
		return nil, err
	}
	return RevokeSession204Response{}, nil
}
