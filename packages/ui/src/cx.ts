// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

/** Joins truthy class names. */
export function cx(...names: (string | false | null | undefined)[]) {
  return names.filter(Boolean).join(' ');
}
