// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { teamOf, useLocation } from '../shell/router.js';

export type Team = components['schemas']['Team'];
export type TeamRole = components['schemas']['TeamRole'];
type SiteOwner = components['schemas']['SiteOwner'];
type Middleware = Parameters<ApiClient['use']>[0];

export type TeamsState =
  { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; teams: Team[] };

export interface Scope {
  /** The team chosen in the address; undefined for the personal account. */
  team: string | undefined;
  /** The signed-in owner's ID. */
  ownerId: string;
  teams: TeamsState;
  /** The signed-in owner's open invitations. */
  invitations: number;
  /** Reads the teams and invitations again. */
  refresh: () => Promise<void>;
}

/**
 * Outside the shell (a screen's own tests) there is the personal account
 * alone, where the owner may do everything.
 */
const personal: Scope = {
  team: undefined,
  ownerId: '',
  teams: { kind: 'ready', teams: [] },
  invitations: 0,
  refresh: () => Promise.resolve(),
};

const Context = createContext<Scope>(personal);
export { Context as ScopeContext };

export const useScope = () => use(Context);

/** `?team=` for a call that takes one, nothing for the personal account. */
export const teamQuery = (team: string | undefined) => (team ? { team } : {});

/** The caller's role in a team, undefined until it is known. */
export function roleIn(scope: Scope, team: string): TeamRole | undefined {
  return scope.teams.kind === 'ready'
    ? scope.teams.teams.find((t) => t.id === team)?.role
    : undefined;
}

const edits = (role: TeamRole | undefined) =>
  role === 'admin' || role === 'editor';

/**
 * What the caller may do in the chosen team or personal account. Controls
 * are left out, not disabled, for anything this says no to; the server
 * decides in the end. A role not yet known allows nothing.
 */
export function useTeamAccess() {
  const scope = useScope();
  if (!scope.team) return { edit: true, admin: true };
  const role = roleIn(scope, scope.team);
  return { edit: edits(role), admin: role === 'admin' };
}

/** As `useTeamAccess`, for one site: its own team decides, not the address. */
export function useSiteAccess(site: { owner: SiteOwner }) {
  const scope = useScope();
  if (site.owner.kind === 'owner') return { edit: true, admin: true };
  const role = roleIn(scope, site.owner.id);
  return { edit: edits(role), admin: role === 'admin' };
}

async function load(api: ApiClient) {
  const [teams, invitations] = await Promise.all([
    api.GET('/api/v0/teams'),
    api.GET('/api/v0/team-invitations'),
  ]);
  if (!teams.data) throw new Error('could not list teams');
  return {
    teams: teams.data.items,
    invitations: invitations.data?.items.length ?? 0,
  };
}

/**
 * Reads the owner's teams for the switcher and for each team's role. Every
 * screen under it reads which team is chosen from here. A refused call means
 * a role changed since the teams were read, so they are read again and the
 * controls it no longer allows go.
 */
export function ScopeProvider({
  api,
  ownerId,
  children,
}: {
  api: ApiClient;
  ownerId: string;
  children: ReactNode;
}) {
  const { pathname, params } = useLocation();
  const team = teamOf(pathname, params);
  const [teams, setTeams] = useState<TeamsState>({ kind: 'loading' });
  const [invitations, setInvitations] = useState(0);
  const latest = useRef(0);

  const refresh = useCallback(() => {
    const started = ++latest.current;
    return load(api).then(
      (loaded) => {
        if (started !== latest.current) return;
        setTeams({ kind: 'ready', teams: loaded.teams });
        setInvitations(loaded.invitations);
      },
      () => {
        if (started === latest.current) setTeams({ kind: 'error' });
      },
    );
  }, [api]);

  useEffect(() => {
    void refresh();
    const middleware: Middleware = {
      onResponse({ response }) {
        if (response.status === 403) void refresh();
      },
    };
    api.use(middleware);
    return () => {
      latest.current += 1;
      api.eject(middleware);
    };
  }, [api, refresh]);

  const value = useMemo(
    () => ({ team, ownerId, teams, invitations, refresh }),
    [team, ownerId, teams, invitations, refresh],
  );
  return <Context value={value}>{children}</Context>;
}
