// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import type { ReactNode } from 'react';
import { PasskeysPage } from '../auth/passkeys.js';
import { OverviewPage } from '../overview/overview.js';
import { SitePage } from '../sites/site-page.js';
import { siteTabs } from '../sites/site-page-tabs.js';
import { SitesPage } from '../sites/sites-page.js';

export interface RouteContext {
  api: ApiClient;
  /** Parameters from the pattern, such as `name` for `/sites/:name`. */
  params: Record<string, string>;
  /** The query string. */
  query: URLSearchParams;
}

export interface ShellRoute {
  /** Path pattern; `:name` segments become parameters. */
  path: string;
  /** Sidebar label. Only built screens get one; omit for unlisted pages. */
  nav?: string;
  render: (context: RouteContext) => ReactNode;
}

/**
 * Screens shown inside the signed-in shell, in sidebar order. A signed-out
 * visitor to any of them is sent to sign-in and returned afterwards. Add a
 * screen here when it is built, never before.
 */
export const shellRoutes: ShellRoute[] = [
  {
    path: '/',
    nav: 'Overview',
    render: ({ api }) => <OverviewPage api={api} />,
  },
  {
    path: '/sites',
    nav: 'Sites',
    render: ({ api }) => <SitesPage api={api} />,
  },
  // One entry per tab. The same key for every tab of a site keeps the page,
  // and the site it read, while the owner moves between tabs; another site
  // starts from a fresh page.
  ...siteTabs.map((tab): ShellRoute => ({
    path: tab.path ? `/sites/:name/${tab.path}` : '/sites/:name',
    render: ({ api, params }) => (
      <SitePage key={params.name} api={api} name={params.name!} tab={tab.id} />
    ),
  })),
  {
    path: '/sites/:name/links/:linkId',
    render: ({ api, params }) => (
      <SitePage
        key={params.name}
        api={api}
        name={params.name!}
        tab="links"
        linkId={params.linkId!}
      />
    ),
  },
  {
    path: '/passkeys',
    nav: 'Passkeys',
    render: ({ api }) => <PasskeysPage api={api} />,
  },
];
