// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import type { ReactNode } from 'react';
import { AnalyticsPage } from '../analytics/analytics-page.js';
import { analyticsIcon } from '../analytics/icon.js';
import { PasskeysPage } from '../auth/passkeys.js';
import { OrganisePage } from '../organise/organise-page.js';
import { OverviewPage } from '../overview/overview.js';
import { SitePage } from '../sites/site-page.js';
import { siteTabs } from '../sites/site-page-tabs.js';
import { SitesPage } from '../sites/sites-page.js';
import {
  organiseIcon,
  overviewIcon,
  passkeysIcon,
  sitesIcon,
} from './icons.js';

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
   * The screen's place in the rail and jump-to. Only built screens get one;
   * omit it for pages reached from another page. Below 48rem, `bar` puts it
   * in the bottom bar; every other place is listed in the Account sheet.
   */
  rail?: { label: string; icon: ReactNode; bar?: boolean };
  /** Shows the sites navigator beside the page. */
  navigator?: boolean;
  render: (context: RouteContext) => ReactNode;
}

/**
 * How many places the bottom bar holds below 48rem, beside Jump to and
 * Account: five items in all. Mark at most this many routes `bar`.
 */
export const barPlaces = 3;

/**
 * Screens shown inside the signed-in shell, in rail order. A signed-out
 * visitor to any of them is sent to sign-in and returned afterwards. Add a
 * screen here when it is built, never before.
 */
export const shellRoutes: ShellRoute[] = [
  {
    path: '/',
    rail: { label: 'Overview', icon: overviewIcon, bar: true },
    render: ({ api }) => <OverviewPage api={api} />,
  },
  {
    path: '/sites',
    rail: { label: 'Sites', icon: sitesIcon, bar: true },
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
    path: '/analytics',
    rail: { label: 'Analytics', icon: analyticsIcon, bar: true },
    render: ({ api, query }) => <AnalyticsPage api={api} query={query} />,
  },
  {
    path: '/organise',
    rail: { label: 'Groups and tags', icon: organiseIcon },
    render: ({ api }) => <OrganisePage api={api} />,
  },
  {
    path: '/passkeys',
    rail: { label: 'Passkeys', icon: passkeysIcon },
    render: ({ api }) => <PasskeysPage api={api} />,
  },
];
