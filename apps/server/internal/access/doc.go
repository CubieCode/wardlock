// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package access will own access policies, share links, site sessions,
// password, email-code and OAuth checks (roadmap A2). It never imports the
// HTTP layer: the edge passes it plain values and acts on plain results.
// Load the wardlock-access skill before changing anything here.
package access
