// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  Button,
  Notice,
  Popover,
  RailButton,
  SegmentedControl,
} from '@wardlock/ui';
import { useState } from 'react';
import { useSession, type Me } from '../auth/session.js';
import { accountIcon } from './icons.js';
import { useTheme, type Theme } from './theme.js';

const themes: { value: Theme; label: string }[] = [
  { value: 'system', label: 'System' },
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
];

/**
 * The account place at the foot of the rail and bottom bar: who is signed
 * in, the theme for this device and Sign out.
 */
export function AccountMenu({ me }: { me: Me }) {
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
        <RailButton icon={accountIcon} label="Account" {...props} />
      )}
    >
      <div className="shell-account">
        <p className="shell-owner">
          <span className="shell-owner-label">Signed in as</span>
          <span className="shell-owner-email" title={me.owner.email}>
            {me.owner.email}
          </span>
        </p>
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
