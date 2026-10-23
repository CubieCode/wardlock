// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import type { ReactNode } from 'react';
import { PasskeysPage } from '../auth/passkeys.js';
import { OverviewPage } from '../overview/overview.js';
import { SitePage } from '../sites/site-page.js';
import { siteTabs } from '../sites/site-page-tabs.js';
import { SitesPage } from '../sites/sites-page.js';
import { overviewIcon, passkeysIcon, sitesIcon } from './icons.js';

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
  /**
   * The screen's place in the rail, bottom bar and jump-to. Only built
   * screens get one; omit it for pages reached from another page.
   */
  rail?: { label: string; icon: ReactNode };
  /** Shows the sites navigator beside the page. */
  navigator?: boolean;
  render: (context: RouteContext) => ReactNode;
}

/**
 * Screens shown inside the signed-in shell, in rail order. A signed-out
 * visitor to any of them is sent to sign-in and returned afterwards. Add a
 * screen here when it is built, never before.
 */
export const shellRoutes: ShellRoute[] = [
  {
    path: '/',
    rail: { label: 'Overview', icon: overviewIcon },
    render: ({ api }) => <OverviewPage api={api} />,
  },
  {
    path: '/sites',
    rail: { label: 'Sites', icon: sitesIcon },
    navigator: true,
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
    rail: { label: 'Passkeys', icon: passkeysIcon },
    render: ({ api }) => <PasskeysPage api={api} />,
  },
];
