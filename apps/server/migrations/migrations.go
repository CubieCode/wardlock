// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package migrations embeds the numbered SQL migrations. Files are named
// NNNN_snake_case.sql, applied in order, forward only, and never edited once
// merged. See docs/ARCHITECTURE.md (Database).
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS
