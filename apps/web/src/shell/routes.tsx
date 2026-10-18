// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import type { ReactNode } from 'react';
import { PasskeysPage } from '../auth/passkeys.js';
import { LinkAccessLogPage } from '../links/access-log-page.js';
import { OverviewPage } from '../overview/overview.js';
import { SitePage } from '../sites/site-page.js';
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
  {
    path: '/sites/:name',
    // A key per site, so moving between sites starts from a fresh page.
    render: ({ api, params }) => (
      <SitePage key={params.name} api={api} name={params.name!} />
    ),
  },
  {
    path: '/sites/:name/links/:linkId',
    render: ({ api, params }) => (
      <LinkAccessLogPage
        key={`${params.name}/${params.linkId}`}
        api={api}
        name={params.name!}
        publicId={params.linkId!}
      />
    ),
  },
  {
    path: '/passkeys',
    nav: 'Passkeys',
    render: ({ api }) => <PasskeysPage api={api} />,
  },
];
