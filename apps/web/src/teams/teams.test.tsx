// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { AnalyticsPage } from '../analytics/analytics-page.js';
import { App } from '../app.js';
import { SessionProvider } from '../auth/session.js';
import { DomainsCard } from '../domains/domains.js';
import { ShareLinksCard } from '../links/share-links.js';
import { OrganisePage } from '../organise/organise-page.js';
import { OverviewPage } from '../overview/overview.js';
import { PoliciesCard } from '../policies/policies.js';
import { sitesHref, useNavigatorData } from '../shell/navigator.js';
import { teamOf, withTeam } from '../shell/router.js';
import { Shell } from '../shell/shell.js';
import { MoveSiteCard } from '../sites/move-site.js';
import { OpenSiteButton } from '../sites/open-site.js';
import { DeploysTab } from '../sites/site-page-deploys.js';
import { SitePage } from '../sites/site-page.js';
import { SettingsTab } from '../sites/site-page-settings.js';
import { SitesPage } from '../sites/sites-page.js';
import { loadAllSiteNames } from '../webhooks/sites.js';
import type { Organise } from '../sites/site-page-organise.js';
import type { Site } from '../sites/format.js';
import { TeamInvitationsPage } from './invitations-page.js';
import { teamRefusal } from './messages.js';
import { ScopeContext, type Scope, type Team, type TeamRole } from './scope.js';
import { switchHref, TeamSwitcher } from './switcher.js';
import { TeamPage } from './team-page.js';

const secret = 'wl_k7m2q6xw4a_TESTonlyTESTonlyTESTonlyTESTonlyTESTonlyTESTonl';
const id = (n: string) => `01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e${n}`;
const teamId = id('90');
const otherTeam = id('91');
const ownerId = id('01');
const otherOwner = id('02');
const now = Date.UTC(2026, 9, 1);

const team = (role: TeamRole, over: Partial<Team> = {}): Team => ({
  id: teamId,
  name: 'Web team',
  role,
  createdAt: now,
  ...over,
});

const member = (over: Record<string, unknown> = {}) => ({
  ownerId,
  email: 'me@example.test',
  role: 'admin',
  createdAt: now,
  suspended: false,
  ...over,
});

const invitation = (over: Record<string, unknown> = {}) => ({
  id: id('70'),
  teamId,
  teamName: 'Web team',
  email: 'me@example.test',
  role: 'editor',
  createdAt: now,
  expiresAt: now + 7 * 24 * 3600_000,
  ...over,
});

const token = (over: Record<string, unknown> = {}) => ({
  id: id('20'),
  publicId: 'k7m2q6xw4a',
  name: 'CI deploys',
  scopes: ['deploy'],
  createdAt: now,
  ...over,
});

type Handler = (url: URL, body: unknown) => Response | Promise<Response>;

function fakeServer(routes: Record<string, Handler>) {
  const calls: { route: string; url: URL; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route, url, body });
      const handler = routes[route];
      if (handler) return handler(url, body);
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return {
    api,
    calls,
    bodyOf: (route: string) => calls.findLast((c) => c.route === route)?.body,
    called: (route: string) => calls.some((c) => c.route === route),
  };
}

const list =
  (...items: unknown[]): Handler =>
  () =>
    Response.json({ items });
const ok =
  (body: unknown, status = 200): Handler =>
  () =>
    Response.json(body, { status });
const refuse =
  (status: number, code: string, target?: string): Handler =>
  () =>
    Response.json(
      { code, message: `Server says ${code}.`, target },
      { status },
    );
const noContent: Handler = () => new Response(null, { status: 204 });

const base = `/api/v0/teams/${teamId}`;

/** The team page's reads, for a member with this role. */
const teamRoutes = (
  role: TeamRole,
  extra: Record<string, Handler> = {},
): Record<string, Handler> => ({
  [`GET ${base}`]: ok(team(role)),
  [`GET ${base}/members`]: list(
    member({ role }),
    member({
      ownerId: otherOwner,
      email: 'pat@example.test',
      role: 'editor',
    }),
  ),
  [`GET ${base}/invitations`]: list(invitation({ email: 'new@example.test' })),
  [`GET ${base}/tokens`]: list(token()),
  ...extra,
});

function scope(role: TeamRole | null, over: Partial<Scope> = {}): Scope {
  return {
    team: role ? teamId : undefined,
    ownerId,
    teams: { kind: 'ready', teams: role ? [team(role)] : [] },
    invitations: 0,
    refresh: () => Promise.resolve(),
    ...over,
  };
}

function inScope(value: Scope, ui: ReactNode) {
  return render(<ScopeContext value={value}>{ui}</ScopeContext>);
}

const teamSite = (over: Partial<Site> = {}): Site => ({
  id: id('40'),
  name: 'docs',
  owner: { kind: 'team', id: teamId, name: 'Web team' },
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now,
  unavailable: false,
  tagIds: [],
  pinned: false,
  ...over,
});

const ownSite = (over: Partial<Site> = {}): Site =>
  teamSite({
    owner: { kind: 'owner', id: ownerId, name: 'me@example.test' },
    ...over,
  });

const organise: Organise = {
  state: { kind: 'ready', groups: [], tags: [] },
  busy: false,
  error: null,
  reload: () => {},
  togglePin: () => {},
  setGroup: () => {},
  setTags: () => {},
  createTag: () => {},
};

beforeEach(() => window.history.replaceState(null, '', '/'));
afterEach(cleanup);

describe('the team in the address', () => {
  it('reads it from the query and from a team page', () => {
    expect(teamOf('/sites', new URLSearchParams(`team=${teamId}`))).toBe(
      teamId,
    );
    expect(teamOf(`/teams/${teamId}`, new URLSearchParams())).toBe(teamId);
    expect(teamOf('/sites', new URLSearchParams())).toBeUndefined();
  });

  it('never scopes a page of the owner’s own, whatever its query says', () => {
    const query = new URLSearchParams(`team=${teamId}`);
    for (const path of [
      '/tokens',
      '/passkeys',
      '/notifications',
      '/webhooks',
      '/webhooks/x',
      '/team-invitations',
    ])
      expect(teamOf(path, query)).toBeUndefined();
    window.history.replaceState(null, '', `/sites?team=${teamId}`);
    expect(withTeam('/tokens')).toBe('/tokens');
    expect(withTeam('/sites/docs/links')).toBe(
      `/sites/docs/links?team=${teamId}`,
    );
    window.history.replaceState(null, '', `/tokens?team=${teamId}`);
    expect(withTeam('/sites')).toBe('/sites');
  });

  it('keeps the chosen team on links, and leaves personal ones alone', () => {
    expect(withTeam('/sites')).toBe('/sites');
    window.history.replaceState(null, '', `/sites?team=${teamId}`);
    expect(withTeam('/analytics')).toBe(`/analytics?team=${teamId}`);
    expect(sitesHref({ v: 'public' })).toBe(`/sites?v=public&team=${teamId}`);
    window.history.replaceState(null, '', `/teams/${teamId}`);
    expect(withTeam('/')).toBe(`/?team=${teamId}`);
  });

  it('keeps a scoped page across a switch and leaves a site for the list', () => {
    expect(switchHref('/analytics', teamId)).toBe(`/analytics?team=${teamId}`);
    expect(switchHref('/organise', undefined)).toBe('/organise');
    expect(switchHref('/sites/docs/links', teamId)).toBe(
      `/sites?team=${teamId}`,
    );
    expect(switchHref('/tokens', teamId)).toBe(`/?team=${teamId}`);
    expect(switchHref('/tokens', undefined)).toBe('/tokens');
    expect(switchHref(`/teams/${teamId}`, otherTeam)).toBe(
      `/teams/${otherTeam}`,
    );
    expect(switchHref(`/teams/${teamId}`, undefined)).toBe('/');
  });
});

describe('team switcher', () => {
  const open = async (name: RegExp | string) =>
    fireEvent.click(await screen.findByRole('button', { name }));

  it('lists personal and each team with its role, marking the current', async () => {
    const { api } = fakeServer({});
    window.history.replaceState(null, '', `/?team=${teamId}`);
    inScope(
      scope('viewer', {
        teams: {
          kind: 'ready',
          teams: [
            team('viewer'),
            team('admin', { id: otherTeam, name: 'Ops' }),
          ],
        },
      }),
      <TeamSwitcher api={api} compact={false} />,
    );
    await open('Switch team: Web team');
    const items = screen.getAllByRole('menuitemcheckbox');
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining('Personal'),
      expect.stringContaining('Web team'),
      expect.stringContaining('Ops'),
    ]);
    expect(items[1]!.getAttribute('aria-checked')).toBe('true');
    expect(items[1]!.textContent).toContain('Viewer');
    expect(
      screen.getByRole('menuitem', { name: 'Team settings' }),
    ).toBeTruthy();
    fireEvent.click(items[2]!);
    expect(window.location.search).toBe(`?team=${otherTeam}`);
  });

  it('offers creating a team to an owner in none, and no team settings', async () => {
    const { api } = fakeServer({});
    inScope(scope(null), <TeamSwitcher api={api} compact />);
    await open('Switch team: Personal');
    expect(screen.getAllByRole('menuitemcheckbox')).toHaveLength(1);
    expect(
      screen.queryByRole('menuitem', { name: 'Team settings' }),
    ).toBeNull();
    expect(
      screen.queryByRole('menuitem', { name: /Team invitations/ }),
    ).toBeNull();
    expect(screen.getByRole('menuitem', { name: /Create team/ })).toBeTruthy();
  });

  it('counts open invitations and links to them', async () => {
    const { api } = fakeServer({});
    inScope(
      scope(null, { invitations: 2 }),
      <TeamSwitcher api={api} compact={false} />,
    );
    await open('Switch team: Personal');
    const item = screen.getByRole('menuitem', { name: /Team invitations/ });
    expect(item.textContent).toContain('2');
    fireEvent.click(item);
    expect(window.location.pathname).toBe('/team-invitations');
  });

  it('says when the teams could not load and reads them again', async () => {
    const { api } = fakeServer({});
    let again = 0;
    inScope(
      scope(null, {
        teams: { kind: 'error' },
        refresh: () => {
          again += 1;
          return Promise.resolve();
        },
      }),
      <TeamSwitcher api={api} compact={false} />,
    );
    await open('Switch team: Personal');
    fireEvent.click(screen.getByRole('menuitem', { name: /Load teams again/ }));
    expect(again).toBe(1);
  });

  it('creates a team, reads the teams again and opens its settings', async () => {
    const { api, bodyOf } = fakeServer({
      'POST /api/v0/teams': ok(team('admin'), 201),
    });
    let read = 0;
    inScope(
      scope(null, {
        refresh: () => {
          read += 1;
          return Promise.resolve();
        },
      }),
      <TeamSwitcher api={api} compact={false} />,
    );
    await open('Switch team: Personal');
    fireEvent.click(screen.getByRole('menuitem', { name: /Create team/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Create team' }));
    expect(await screen.findByText('Enter a name for the team.')).toBeTruthy();
    fireEvent.change(screen.getByLabelText(/Team name/), {
      target: { value: ' Web team ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create team' }));
    await waitFor(() =>
      expect(window.location.pathname).toBe(`/teams/${teamId}`),
    );
    expect(bodyOf('POST /api/v0/teams')).toEqual({ name: 'Web team' });
    expect(read).toBe(1);
  });

  it('shows why a team could not be created', async () => {
    const { api } = fakeServer({
      'POST /api/v0/teams': refuse(413, 'limit_exceeded'),
    });
    inScope(scope(null), <TeamSwitcher api={api} compact={false} />);
    await open('Switch team: Personal');
    fireEvent.click(screen.getByRole('menuitem', { name: /Create team/ }));
    fireEvent.change(screen.getByLabelText(/Team name/), {
      target: { value: 'One too many' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create team' }));
    expect(await screen.findByText('Server says limit_exceeded.')).toBeTruthy();
  });
});

describe('shell', () => {
  const me = {
    owner: { id: ownerId, email: 'me@example.test', createdAt: now },
    credential: 'session' as const,
    scopes: ['deploy' as const, 'manage' as const],
  };

  it('says so instead of a page for a team the owner is not in', async () => {
    window.history.replaceState(null, '', `/?team=${otherTeam}`);
    const { api } = fakeServer({
      'GET /api/v0/teams': list(team('admin')),
      'GET /api/v0/team-invitations': list(),
    });
    render(
      <SessionProvider api={api}>
        <Shell api={api} me={me} pathname="/" route={undefined}>
          <p>A page</p>
        </Shell>
      </SessionProvider>,
    );
    expect(
      await screen.findByRole('heading', { name: 'Team not found' }),
    ).toBeTruthy();
    expect(screen.queryByText('A page')).toBeNull();
  });

  it('shows Personal on a page of the owner’s own, whatever its query says', async () => {
    window.history.replaceState(null, '', `/tokens?team=${teamId}`);
    const { api } = fakeServer({
      'GET /api/v0/teams': list(team('admin')),
      'GET /api/v0/team-invitations': list(),
    });
    render(
      <SessionProvider api={api}>
        <Shell api={api} me={me} pathname="/tokens" route={undefined}>
          <p>A page</p>
        </Shell>
      </SessionProvider>,
    );
    expect(
      await screen.findByRole('button', { name: 'Switch team: Personal' }),
    ).toBeTruthy();
    const main = screen.getAllByRole('navigation', { name: 'Main' })[0]!;
    expect(
      within(main).getByRole('link', { name: 'Sites' }).getAttribute('href'),
    ).toBe('/sites');
  });

  it('carries the team on its places', async () => {
    window.history.replaceState(null, '', `/?team=${teamId}`);
    const { api } = fakeServer({
      'GET /api/v0/teams': list(team('admin')),
      'GET /api/v0/team-invitations': list(),
    });
    render(
      <SessionProvider api={api}>
        <Shell api={api} me={me} pathname="/" route={undefined}>
          <p>A page</p>
        </Shell>
      </SessionProvider>,
    );
    expect(await screen.findByText('A page')).toBeTruthy();
    const main = screen.getAllByRole('navigation', { name: 'Main' })[0]!;
    expect(
      within(main).getByRole('link', { name: 'Sites' }).getAttribute('href'),
    ).toBe(`/sites?team=${teamId}`);
    expect(
      within(main)
        .getByRole('link', { name: 'API tokens' })
        .getAttribute('href'),
    ).toBe('/tokens');
  });

  it('never shows one team’s counts under another', async () => {
    let answer: (response: Response) => void = () => {};
    const { api } = fakeServer({
      'GET /api/v0/site-counts': (url) =>
        url.searchParams.get('team') === otherTeam
          ? new Promise((resolve) => (answer = resolve))
          : ok({
              all: 7,
              views: {
                pinned: 0,
                expiring: 0,
                public: 0,
                quiet: 0,
                untagged: 0,
                down: 0,
              },
              groups: [],
              noGroup: 0,
              tags: [],
            })(url, undefined),
      'GET /api/v0/groups': list(),
      'GET /api/v0/tags': list(),
    });
    const { result, rerender } = renderHook(
      ({ team: chosen }) => useNavigatorData(api, true, true, chosen),
      { initialProps: { team: teamId } },
    );
    await waitFor(() => expect(result.current.state.kind).toBe('ready'));
    rerender({ team: otherTeam });
    expect(result.current.state.kind).toBe('loading');
    answer(Response.json({ code: 'internal', message: 'x' }, { status: 500 }));
  });
});

describe('team settings', () => {
  it('shows a loading state, then the team to an admin with everything', async () => {
    const { api } = fakeServer(teamRoutes('admin'));
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    expect(screen.getByRole('status').textContent).toBe('Loading team…');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Web team' }),
    ).toBeTruthy();
    expect(screen.getByText('You are an admin of this team.')).toBeTruthy();
    for (const title of [
      'Team name',
      'Members',
      'Invitations',
      'Team tokens',
      'Leave this team',
      'Delete this team',
    ])
      expect(screen.getByRole('heading', { name: title })).toBeTruthy();
    expect(
      await screen.findByLabelText('Role of pat@example.test'),
    ).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Remove pat@example.test' }),
    ).toBeTruthy();
  });

  it.each(['editor', 'viewer'] as const)(
    'leaves the admin controls out for a %s',
    async (role) => {
      const { api, called } = fakeServer(teamRoutes(role));
      inScope(scope(role), <TeamPage api={api} teamId={teamId} />);
      expect(
        await screen.findByRole('heading', { name: 'Members' }),
      ).toBeTruthy();
      expect(await screen.findByText('pat@example.test')).toBeTruthy();
      for (const title of [
        'Team name',
        'Invitations',
        'Team tokens',
        'Delete this team',
      ])
        expect(screen.queryByRole('heading', { name: title })).toBeNull();
      expect(screen.queryByLabelText(/^Role of/)).toBeNull();
      expect(screen.queryByRole('button', { name: /^Remove/ })).toBeNull();
      expect(screen.getByRole('button', { name: 'Leave team' })).toBeTruthy();
      expect(called(`GET ${base}/tokens`)).toBe(false);
      expect(called(`GET ${base}/invitations`)).toBe(false);
    },
  );

  it('says a team it cannot see is not found', async () => {
    const { api } = fakeServer({});
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    expect(
      await screen.findByRole('heading', { name: 'Team not found' }),
    ).toBeTruthy();
  });

  it('offers to try again when the team cannot load', async () => {
    let up = false;
    const { api } = fakeServer({
      ...teamRoutes('viewer'),
      [`GET ${base}`]: (url, body) =>
        up ? ok(team('viewer'))(url, body) : refuse(500, 'internal')(url, body),
    });
    inScope(scope('viewer'), <TeamPage api={api} teamId={teamId} />);
    const button = await screen.findByRole('button', { name: 'Try again' });
    up = true;
    fireEvent.click(button);
    expect(
      await screen.findByRole('heading', { name: 'Web team' }),
    ).toBeTruthy();
  });

  it('renames the team', async () => {
    const { api, bodyOf } = fakeServer({
      ...teamRoutes('admin'),
      [`PATCH ${base}`]: ok(team('admin', { name: 'Docs team' })),
    });
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.change(await screen.findByLabelText(/^Name/), {
      target: { value: 'Docs team' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Docs team' }),
    ).toBeTruthy();
    expect(bodyOf(`PATCH ${base}`)).toEqual({ name: 'Docs team' });
  });
});

describe('members', () => {
  it('marks a suspended member and the signed-in owner', async () => {
    const { api } = fakeServer(
      teamRoutes('viewer', {
        [`GET ${base}/members`]: list(
          member({ role: 'viewer' }),
          member({
            ownerId: otherOwner,
            email: 'pat@example.test',
            suspended: true,
          }),
        ),
      }),
    );
    inScope(scope('viewer'), <TeamPage api={api} teamId={teamId} />);
    expect(await screen.findByText('Suspended')).toBeTruthy();
    expect(screen.getByText('You')).toBeTruthy();
  });

  const choose = async (email: string, role: TeamRole) =>
    fireEvent.change(await screen.findByLabelText(`Role of ${email}`), {
      target: { value: role },
    });

  it('changes a role only on Save, not while choosing', async () => {
    const route = `PATCH ${base}/members/${otherOwner}`;
    const { api, bodyOf, called } = fakeServer(
      teamRoutes('admin', {
        [route]: ok(
          member({
            ownerId: otherOwner,
            email: 'pat@example.test',
            role: 'viewer',
          }),
        ),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await choose('pat@example.test', 'admin');
    await choose('pat@example.test', 'viewer');
    expect(called(route)).toBe(false);
    fireEvent.click(
      screen.getByRole('button', { name: 'Save the role of pat@example.test' }),
    );
    await waitFor(() => expect(bodyOf(route)).toEqual({ role: 'viewer' }));
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: 'Save the role of pat@example.test',
        }),
      ).toBeNull(),
    );
    expect(
      (screen.getByLabelText('Role of pat@example.test') as HTMLSelectElement)
        .value,
    ).toBe('viewer');
  });

  it('offers no Save once the role is back as it was', async () => {
    const { api } = fakeServer(teamRoutes('admin'));
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await choose('pat@example.test', 'viewer');
    await choose('pat@example.test', 'editor');
    expect(screen.queryByRole('button', { name: /^Save the role/ })).toBeNull();
  });

  it('shows a refused change once, on the card', async () => {
    const { api } = fakeServer(
      teamRoutes('admin', {
        [`PATCH ${base}/members/${otherOwner}`]: refuse(403, 'forbidden_role'),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await choose('pat@example.test', 'viewer');
    fireEvent.click(
      screen.getByRole('button', { name: 'Save the role of pat@example.test' }),
    );
    expect(await screen.findAllByText(/no longer allows/)).toHaveLength(1);
  });

  it('confirms making someone an admin, naming them', async () => {
    const route = `PATCH ${base}/members/${otherOwner}`;
    const { api, bodyOf, called } = fakeServer(
      teamRoutes('admin', {
        [route]: ok(
          member({
            ownerId: otherOwner,
            email: 'pat@example.test',
            role: 'admin',
          }),
        ),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await choose('pat@example.test', 'admin');
    fireEvent.click(
      screen.getByRole('button', { name: 'Save the role of pat@example.test' }),
    );
    const dialog = screen.getByRole('dialog', {
      name: 'Make pat@example.test an admin of Web team?',
    });
    expect(called(route)).toBe(false);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Make admin' }));
    await waitFor(() => expect(bodyOf(route)).toEqual({ role: 'admin' }));
  });

  it('confirms stepping down and shows a refusal once, in the dialog', async () => {
    const route = `PATCH ${base}/members/${ownerId}`;
    const { api, called } = fakeServer(
      teamRoutes('admin', { [route]: refuse(409, 'team_needs_admin') }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await choose('me@example.test', 'editor');
    fireEvent.click(
      screen.getByRole('button', { name: 'Save the role of me@example.test' }),
    );
    const dialog = screen.getByRole('dialog', {
      name: 'Stop being an admin of Web team?',
    });
    expect(called(route)).toBe(false);
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Become an editor' }),
    );
    expect(await within(dialog).findByText(/must keep an admin/)).toBeTruthy();
    expect(screen.getAllByText(/must keep an admin/)).toHaveLength(1);
  });

  it('confirms removing someone, naming them', async () => {
    const route = `DELETE ${base}/members/${otherOwner}`;
    const { api, called } = fakeServer(
      teamRoutes('admin', { [route]: noContent }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Remove pat@example.test' }),
    );
    expect(
      screen.getByRole('dialog', {
        name: 'Remove pat@example.test from Web team?',
      }),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Remove member' }));
    await waitFor(() => expect(called(route)).toBe(true));
    await waitFor(() =>
      expect(screen.queryByText('pat@example.test')).toBeNull(),
    );
  });

  it('offers to try again when the members cannot load', async () => {
    const { api } = fakeServer(
      teamRoutes('admin', {
        [`GET ${base}/members`]: refuse(500, 'internal'),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    expect(await screen.findByText(/Could not load the members/)).toBeTruthy();
  });

  it('pages through the members', async () => {
    const { api } = fakeServer(
      teamRoutes('viewer', {
        [`GET ${base}/members`]: (url) =>
          Response.json(
            url.searchParams.get('cursor')
              ? {
                  items: [
                    member({ ownerId: id('03'), email: 'lee@example.test' }),
                  ],
                }
              : { items: [member({ role: 'viewer' })], nextCursor: 'more' },
          ),
      }),
    );
    inScope(scope('viewer'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show more members' }),
    );
    expect(await screen.findByText('lee@example.test')).toBeTruthy();
  });
});

describe('invitations on the team page', () => {
  const invite = async (email: string) => {
    fireEvent.change(await screen.findByLabelText(/Email address/), {
      target: { value: email },
    });
    fireEvent.change(screen.getByLabelText('Role'), {
      target: { value: 'viewer' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Send invitation' }));
  };

  it('invites an address with a role and lists it', async () => {
    const route = `POST ${base}/invitations`;
    const { api, bodyOf } = fakeServer(
      teamRoutes('admin', {
        [route]: ok(
          invitation({ email: 'lee@example.test', role: 'viewer' }),
          201,
        ),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await invite('lee@example.test');
    expect(await screen.findByText(/Invited lee@example.test/)).toBeTruthy();
    expect(bodyOf(route)).toEqual({
      email: 'lee@example.test',
      role: 'viewer',
    });
  });

  it('asks for an address first', async () => {
    const { api, called } = fakeServer(teamRoutes('admin'));
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await invite('  ');
    expect(
      await screen.findByText('Enter the address to invite.'),
    ).toBeTruthy();
    expect(called(`POST ${base}/invitations`)).toBe(false);
  });

  it.each([
    ['already_member', 409, /already a member/],
    ['invites_restricted', 403, /only the operator/],
    ['team_suspended', 409, /suspended this team/],
  ])('explains %s', async (code, status, words) => {
    const { api } = fakeServer(
      teamRoutes('admin', {
        [`POST ${base}/invitations`]: refuse(status, code),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await invite('lee@example.test');
    expect(await screen.findByText(words)).toBeTruthy();
  });

  it('cancels an invitation', async () => {
    const invitationId = id('70');
    const route = `DELETE ${base}/invitations/${invitationId}`;
    const { api, called } = fakeServer(
      teamRoutes('admin', { [route]: noContent }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Cancel the invitation to new@example.test',
      }),
    );
    await waitFor(() => expect(called(route)).toBe(true));
    expect(await screen.findByText(/No open invitations/)).toBeTruthy();
  });

  it('says when none are open or they cannot load', async () => {
    const empty = fakeServer(
      teamRoutes('admin', { [`GET ${base}/invitations`]: list() }),
    );
    inScope(scope('admin'), <TeamPage api={empty.api} teamId={teamId} />);
    expect(await screen.findByText('No open invitations.')).toBeTruthy();
    cleanup();
    const down = fakeServer(
      teamRoutes('admin', {
        [`GET ${base}/invitations`]: refuse(500, 'internal'),
      }),
    );
    inScope(scope('admin'), <TeamPage api={down.api} teamId={teamId} />);
    expect(
      await screen.findByText(/Could not load the invitations/),
    ).toBeTruthy();
  });
});

describe('team tokens', () => {
  it('creates one and shows the secret once, with the shared dialog', async () => {
    const route = `POST ${base}/tokens`;
    const { api, bodyOf } = fakeServer(
      teamRoutes('admin', {
        [route]: ok({ token: secret, apiToken: token({ name: 'New' }) }, 201),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await screen.findByText('CI deploys');
    fireEvent.click(
      await screen.findByRole('button', { name: 'Create team token' }),
    );
    expect(
      screen.getByRole('dialog', { name: 'Create a token for Web team' }),
    ).toBeTruthy();
    fireEvent.change(
      screen.getByLabelText(/^Name/, { selector: 'dialog input' }),
      {
        target: { value: 'New' },
      },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect(await screen.findByDisplayValue(secret)).toBeTruthy();
    expect(
      screen.getByText(/only time Wardlock shows this token/),
    ).toBeTruthy();
    expect((bodyOf(route) as { name: string }).name).toBe('New');
    // The dialog opens in an effect, after its fields are in the DOM.
    fireEvent.click(await screen.findByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByDisplayValue(secret)).toBeNull());
  });

  it('explains a suspended team', async () => {
    const { api } = fakeServer(
      teamRoutes('admin', {
        [`POST ${base}/tokens`]: refuse(409, 'team_suspended'),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await screen.findByText('CI deploys');
    fireEvent.click(
      await screen.findByRole('button', { name: 'Create team token' }),
    );
    fireEvent.change(
      screen.getByLabelText(/^Name/, { selector: 'dialog input' }),
      {
        target: { value: 'New' },
      },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create token' }));
    expect(await screen.findByText(/suspended this team/)).toBeTruthy();
  });

  it('revokes one through the team route', async () => {
    const route = `DELETE ${base}/tokens/${id('20')}`;
    const { api, called } = fakeServer(
      teamRoutes('admin', { [route]: noContent }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Revoke CI deploys' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Revoke token' }));
    await waitFor(() => expect(called(route)).toBe(true));
    expect(await screen.findByText(/No team tokens yet/)).toBeTruthy();
  });

  it('says when the tokens cannot load', async () => {
    const { api } = fakeServer(
      teamRoutes('admin', { [`GET ${base}/tokens`]: refuse(500, 'internal') }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    expect(
      await screen.findByText(/Could not load the team’s tokens/),
    ).toBeTruthy();
  });
});

describe('leaving and deleting', () => {
  it('leaves the team and goes to personal sites', async () => {
    const route = `DELETE ${base}/members/${ownerId}`;
    const { api, called } = fakeServer(
      teamRoutes('viewer', { [route]: noContent }),
    );
    window.history.replaceState(null, '', `/teams/${teamId}`);
    inScope(scope('viewer'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Leave team' }));
    const dialog = screen.getByRole('dialog', { name: 'Leave Web team?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Leave team' }));
    await waitFor(() => expect(window.location.pathname).toBe('/sites'));
    expect(called(route)).toBe(true);
  });

  it('explains why the last admin cannot leave', async () => {
    const { api } = fakeServer(
      teamRoutes('admin', {
        [`DELETE ${base}/members/${ownerId}`]: refuse(409, 'team_needs_admin'),
      }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Leave team' }));
    const dialog = screen.getByRole('dialog', { name: 'Leave Web team?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Leave team' }));
    expect(await within(dialog).findByText(/must keep an admin/)).toBeTruthy();
  });

  const typeToDelete = async () => {
    fireEvent.click(await screen.findByRole('button', { name: 'Delete team' }));
    const dialog = screen.getByRole('dialog', { name: 'Delete Web team?' });
    const confirm = within(dialog).getByRole<HTMLButtonElement>('button', {
      name: 'Delete team',
    });
    expect(confirm.disabled).toBe(true);
    fireEvent.change(
      within(dialog).getByLabelText(/Type Web team to confirm/),
      {
        target: { value: 'Web team' },
      },
    );
    fireEvent.click(confirm);
    return dialog;
  };

  it('deletes the team only once its name is typed', async () => {
    const { api, called } = fakeServer(
      teamRoutes('admin', { [`DELETE ${base}`]: noContent }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    await typeToDelete();
    await waitFor(() => expect(window.location.pathname).toBe('/sites'));
    expect(called(`DELETE ${base}`)).toBe(true);
  });

  it.each([
    ['team_has_sites', /still has sites/],
    ['team_suspended', /suspended this team/],
  ])('explains %s', async (code, words) => {
    const { api } = fakeServer(
      teamRoutes('admin', { [`DELETE ${base}`]: refuse(409, code) }),
    );
    inScope(scope('admin'), <TeamPage api={api} teamId={teamId} />);
    const dialog = await typeToDelete();
    expect(await within(dialog).findByText(words)).toBeTruthy();
  });
});

describe('invitation page', () => {
  const inviteId = id('70');
  const accept = `POST /api/v0/team-invitations/${inviteId}/accept`;
  const decline = `POST /api/v0/team-invitations/${inviteId}/decline`;
  const mine = { 'GET /api/v0/team-invitations': list(invitation()) };

  it('lists the owner’s invitations with the role offered', async () => {
    const { api } = fakeServer(mine);
    inScope(scope(null), <TeamInvitationsPage api={api} />);
    expect(screen.getByRole('status').textContent).toBe('Loading invitations…');
    expect(
      await screen.findByRole('heading', { name: 'Web team' }),
    ).toBeTruthy();
    expect(screen.getByText('editor')).toBeTruthy();
  });

  it('says when there are none', async () => {
    const { api } = fakeServer({ 'GET /api/v0/team-invitations': list() });
    inScope(scope(null), <TeamInvitationsPage api={api} />);
    expect(await screen.findByText('No open invitations')).toBeTruthy();
  });

  it('accepts, reads the teams again and opens the team', async () => {
    const { api } = fakeServer({ ...mine, [accept]: ok(team('editor')) });
    let read = 0;
    inScope(
      scope(null, {
        refresh: () => {
          read += 1;
          return Promise.resolve();
        },
      }),
      <TeamInvitationsPage api={api} />,
    );
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Accept the invitation to Web team',
      }),
    );
    await waitFor(() => expect(window.location.search).toBe(`?team=${teamId}`));
    expect(window.location.pathname).toBe('/sites');
    expect(read).toBe(1);
  });

  it('says when an invitation has lapsed', async () => {
    const { api } = fakeServer({ ...mine, [accept]: refuse(404, 'not_found') });
    inScope(scope(null), <TeamInvitationsPage api={api} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Accept the invitation to Web team',
      }),
    );
    expect(await screen.findByText(/no longer open/)).toBeTruthy();
  });

  it('declines and drops it from the list', async () => {
    const { api, called } = fakeServer({ ...mine, [decline]: noContent });
    inScope(scope(null), <TeamInvitationsPage api={api} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Decline the invitation to Web team',
      }),
    );
    expect(await screen.findByText('No open invitations')).toBeTruthy();
    expect(called(decline)).toBe(true);
  });

  it('offers to try again when they cannot load', async () => {
    const { api } = fakeServer({
      'GET /api/v0/team-invitations': refuse(500, 'internal'),
    });
    inScope(scope(null), <TeamInvitationsPage api={api} />);
    expect(
      await screen.findByRole('button', { name: 'Try again' }),
    ).toBeTruthy();
  });
});

describe('moving a site', () => {
  const transfer = 'POST /api/v0/sites/docs/transfer';
  const twoTeams = (role: TeamRole): Scope =>
    scope(role, {
      teams: {
        kind: 'ready',
        teams: [team(role), team('admin', { id: otherTeam, name: 'Ops' })],
      },
    });

  it('moves a personal site to a team the owner administers', async () => {
    const moved = teamSite();
    const { api, bodyOf } = fakeServer({ [transfer]: ok(moved) });
    let after: Site | undefined;
    let where = '';
    inScope(
      scope('admin'),
      <MoveSiteCard
        api={api}
        site={ownSite()}
        onMoved={(site, to) => {
          after = site;
          where = to;
        }}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Move site' }));
    const dialog = screen.getByRole('dialog', { name: 'Move docs?' });
    expect(within(dialog).getByText(/group and tags are cleared/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move site' }));
    await waitFor(() => expect(after).toEqual(moved));
    expect(where).toBe('Web team');
    expect(bodyOf(transfer)).toEqual({ to: { kind: 'team', id: teamId } });
  });

  it('moves a team site back to the admin’s own account', async () => {
    const { api, bodyOf } = fakeServer({ [transfer]: ok(ownSite()) });
    inScope(
      scope('admin'),
      <MoveSiteCard api={api} site={teamSite()} onMoved={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Move site' }));
    const dialog = screen.getByRole('dialog', { name: 'Move docs?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move site' }));
    await waitFor(() =>
      expect(bodyOf(transfer)).toEqual({ to: { kind: 'owner' } }),
    );
  });

  it.each(['editor', 'viewer'] as const)(
    'is left out for a %s of the site’s team',
    (role) => {
      const { api } = fakeServer({});
      inScope(
        twoTeams(role),
        <MoveSiteCard api={api} site={teamSite()} onMoved={() => {}} />,
      );
      expect(
        screen.queryByRole('heading', { name: 'Move this site' }),
      ).toBeNull();
    },
  );

  it('is left out for an owner with no team to move it to', () => {
    const { api } = fakeServer({});
    inScope(
      scope(null),
      <MoveSiteCard api={api} site={ownSite()} onMoved={() => {}} />,
    );
    expect(
      screen.queryByRole('heading', { name: 'Move this site' }),
    ).toBeNull();
  });

  it('says a taken-down site cannot be moved', () => {
    const { api } = fakeServer({});
    inScope(
      scope('admin'),
      <MoveSiteCard
        api={api}
        site={teamSite({ unavailable: true })}
        onMoved={() => {}}
      />,
    );
    expect(screen.getByText(/cannot be moved/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Move site' })).toBeNull();
  });

  it.each([
    ['team_suspended', /suspended this team/],
    ['site_unavailable', /cannot be moved while it is taken down/],
  ])('explains %s', async (code, words) => {
    const { api } = fakeServer({ [transfer]: refuse(409, code) });
    inScope(
      scope('admin'),
      <MoveSiteCard api={api} site={ownSite()} onMoved={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Move site' }));
    const dialog = screen.getByRole('dialog', { name: 'Move docs?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move site' }));
    expect(await within(dialog).findByText(words)).toBeTruthy();
  });
});

describe('a team’s site, by role', () => {
  const by = { kind: 'owner', id: otherOwner, name: 'pat@example.test' };
  const link = {
    id: id('50'),
    publicId: 'k7m2q6xw',
    label: 'For the client',
    path: '/',
    createdAt: now,
    revoked: false,
    uses: 0,
    createdBy: by,
  };
  const policy = {
    id: id('60'),
    path: '/',
    visibility: 'public',
    methods: [],
    emailAllowlist: [],
    updatedAt: now,
    createdBy: by,
  };
  const policyAt = { ...policy, id: id('61'), path: '/docs/' };
  const deploy = {
    id: id('30'),
    status: 'inactive',
    createdAt: now,
    fileCount: 1,
    totalSize: 10,
    createdBy: { kind: 'token', id: id('20'), name: 'CI deploys' },
  };
  const routes = {
    'GET /api/v0/sites/docs/links': list(link),
    'GET /api/v0/sites/docs/policies': list(policy, policyAt),
    'GET /api/v0/sites/docs/domains': list({
      id: id('65'),
      hostname: 'docs.example.test',
      status: 'active',
      createdAt: now,
      routingRecord: {
        type: 'CNAME',
        name: 'docs.example.test',
        value: 'x.test',
      },
      verificationRecord: {
        type: 'TXT',
        name: '_wardlock.docs.example.test',
        value: 'x',
      },
    }),
  };

  describe.each([
    ['viewer', false],
    ['editor', true],
    ['admin', true],
  ] as const)('as %s', (role, edits) => {
    const expectButton = (name: string | RegExp) =>
      expect(screen.queryAllByRole('button', { name }).length > 0).toBe(edits);

    it('share links', async () => {
      const { api } = fakeServer(routes);
      inScope(scope(role), <ShareLinksCard api={api} site={teamSite()} />);
      expect(await screen.findByText('For the client')).toBeTruthy();
      expect(screen.getByText(/Created by pat@example.test/)).toBeTruthy();
      expectButton('Create link');
      expectButton(/^Rotate/);
      expectButton(/^Revoke/);
    });

    it('access policies', async () => {
      const { api } = fakeServer(routes);
      inScope(scope(role), <PoliciesCard api={api} site={teamSite()} />);
      expect(await screen.findByText('/docs/')).toBeTruthy();
      expect(
        screen.getAllByText(/Last changed by pat@example.test/),
      ).toHaveLength(2);
      expectButton('Add path policy');
      expectButton(/^Edit/);
      expectButton(/^Delete/);
    });

    it('custom domains', async () => {
      const { api } = fakeServer(routes);
      inScope(
        scope(role),
        <DomainsCard api={api} site={teamSite()} onSiteChange={() => {}} />,
      );
      expect(
        (await screen.findAllByText('docs.example.test')).length,
      ).toBeGreaterThan(0);
      expectButton(/^Check/);
      expectButton(/^Remove/);
      expect(screen.queryAllByLabelText(/^Hostname/).length > 0).toBe(edits);
      expectButton('Save primary hostname');
    });

    it('deploys', () => {
      const { api } = fakeServer({});
      inScope(
        scope(role),
        <DeploysTab
          api={api}
          site={teamSite({ activeDeployId: id('31') })}
          deploys={[deploy] as never}
          nextCursor={undefined}
          more="idle"
          onOlder={() => {}}
          refreshFailed={false}
          onRefresh={() => {}}
          onRolledBack={() => {}}
        />,
      );
      expect(screen.getByText(/Deployed by the token CI deploys/)).toBeTruthy();
      expectButton(/^Roll back/);
    });

    it('settings', () => {
      const { api } = fakeServer({});
      inScope(
        scope(role),
        <SettingsTab
          api={api}
          site={teamSite()}
          organise={organise}
          onMoved={() => {}}
        />,
      );
      expect(screen.getByText(/Everyone in the team sees/)).toBeTruthy();
      expectButton('Tear down site');
      expect(
        screen.queryAllByLabelText(/^Group/, { selector: 'select' }).length > 0,
      ).toBe(edits);
    });

    it('opening a protected site, which takes a share link', () => {
      const { api } = fakeServer({});
      inScope(
        scope(role),
        <OpenSiteButton
          api={api}
          site={teamSite()}
          policy={{ ...policy, visibility: 'protected' } as never}
          onLinkCreated={() => {}}
        >
          Open site
        </OpenSiteButton>,
      );
      expectButton('Open site');
    });
  });

  it('still lets a viewer open a public site', () => {
    const { api } = fakeServer({});
    inScope(
      scope('viewer'),
      <OpenSiteButton
        api={api}
        site={teamSite()}
        policy={policy as never}
        onLinkCreated={() => {}}
      >
        Open site
      </OpenSiteButton>,
    );
    expect(screen.getByRole('button', { name: 'Open site' })).toBeTruthy();
  });

  it('shows nothing that changes a site while the role is unknown', async () => {
    const { api } = fakeServer(routes);
    inScope(
      scope('admin', { teams: { kind: 'loading' } }),
      <ShareLinksCard api={api} site={teamSite()} />,
    );
    expect(await screen.findByText('For the client')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Create link' })).toBeNull();
  });

  it('says nothing about who made things on a personal site', async () => {
    const { api } = fakeServer(routes);
    inScope(scope(null), <ShareLinksCard api={api} site={ownSite()} />);
    expect(await screen.findByText('For the client')).toBeTruthy();
    expect(screen.queryByText(/Created by/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Create link' })).toBeTruthy();
  });
});

describe('the site page', () => {
  const site = (over: Partial<Site> = {}) => ({
    'GET /api/v0/sites/docs': ok(teamSite(over)),
    'GET /api/v0/sites/docs/policies': list({
      id: id('60'),
      path: '/',
      visibility: 'public',
      methods: [],
      emailAllowlist: [],
      updatedAt: now,
    }),
    'GET /api/v0/sites/docs/deploys': list(),
    'GET /api/v0/sites/docs/links': list(),
    'GET /api/v0/sites/docs/domains': list(),
    'GET /api/v0/groups': list(),
    'GET /api/v0/tags': list(),
  });

  it('puts the site’s team in the address and reads the team’s groups', async () => {
    window.history.replaceState(null, '', '/sites/docs/settings');
    const { api, calls } = fakeServer(site());
    inScope(
      scope('viewer', { team: undefined }),
      <SitePage api={api} name="docs" tab="settings" />,
    );
    expect(await screen.findByRole('link', { name: 'Web team' })).toBeTruthy();
    await waitFor(() => expect(window.location.search).toBe(`?team=${teamId}`));
    await waitFor(() =>
      expect(
        calls
          .find((c) => c.route === 'GET /api/v0/groups')
          ?.url.searchParams.get('team'),
      ).toBe(teamId),
    );
  });

  it('takes a team out of the address for a personal site', async () => {
    window.history.replaceState(null, '', `/sites/docs?team=${teamId}`);
    const { api } = fakeServer({
      ...site(),
      'GET /api/v0/sites/docs': ok(ownSite()),
    });
    inScope(scope('admin'), <SitePage api={api} name="docs" tab="overview" />);
    await waitFor(() => expect(window.location.search).toBe(''));
  });

  it('shows a suspended team’s site as taken down, keeping a pin for a viewer', async () => {
    window.history.replaceState(null, '', `/sites/docs?team=${teamId}`);
    const { api } = fakeServer(site({ unavailable: true }));
    inScope(scope('viewer'), <SitePage api={api} name="docs" tab="overview" />);
    expect((await screen.findAllByText('Taken down')).length).toBeGreaterThan(
      0,
    );
    expect(screen.getByText(/has taken this site down/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Pin site' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Open site/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /Tag$/ })).toBeNull();
  });
});

describe('the whole app on a site’s page', () => {
  const me = {
    owner: { id: ownerId, email: 'me@example.test', createdAt: now },
    credential: 'session',
    scopes: ['deploy', 'manage'],
  };
  const routes = (site: Site) => ({
    'GET /api/v0/me': ok(me),
    'GET /api/v0/teams': list(team('admin')),
    'GET /api/v0/team-invitations': list(),
    'GET /api/v0/sites/docs': ok(site),
    'GET /api/v0/sites/docs/policies': list(),
    'GET /api/v0/sites/docs/deploys': list(),
    'GET /api/v0/groups': list(),
    'GET /api/v0/tags': list(),
  });
  const reads = (calls: { route: string }[]) =>
    calls.filter((c) => c.route === 'GET /api/v0/sites/docs').length;

  it('corrects the address without reading the site again', async () => {
    window.history.replaceState(null, '', '/sites/docs/settings');
    const { api, calls } = fakeServer(routes(teamSite()));
    render(<App api={api} />);
    await waitFor(() => expect(window.location.search).toBe(`?team=${teamId}`));
    expect(
      await screen.findByRole('button', { name: 'Switch team: Web team' }),
    ).toBeTruthy();
    expect(reads(calls)).toBe(1);
  });

  it('says a site moved, and reads it once, as the address follows it', async () => {
    window.history.replaceState(null, '', '/sites/docs/settings');
    const { api, calls } = fakeServer({
      ...routes(ownSite()),
      'POST /api/v0/sites/docs/transfer': ok(teamSite()),
    });
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Move site' }));
    const dialog = screen.getByRole('dialog', { name: 'Move docs?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Move site' }));
    expect(await screen.findByText('Moved docs to Web team.')).toBeTruthy();
    await waitFor(() => expect(window.location.search).toBe(`?team=${teamId}`));
    expect(screen.getByText('Moved docs to Web team.')).toBeTruthy();
    expect(reads(calls)).toBe(1);
  });
});

describe('groups and tags', () => {
  const routes = {
    'GET /api/v0/groups': list({
      id: id('80'),
      name: 'Client',
      createdAt: now,
    }),
    'GET /api/v0/tags': list({
      id: id('81'),
      name: 'docs',
      colour: 1,
      createdAt: now,
    }),
    'GET /api/v0/site-counts': ok({
      all: 0,
      views: {
        pinned: 0,
        expiring: 0,
        public: 0,
        quiet: 0,
        untagged: 0,
        down: 0,
      },
      groups: [],
      noGroup: 0,
      tags: [],
    }),
  };

  it('reads the team’s and offers an editor every change', async () => {
    const { api, calls } = fakeServer({
      ...routes,
      'POST /api/v0/groups': ok(
        { id: id('82'), name: 'New', createdAt: now },
        201,
      ),
    });
    inScope(scope('editor'), <OrganisePage api={api} />);
    expect(
      await screen.findByRole('button', { name: 'New group' }),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'New tag' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Edit Client' })).toBeTruthy();
    for (const path of ['groups', 'tags', 'site-counts'])
      expect(
        calls
          .find((c) => c.route === `GET /api/v0/${path}`)
          ?.url.searchParams.get('team'),
      ).toBe(teamId);
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));
    fireEvent.change(screen.getByLabelText(/New group name/), {
      target: { value: 'New' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create group' }));
    await waitFor(() =>
      expect(
        calls
          .find((c) => c.route === 'POST /api/v0/groups')
          ?.url.searchParams.get('team'),
      ).toBe(teamId),
    );
  });

  it('shows a viewer the lists and nothing to change', async () => {
    const { api } = fakeServer(routes);
    inScope(scope('viewer'), <OrganisePage api={api} />);
    expect(await screen.findByText('Client')).toBeTruthy();
    expect(screen.getByText(/Everyone in the team sees them/)).toBeTruthy();
    for (const name of [
      'New group',
      'New tag',
      'Edit Client',
      'Delete Client',
      'Rename docs',
      'Delete docs',
    ])
      expect(screen.queryByRole('button', { name })).toBeNull();
  });
});

describe('the screens that show one account', () => {
  const summary = (name: string) => ({
    id: id('40'),
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: now,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [0, 0, 0, 0, 0, 0, 0],
    quiet: true,
  });
  const counts = {
    all: 1,
    views: {
      pinned: 0,
      expiring: 0,
      public: 1,
      quiet: 0,
      untagged: 1,
      down: 0,
    },
    groups: [],
    noGroup: 1,
    tags: [],
  };
  const routes = {
    'GET /api/v0/health': ok({ status: 'ok', version: '0.0.0-test' }),
    'GET /api/v0/site-counts': ok(counts),
    'GET /api/v0/groups': list(),
    'GET /api/v0/tags': list(),
    'GET /api/v0/expiring-links': list(),
    'GET /api/v0/site-summaries': list(summary('docs')),
    'GET /api/v0/analytics': ok({
      from: now - 3600_000,
      to: now,
      total: { allowed: 0, refused: 0 },
      hours: [],
      sites: [],
      groups: [],
      tags: [],
      links: [],
      paths: [],
      referrers: [],
      via: [],
      browsers: [],
      countries: [],
    }),
  };
  const scopedOnly = (calls: { route: string; url: URL }[]) => {
    const scoped = calls.filter(({ route }) =>
      /site-counts|groups|tags|expiring-links|site-summaries|analytics/.test(
        route,
      ),
    );
    expect(scoped.length).toBeGreaterThan(0);
    for (const { url } of scoped)
      expect(url.searchParams.get('team')).toBe(teamId);
  };

  it('reads the overview of the chosen team, in its words', async () => {
    const { api, calls } = fakeServer(routes);
    inScope(scope('viewer'), <OverviewPage api={api} />);
    expect(await screen.findByText(/This team has 1 site/)).toBeTruthy();
    scopedOnly(calls);
  });

  it('reads the sites of the chosen team', async () => {
    window.history.replaceState(null, '', `/sites?team=${teamId}`);
    const { api, calls } = fakeServer(routes);
    inScope(scope('editor'), <SitesPage api={api} />);
    expect(await screen.findByRole('link', { name: 'docs' })).toBeTruthy();
    await screen.findByRole('checkbox', { name: 'Select docs' });
    scopedOnly(calls);
    expect(
      screen.getByRole('link', { name: 'docs' }).getAttribute('href'),
    ).toBe(`/sites/docs?team=${teamId}`);
  });

  it('gives a viewer no selection and no organising, only a pin', async () => {
    window.history.replaceState(null, '', `/sites?team=${teamId}`);
    const { api } = fakeServer(routes);
    inScope(scope('viewer'), <SitesPage api={api} />);
    expect(await screen.findByRole('link', { name: 'docs' })).toBeTruthy();
    expect(screen.queryByRole('checkbox', { name: 'Select docs' })).toBeNull();
    expect(screen.queryByRole('checkbox', { name: /^Select all/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Actions for docs' }));
    expect(screen.getByRole('menuitem', { name: 'Pin' })).toBeTruthy();
    expect(screen.queryByRole('menuitem', { name: /Edit tags/ })).toBeNull();
    expect(
      screen.queryByRole('menuitem', { name: /Move to group/ }),
    ).toBeNull();
  });

  it('reads the map of the chosen team', async () => {
    window.history.replaceState(null, '', `/sites?view=map&team=${teamId}`);
    const { api, calls } = fakeServer(routes);
    inScope(scope('viewer'), <SitesPage api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: /map/i }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(calls.some((c) => c.route === 'GET /api/v0/site-counts')).toBe(
        true,
      ),
    );
    await waitFor(() =>
      expect(calls.some((c) => c.route === 'GET /api/v0/site-summaries')).toBe(
        true,
      ),
    );
    scopedOnly(calls);
  });

  it('scopes every read of a filtered map', async () => {
    window.history.replaceState(
      null,
      '',
      `/sites?view=map&q=do&team=${teamId}`,
    );
    const { api, calls } = fakeServer(routes);
    inScope(scope('viewer'), <SitesPage api={api} />);
    await waitFor(() =>
      expect(
        calls.filter((c) => c.route === 'GET /api/v0/site-summaries'),
      ).toHaveLength(2),
    );
    scopedOnly(calls);
  });

  it('keeps a viewer’s grouping and sort on a phone, without Select', async () => {
    const wide = window.matchMedia;
    window.matchMedia = ((query: string) => ({
      matches: false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })) as unknown as typeof window.matchMedia;
    try {
      window.history.replaceState(null, '', `/sites?team=${teamId}`);
      const { api } = fakeServer(routes);
      inScope(scope('viewer'), <SitesPage api={api} />);
      expect(await screen.findByRole('link', { name: 'docs' })).toBeTruthy();
      expect(document.querySelector('.sites-applied')?.textContent).toContain(
        '·',
      );
      expect(screen.queryByRole('button', { name: 'Select' })).toBeNull();
    } finally {
      window.matchMedia = wide;
    }
  });

  it('reads the analytics of the chosen team', async () => {
    const { api, calls } = fakeServer(routes);
    inScope(
      scope('viewer'),
      <AnalyticsPage api={api} query={new URLSearchParams()} />,
    );
    await waitFor(() =>
      expect(calls.some((c) => c.route === 'GET /api/v0/analytics')).toBe(true),
    );
    await waitFor(() =>
      expect(calls.some((c) => c.route === 'GET /api/v0/site-summaries')).toBe(
        true,
      ),
    );
    scopedOnly(calls);
  });

  it('leaves the personal account’s reads as they were', async () => {
    const { api, calls } = fakeServer(routes);
    inScope(scope(null), <OverviewPage api={api} />);
    expect(await screen.findByText(/You have 1 site/)).toBeTruthy();
    expect(calls.some((c) => c.url.searchParams.has('team'))).toBe(false);
  });

  it('lists only personal sites for a webhook', async () => {
    const { api } = fakeServer({
      'GET /api/v0/sites': list(
        { name: 'mine', owner: { kind: 'owner' } },
        { name: 'theirs', owner: { kind: 'team' } },
      ),
    });
    expect(await loadAllSiteNames(api)).toEqual(['mine']);
  });
});

describe('refusals', () => {
  it('uses the words for each team rule, and the server’s otherwise', () => {
    const answer = (
      status: number,
      code: string,
      message = 'From server.',
    ) => ({
      error: { code, message },
      response: new Response(null, { status }),
    });
    expect(teamRefusal(answer(403, 'forbidden_role'))).toMatch(
      /no longer allows/,
    );
    expect(teamRefusal(answer(409, 'something_else'))).toBe('From server.');
    expect(teamRefusal(answer(404, 'not_found'))).toMatch(/no longer here/);
    expect(teamRefusal(answer(500, 'internal'))).toMatch(/went wrong/);
    expect(teamRefusal(null)).toMatch(/Could not reach/);
  });
});
