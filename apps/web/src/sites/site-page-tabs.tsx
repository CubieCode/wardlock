// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Tab, Tabs } from '@wardlock/ui';

/** The site page's tabs in order, each at `/sites/<name>/<path>`. */
export const siteTabs = [
  { id: 'overview', path: '', label: 'Overview' },
  { id: 'links', path: 'links', label: 'Links' },
  { id: 'access', path: 'access', label: 'Access' },
  { id: 'domains', path: 'domains', label: 'Domains' },
  { id: 'deploys', path: 'deploys', label: 'Deploys' },
  { id: 'access-log', path: 'access-log', label: 'Access log' },
  { id: 'settings', path: 'settings', label: 'Settings' },
] as const;

export type SiteTabId = (typeof siteTabs)[number]['id'];

/** A path on a site's page, such as `access` or `links/<id>`. */
export const siteHref = (name: string, path = '') =>
  `/sites/${encodeURIComponent(name)}${path ? `/${path}` : ''}`;

/** The row of tabs under the site header. */
export function SiteTabs({ name, tab }: { name: string; tab: SiteTabId }) {
  return (
    <Tabs label="Site sections">
      {siteTabs.map((t) => (
        <Tab key={t.id} href={siteHref(name, t.path)} current={t.id === tab}>
          {t.label}
        </Tab>
      ))}
    </Tabs>
  );
}
