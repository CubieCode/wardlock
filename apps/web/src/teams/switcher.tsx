// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Dialog,
  Menu,
  MenuCheckboxItem,
  MenuItem,
  Notice,
  Popover,
  RailButton,
  TextField,
} from '@wardlock/ui';
import { useId, useState, type FormEvent } from 'react';
import { navigate, teamPath, useLocation } from '../shell/router.js';
import { roleNames, teamRefusal } from './messages.js';
import { useScope } from './scope.js';
import './teams.css';

/** Pages that show one account's sites keep their place across a switch. */
const scoped = ['/', '/sites', '/analytics', '/organise'];

/** Where choosing a team (undefined for personal) goes from here. */
export function switchHref(pathname: string, team: string | undefined) {
  if (pathname.startsWith('/teams/')) return team ? teamPath(team) : '/';
  const place = pathname.startsWith('/sites/')
    ? '/sites'
    : scoped.includes(pathname)
      ? pathname
      : '/';
  return team ? `${place}?team=${encodeURIComponent(team)}` : place;
}

/** The first letter of a name, for the rail's square. */
const initial = (name: string) => [...name.trim()][0]?.toUpperCase() ?? '?';

/**
 * The account shown in the shell: the owner's own sites or a team's. In the
 * rail it is a square with the first letter of the name, and below 48rem a
 * button with the name in the top bar. The choice lives in the address.
 */
export function TeamSwitcher({
  api,
  compact,
}: {
  api: ApiClient;
  /** The top bar's form, below 48rem. */
  compact: boolean;
}) {
  const { pathname } = useLocation();
  const { team, teams, invitations, refresh } = useScope();
  const [open, setOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const known = teams.kind === 'ready' ? teams.teams : [];
  const current = known.find((t) => t.id === team);
  const name = team ? (current?.name ?? 'Team') : 'Personal';
  const label = `Switch team: ${name}`;

  const go = (href: string) => {
    setOpen(false);
    navigate(href);
  };

  return (
    <>
      <Popover
        label="Switch team"
        open={open}
        onOpenChange={setOpen}
        trigger={(props) =>
          compact ? (
            <button
              {...props}
              type="button"
              className="team-switch"
              aria-label={label}
            >
              <span className="team-switch-name">{name}</span>
              <span aria-hidden="true">⌄</span>
            </button>
          ) : (
            <RailButton
              {...props}
              icon={<span className="team-square">{initial(name)}</span>}
              label={label}
            />
          )
        }
      >
        <Menu label="Teams">
          <MenuCheckboxItem
            checked={!team}
            onSelect={() => go(switchHref(pathname, undefined))}
          >
            Personal
          </MenuCheckboxItem>
          {known.map((t) => (
            <MenuCheckboxItem
              key={t.id}
              checked={t.id === team}
              hint={roleNames[t.role]}
              onSelect={() => go(switchHref(pathname, t.id))}
            >
              {t.name}
            </MenuCheckboxItem>
          ))}
          {teams.kind === 'error' ? (
            <MenuItem
              hint="Could not load them"
              onSelect={() => void refresh()}
            >
              Load teams again
            </MenuItem>
          ) : null}
          {team ? (
            <MenuItem onSelect={() => go(teamPath(team))}>
              Team settings
            </MenuItem>
          ) : null}
          {invitations > 0 ? (
            <MenuItem
              hint={String(invitations)}
              onSelect={() => go('/team-invitations')}
            >
              Team invitations
            </MenuItem>
          ) : null}
          <MenuItem
            icon="+"
            onSelect={() => {
              setOpen(false);
              setCreating(true);
            }}
          >
            Create team
          </MenuItem>
        </Menu>
      </Popover>
      {creating ? (
        <CreateTeamDialog api={api} onClose={() => setCreating(false)} />
      ) : null}
    </>
  );
}

const maxName = 80;

/** Names a new team, with the owner as its admin, then opens its settings. */
function CreateTeamDialog({
  api,
  onClose,
}: {
  api: ApiClient;
  onClose: () => void;
}) {
  const { refresh } = useScope();
  const formId = useId();
  const [name, setName] = useState('');
  const [error, setError] = useState<string>();
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const trimmed = name.trim();
    setProblem(null);
    if (!trimmed) return setError('Enter a name for the team.');
    setError(undefined);
    setBusy(true);
    const result = await api
      .POST('/api/v0/teams', { body: { name: trimmed } })
      .catch(() => null);
    if (result?.data) {
      await refresh();
      onClose();
      return navigate(teamPath(result.data.id));
    }
    setBusy(false);
    if (result?.error?.target === 'name') setError(result.error.message);
    else setProblem(teamRefusal(result));
  };

  return (
    <Dialog
      open
      title="Create a team"
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy}>
            {busy ? 'Creating…' : 'Create team'}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={create} noValidate>
        <TextField
          label="Team name"
          hint="You become its admin. Invite others once it exists."
          maxLength={maxName}
          autoComplete="off"
          value={name}
          error={error}
          onChange={(event) => setName(event.target.value)}
        />
      </form>
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
    </Dialog>
  );
}
