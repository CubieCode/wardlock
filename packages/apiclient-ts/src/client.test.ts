// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';
import { createApiClient } from './index.js';

describe('createApiClient', () => {
  it('calls the generated health operation', async () => {
    const seen: string[] = [];
    const client = createApiClient({
      baseUrl: 'http://wardlock.test',
      fetch: async (input) => {
        seen.push(input instanceof Request ? input.url : String(input));
        return Response.json({ status: 'ok', version: '1.0.0' });
      },
    });
    const { data } = await client.GET('/api/v0/health');
    expect(seen).toEqual(['http://wardlock.test/api/v0/health']);
    expect(data).toEqual({ status: 'ok', version: '1.0.0' });
  });
});
