// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  Button,
  NavItem,
  NavList,
  Notice,
  Popover,
  RailButton,
  SegmentedControl,
} from '@wardlock/ui';
import { useState, type ReactNode } from 'react';
import { useSession, type Me } from '../auth/session.js';
import { accountIcon } from './icons.js';
import { useTheme, type Theme } from './theme.js';

const themes: { value: Theme; label: string }[] = [
  { value: 'system', label: 'System' },
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
];

/** A screen's place in the rail, bottom bar or account sheet. */
export interface Place {
  href: string;
  label: string;
  icon: ReactNode;
  current: boolean;
}

/**
 * The account place at the foot of the rail and bottom bar: who is signed
 * in, the theme for this device and Sign out. Below 48rem it also lists the
 * `places` the bottom bar has no room for; while one of them is showing, the
 * Account tab is marked current.
 */
export function AccountMenu({ me, places = [] }: { me: Me; places?: Place[] }) {
  const { signOut } = useSession();
  const [open, setOpen] = useState(false);
  const [theme, setTheme] = useTheme();
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
    <Popover
      label="Account"
      open={open}
      onOpenChange={setOpen}
      placement="above"
      trigger={(props) => (
        <RailButton
          icon={accountIcon}
          label="Account"
          aria-current={places.some((p) => p.current) ? 'page' : undefined}
          {...props}
        />
      )}
    >
      <div className="shell-account">
        <p className="shell-owner">
          <span className="shell-owner-label">Signed in as</span>
          <span className="shell-owner-email" title={me.owner.email}>
            {me.owner.email}
          </span>
        </p>
        {places.length > 0 ? (
          <NavList label="More places">
            {places.map((p) => (
              <NavItem
                key={p.href}
                href={p.href}
                current={p.current}
                className="shell-nav-row shell-account-place"
                // Following a place closes the sheet.
                onClick={() => setOpen(false)}
              >
                <span className="shell-nav-icon" aria-hidden="true">
                  {p.icon}
                </span>
                <span className="shell-nav-name">{p.label}</span>
              </NavItem>
            ))}
          </NavList>
        ) : null}
        <div className="shell-theme">
          <span className="shell-theme-label" aria-hidden="true">
            Theme
          </span>
          <SegmentedControl
            label="Theme"
            value={theme}
            options={themes}
            onChange={setTheme}
          />
        </div>
        {failed ? (
          <Notice tone="danger">Could not sign out. Try again.</Notice>
        ) : null}
        <Button type="button" disabled={signingOut} onClick={onSignOut}>
          {signingOut ? 'Signing out…' : 'Sign out'}
        </Button>
      </div>
    </Popover>
  );
}
