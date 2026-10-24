// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';

type Analytics = components['schemas']['Analytics'];

const reading = new WeakMap<ApiClient, Map<string, Promise<Analytics>>>();

/**
 * One site's analytics over the last 7 days. The site header and the access
 * log's country lookup both want it as a site page opens, so reads made
 * while one is already in flight share it; once it settles the next call
 * reads again, so nothing is kept stale.
 */
export function readWeek(api: ApiClient, site: string): Promise<Analytics> {
  const sites = reading.get(api) ?? new Map<string, Promise<Analytics>>();
  reading.set(api, sites);
  const pending = sites.get(site);
  if (pending) return pending;
  const read = api
    .GET('/api/v0/analytics', { params: { query: { site, days: 7 } } })
    .then(({ data }) => {
      if (!data) throw new Error('could not read analytics');
      return data;
    })
    .finally(() => sites.delete(site));
  sites.set(site, read);
  return read;
}
