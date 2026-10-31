// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';

/** Every site's name, following the pages; rejects if any page fails. */
export async function loadAllSiteNames(api: ApiClient) {
  const names: string[] = [];
  let cursor: string | undefined;
  do {
    const { data } = await api.GET('/api/v0/sites', {
      params: { query: { limit: 100, ...(cursor ? { cursor } : {}) } },
    });
    if (!data) throw new Error('could not list sites');
    names.push(...data.items.map((site) => site.name));
    cursor = data.nextCursor;
  } while (cursor);
  return names;
}
