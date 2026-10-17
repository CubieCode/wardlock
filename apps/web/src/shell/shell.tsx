// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  AppShell,
  Button,
  NavItem,
  NavList,
  Notice,
  PageHeader,
} from '@wardlock/ui';
import { useState, type ReactNode } from 'react';
import { useSession, type Me } from '../auth/session.js';
import { useTitle } from './page.js';
import { matchPath } from './router.js';
import { shellRoutes } from './routes.js';

/** The signed-in frame: sidebar navigation, the owner and sign-out. */
export function Shell({
  me,
  pathname,
  children,
}: {
  me: Me;
  pathname: string;
  children: ReactNode;
}) {
  const { signOut } = useSession();
  const [signingOut, setSigningOut] = useState(false);
  const [failed, setFailed] = useState(false);

  const onSignOut = async () => {
    setSigningOut(true);
    setFailed(false);
    try {
      await signOut();
    } catch {
      setFailed(true);
      setSigningOut(false);
    }
  };

  return (
    <AppShell
      productName="Wardlock"
      navigation={
        <NavList label="Main">
          {shellRoutes.map((route) =>
            route.nav ? (
              <NavItem
                key={route.path}
                href={route.path}
                current={matchPath(route.path, pathname) !== null}
              >
                {route.nav}
              </NavItem>
            ) : null,
          )}
        </NavList>
      }
      account={
        <>
          <p className="shell-owner">
            <span className="shell-owner-label">Signed in as</span>
            <span className="shell-owner-email" title={me.owner.email}>
              {me.owner.email}
            </span>
          </p>
          {failed ? (
            <Notice tone="danger">Could not sign out. Try again.</Notice>
          ) : null}
          <Button type="button" disabled={signingOut} onClick={onSignOut}>
            {signingOut ? 'Signing out…' : 'Sign out'}
          </Button>
        </>
      }
    >
      {children}
    </AppShell>
  );
}

/** Any path inside the shell that matches no screen. */
export function NotFoundPage() {
  useTitle('Page not found');
  return (
    <>
      <PageHeader
        title="Page not found"
        description="There is nothing at this address."
      />
      <p>
        <a href="/">Go to the overview</a>
      </p>
    </>
  );
}
