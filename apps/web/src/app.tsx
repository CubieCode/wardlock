// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Notice, PanelLayout } from '@wardlock/ui';
import { SessionProvider, useSession } from './auth/session.js';
import { SetupPage } from './auth/setup.js';
import { SignInPage, signInPath } from './auth/sign-in.js';
import { Redirect } from './shell/page.js';
import { matchPath, useLinkInterception, useLocation } from './shell/router.js';
import { shellRoutes } from './shell/routes.js';
import { NotFoundPage, Shell } from './shell/shell.js';

export function App({ api }: { api: ApiClient }) {
  return (
    <SessionProvider api={api}>
      <Routes />
    </SessionProvider>
  );
}

function Routes() {
  useLinkInterception();
  const { session, api, refresh } = useSession();
  const { pathname, params } = useLocation();

  if (session.status === 'loading')
    return (
      <PanelLayout productName="Wardlock">
        <p role="status">Loading…</p>
      </PanelLayout>
    );
  if (session.status === 'unreachable')
    return (
      <PanelLayout productName="Wardlock">
        <h1 className="auth-title">Could not reach the server</h1>
        <Notice tone="danger">
          Check that wardlockd is running and that this address is its
          dashboard, then try again.
        </Notice>
        <Button type="button" variant="primary" onClick={refresh}>
          Try again
        </Button>
      </PanelLayout>
    );

  // Pages reached signed out. The one-time links in setup and sign-in URLs
  // land here (docs/SPEC.md, "One-time URL paths").
  if (pathname === '/sign-in') return <SignInPage />;
  if (pathname === '/setup') return <SetupPage />;

  if (session.status === 'signed-out') {
    const here = pathname + (params.size ? `?${params}` : '');
    return <Redirect to={signInPath(here)} />;
  }

  for (const route of shellRoutes) {
    const routeParams = matchPath(route.path, pathname);
    if (routeParams)
      return (
        <Shell me={session.me} pathname={pathname}>
          {route.render({ api, params: routeParams, query: params })}
        </Shell>
      );
  }
  return (
    <Shell me={session.me} pathname={pathname}>
      <NotFoundPage />
    </Shell>
  );
}
