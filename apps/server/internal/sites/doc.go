// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package sites owns sites: names and the reserved-name rules, creation,
// lookup and teardown; and their custom domains (C1.9): hostname rules,
// DNS verification through a Resolver, and the worker that re-checks them;
// and how owners organise them (V5.3): groups, tags and pins.
package sites
