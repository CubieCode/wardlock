// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package access owns site access: policies and their evaluation, and the
// site session cookie primitives (A2.1); share links, password, email-code
// and OAuth checks join it in later A2 items. It never imports the HTTP
// layer: the edge passes it plain values (site, path, cookie value) and acts
// on the plain Decision it returns. Load the wardlock-access skill before
// changing anything here.
package access
