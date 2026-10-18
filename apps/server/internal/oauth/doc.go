// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package oauth talks to the operator's OAuth and OIDC providers (A2.5):
// GitHub, Google and a generic OIDC issuer. Each provider builds the
// authorisation URL (with PKCE S256, state and, for OIDC, a nonce) and
// redeems the provider's code for a verified identity: verified email
// addresses and, for GitHub, organisation memberships. It implements
// access.Provider; package access keeps every allow and deny, and this
// package never sees policies, sites or cookies. Errors name what failed,
// never a code, token or secret.
package oauth
