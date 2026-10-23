// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  BottomBar,
  Drawer,
  Mark,
  PageHeader,
  Rail,
  RailButton,
  RailItem,
  RailLayout,
} from '@wardlock/ui';
import {
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import type { Me } from '../auth/session.js';
import { AccountMenu } from './account-menu.js';
import { filterIcon, searchIcon } from './icons.js';
import { JumpTo } from './jump-to.js';
import {
  shortcutLabel,
  SitesNavigator,
  useNavigatorData,
} from './navigator.js';
import { useTitle } from './page.js';
import { matchPath } from './router.js';
import { shellRoutes, type ShellRoute } from './routes.js';
import './shell.css';

/**
 * The signed-in frame: the rail (a bottom bar below 48rem) with each built
 * screen, Jump to and the account menu; the sites navigator beside the sites
 * pages (a drawer below 48rem); and the jump-to palette on ⌘K or Ctrl+K.
 */
export function Shell({
  api,
  me,
  pathname,
  route,
  children,
}: {
  api: ApiClient;
  me: Me;
  pathname: string;
  /** The screen shown; undefined for a page that is not found. */
  route: ShellRoute | undefined;
  children: ReactNode;
}) {
  const wide = useWide();
  const [jumping, setJumping] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const withNavigator = Boolean(route?.navigator);
  const navigator = useNavigatorData(api, withNavigator);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.key.toLowerCase() !== 'k' ||
        !(event.metaKey || event.ctrlKey) ||
        event.altKey ||
        event.shiftKey
      )
        return;
      // Leave another open dialog (a confirmation) in charge.
      if (document.querySelector('dialog[open]:not(.wl-palette)')) return;
      event.preventDefault();
      setJumping((open) => !open);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  const openJump = () => setJumping(true);
  const places = shellRoutes.map((route) =>
    route.rail ? (
      <RailItem
        key={route.path}
        href={route.path}
        icon={route.rail.icon}
        label={route.rail.label}
        current={isCurrent(route.path, pathname)}
      />
    ) : null,
  );
  const jump = (
    <RailButton
      icon={searchIcon}
      label="Jump to"
      shortcut={shortcutLabel()}
      aria-keyshortcuts="Meta+K Control+K"
      onClick={openJump}
    />
  );
  const navigatorContent = (
    <SitesNavigator
      state={navigator.state}
      onRetry={navigator.retry}
      onJump={() => {
        setDrawer(false);
        openJump();
      }}
    />
  );

  return (
    <>
      <RailLayout
        rail={
          wide ? (
            <Rail
              label="Main"
              productName="Wardlock"
              foot={
                <>
                  {jump}
                  <AccountMenu me={me} />
                </>
              }
            >
              {places}
            </Rail>
          ) : null
        }
        bottomBar={
          wide ? null : (
            <BottomBar label="Main">
              {places}
              {jump}
              <AccountMenu me={me} />
            </BottomBar>
          )
        }
        header={
          wide ? null : (
            <>
              <a className="shell-home" href="/">
                <Mark />
                Wardlock
              </a>
              {withNavigator ? (
                <button
                  type="button"
                  className="shell-browse"
                  aria-expanded={drawer}
                  onClick={() => setDrawer(true)}
                >
                  <span className="shell-nav-icon" aria-hidden="true">
                    {filterIcon}
                  </span>
                  Browse sites
                </button>
              ) : null}
            </>
          )
        }
        navigator={withNavigator && wide ? navigatorContent : undefined}
        navigatorLabel="Sites navigator"
      >
        {children}
      </RailLayout>
      {withNavigator && !wide ? (
        <Drawer
          open={drawer}
          onClose={() => setDrawer(false)}
          title="Browse sites"
        >
          {navigatorContent}
        </Drawer>
      ) : null}
      {jumping ? <JumpTo api={api} onClose={() => setJumping(false)} /> : null}
    </>
  );
}

// Wide screens get the rail and the navigator column; narrow ones the top
// and bottom bars and the drawer. The layout's CSS draws the same split, so
// only one set is ever on the page. Without matchMedia (tests), it is wide.
const wideQuery = '(min-width: 48rem)';

function subscribeWide(onChange: () => void) {
  if (!window.matchMedia) return () => {};
  const media = window.matchMedia(wideQuery);
  media.addEventListener('change', onChange);
  return () => media.removeEventListener('change', onChange);
}

const wideSnapshot = () =>
  window.matchMedia ? window.matchMedia(wideQuery).matches : true;

function useWide() {
  return useSyncExternalStore(subscribeWide, wideSnapshot);
}

/** A rail place is current on its own page and on the pages under it. */
function isCurrent(path: string, pathname: string) {
  if (matchPath(path, pathname) !== null) return true;
  return path !== '/' && pathname.startsWith(`${path}/`);
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
