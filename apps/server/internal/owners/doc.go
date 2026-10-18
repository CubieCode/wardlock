// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package owners owns dashboard and API authentication: owner accounts,
// bootstrap (the setup URL and `wardlockd admin create`), invitations,
// email-code sign-in and owner sessions (C1.1); passkeys (C1.6,
// passkeys.go); API tokens in the shared wl_<public id>_<secret> format and
// the CLI device login (C1.7, Tokens).
//
// It works on plain values. internal/api reads cookies and headers, passes
// the raw cookie token and the client address in, and turns the sentinel
// errors here into HTTP responses. Codes, sign-in URLs and session tokens
// are random, shown once and stored only as HMAC-SHA-256 keyed by the
// server secret; none of them is ever logged.
package owners
