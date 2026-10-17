// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest';
import { checkHeader, licenceFor } from './check-boundaries.mjs';

describe('licence areas', () => {
  it('maps directories to licences', () => {
    expect(licenceFor('apps/server/cmd/wardlockd/main.go')).toBe(
      'AGPL-3.0-only',
    );
    expect(licenceFor('packages/ui/src/index.ts')).toBe('AGPL-3.0-only');
    expect(licenceFor('apps/cli/cmd/wardlock/main.go')).toBe('MIT');
    expect(licenceFor('packages/apiclient-ts/src/index.ts')).toBe('MIT');
  });

  it('requires the matching SPDX header', () => {
    const agpl = '// SPDX-License-Identifier: AGPL-3.0-only\npackage x\n';
    expect(checkHeader('apps/server/x.go', agpl)).toEqual([]);
    expect(checkHeader('apps/cli/x.go', agpl)).toHaveLength(1);
    expect(checkHeader('apps/server/x.go', 'package x\n')).toHaveLength(1);
    expect(checkHeader('docs/SPEC.md', '# Spec')).toEqual([]);
  });

  it('accepts a trailing header on SQL query files', () => {
    const sql =
      '-- name: X :one\nSELECT 1;\n\n-- SPDX-License-Identifier: AGPL-3.0-only\n';
    expect(
      checkHeader('apps/server/internal/store/queries/x.sql', sql),
    ).toEqual([]);
  });
});
