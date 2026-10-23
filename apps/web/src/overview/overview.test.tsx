// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { OverviewPage } from './overview.js';
import type {
  ExpiringLink,
  Group,
  SiteCounts,
  SiteSummary,
} from './overview-data.js';

const now = Date.now();
const hour = 3600_000;
const clients = '01928c5e-7a1b-7c3d-9e4f-00000000a001';
const archive = '01928c5e-7a1b-7c3d-9e4f-00000000a002';

function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${name.padEnd(12, '0').slice(0, 12)}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: now - 48 * hour,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [0, 0, 0, 0, 0, 0, 0],
    quiet: false,
    ...extra,
  };
}

function live(tail: string, activatedAt: number, message?: string) {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-0000${tail.padStart(8, '0')}`,
    shortId: tail.padStart(8, '0'),
    activatedAt,
    ...(message ? { message } : {}),
  };
}

const groups: Group[] = [
  { id: clients, name: 'Client work', createdAt: now },
  { id: archive, name: 'Archive', createdAt: now },
];

// Newest live deploy first, as listSiteSummaries sorts them.
const sites = [
  summary('docs', {
    groupId: clients,
    pinned: true,
    visibility: 'protected',
    methods: ['links'],
    workingLinks: 3,
    expiringLinks: 2,
    requests: [1, 0, 4, 0, 0, 2, 1200],
    liveDeploy: live('aaaa0001', now - hour, 'Fix typo on the install page'),
  }),
  summary('preview', {
    groupId: clients,
    workingLinks: 1,
    expiringLinks: 1,
    requests: [0, 0, 0, 0, 0, 0, 1],
    tagIds: ['01928c5e-7a1b-7c3d-9e4f-00000000b001'],
    liveDeploy: live('aaaa0002', now - 5 * hour),
  }),
  summary('old-launch', {
    pinned: true,
    unavailable: true,
    quiet: true,
  }),
];

const counts: SiteCounts = {
  all: 3,
  views: { pinned: 2, expiring: 2, public: 1, quiet: 1, untagged: 2, down: 1 },
  groups: [
    { groupId: clients, sites: 2 },
    { groupId: archive, sites: 0 },
  ],
  noGroup: 1,
  tags: [],
};

const expiring: ExpiringLink[] = [
  {
    id: '01928c5e-7a1b-7c3d-9e4f-0000000c0001',
    site: 'docs',
    label: 'Board pack',
    expiresAt: now + 3 * hour + 60_000,
    usesLeft: 2,
  },
  {
    id: '01928c5e-7a1b-7c3d-9e4f-0000000c0002',
    site: 'preview',
    expiresAt: now + 30 * hour,
  },
];

interface Fixture {
  counts: SiteCounts;
  sites: SiteSummary[];
  groups: Group[];
  expiring: ExpiringLink[];
  /** Summaries per page; all of them in one by default. */
  pageSize?: number;
  health?: () => Response;
  fail?: boolean;
}

function fakeServer(fx: Fixture) {
  const calls: string[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      calls.push(url.pathname + url.search);
      if (fx.fail && url.pathname !== '/api/v0/health')
        return Response.json(
          { code: 'internal', message: 'Something went wrong.' },
          { status: 500 },
        );
      switch (url.pathname) {
        case '/api/v0/health':
          return (
            fx.health?.() ?? Response.json({ status: 'ok', version: '0.2.0' })
          );
        case '/api/v0/site-counts':
          return Response.json(fx.counts);
        case '/api/v0/groups':
          return Response.json({ items: fx.groups });
        case '/api/v0/expiring-links':
          return Response.json({ items: fx.expiring });
        case '/api/v0/site-summaries': {
          const size = fx.pageSize ?? fx.sites.length;
          const from = Number(url.searchParams.get('cursor') ?? 0);
          const next = from + size;
          return Response.json({
            items: fx.sites.slice(from, next),
            ...(next < fx.sites.length ? { nextCursor: String(next) } : {}),
          });
        }
      }
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return { api, calls };
}

const full: Fixture = { counts, sites, groups, expiring };

async function show(fx: Fixture = full) {
  const server = fakeServer(fx);
  render(<OverviewPage api={server.api} />);
  await screen.findByRole('region', { name: 'Needs attention' });
  return server;
}

const card = (name: string) => screen.getByRole('region', { name });
const hrefs = (scope: HTMLElement) =>
  within(scope)
    .getAllByRole('link')
    .map((a) => a.getAttribute('href'));

afterEach(cleanup);

describe('overview', () => {
  it('greets the owner with site and group counts and the server version', async () => {
    await show();
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' }));
    expect(
      await screen.findByText(/You have 3 sites in 2 groups\./),
    ).toBeTruthy();
    expect(screen.getByText('0.2.0').tagName).toBe('CODE');
    expect(document.title).toBe('Overview · Wardlock');
  });

  it('leaves out the version when the health read fails', async () => {
    await show({
      ...full,
      groups: [],
      health: () => Response.json({ code: 'x', message: 'x' }, { status: 503 }),
    });
    expect(
      screen.getByText(/Kia ora\. You have 3 sites and no groups\./),
    ).toBeTruthy();
    expect(screen.queryByText(/This server runs/)).toBeNull();
  });

  it('reads every page of summaries, and never one site at a time', async () => {
    const { calls } = await show({ ...full, pageSize: 1 });
    expect(calls.filter((c) => c.startsWith('/api/v0/site-summaries'))).toEqual(
      [
        '/api/v0/site-summaries?sort=deploy&limit=100',
        '/api/v0/site-summaries?sort=deploy&limit=100&cursor=1',
        '/api/v0/site-summaries?sort=deploy&limit=100&cursor=2',
      ],
    );
    expect(calls.some((c) => c.startsWith('/api/v0/sites'))).toBe(false);
    expect(calls).toContain('/api/v0/expiring-links?limit=5');
    // The last page's sites still count.
    expect(within(card('Pinned')).getByText('old-launch')).toBeTruthy();
  });

  it('says what needs attention, each linking to its saved view', async () => {
    await show();
    const attention = card('Needs attention');
    const items = within(attention).getAllByRole('listitem');
    expect(
      items
        .filter((li) => li.classList.contains('attention-item'))
        .map((li) => li.querySelector('strong')?.textContent),
    ).toEqual([
      '3 links expire within 48 hours',
      '1 site had no requests in 30 days',
      '2 sites have no tags',
      '1 site is public',
      '1 site taken down',
    ]);
    // Neutral advice: it fits a site made today and a taken-down site's
    // links alike.
    expect(
      within(attention).getByText(
        'On 2 sites. Check whether anyone still needs them.',
      ),
    ).toBeTruthy();
    expect(
      within(attention).getByText('Check whether you still need them.'),
    ).toBeTruthy();
    for (const [name, href] of [
      ['Review sites with links expiring soon', '/sites?v=expiring'] as const,
      ['Review sites with no requests in 30 days', '/sites?v=quiet'],
      ['Review untagged sites', '/sites?v=untagged'],
      ['Check public sites', '/sites?v=public'],
      ['View taken-down sites', '/sites?v=down'],
    ])
      expect(
        within(attention).getByRole('link', { name }).getAttribute('href'),
      ).toBe(href);
  });

  it('lists each expiring link with uses left and a View link', async () => {
    await show();
    const links = within(card('Needs attention')).getByRole('list', {
      name: 'Links expiring soon',
    });
    const [board, unlabelled] = within(links).getAllByRole('listitem');
    expect(board!.textContent).toContain('Board pack on docs');
    expect(board!.textContent).toContain('Expires in 3 hours · 2 uses left');
    expect(
      within(board!)
        .getByRole('link', { name: 'View link Board pack on docs' })
        .getAttribute('href'),
    ).toBe('/sites/docs/links/01928c5e-7a1b-7c3d-9e4f-0000000c0001');
    expect(unlabelled!.textContent).toContain('Unlabelled link on preview');
    expect(unlabelled!.textContent).not.toContain('uses left');
    expect(
      within(unlabelled!).getByRole('link', { name: /^View link/ }),
    ).toBeTruthy();
  });

  it('leaves out each item with nothing to report', async () => {
    await show({
      ...full,
      counts: {
        ...counts,
        views: { ...counts.views, quiet: 0, down: 0, untagged: 0 },
      },
      sites: sites.map((s) => ({ ...s, expiringLinks: 0 })),
      expiring: [],
    });
    const attention = card('Needs attention');
    expect(within(attention).queryByText(/expire/)).toBeNull();
    expect(within(attention).queryByText(/no requests/)).toBeNull();
    expect(within(attention).queryByText(/no tags/)).toBeNull();
    expect(within(attention).queryByText(/taken down/)).toBeNull();
    expect(within(attention).getByText('1 site is public')).toBeTruthy();
  });

  it('says so plainly when nothing needs attention', async () => {
    await show({
      ...full,
      counts: {
        ...counts,
        views: { ...counts.views, public: 0, quiet: 0, down: 0, untagged: 0 },
      },
      sites: sites.map((s) => ({ ...s, expiringLinks: 0 })),
      expiring: [],
    });
    expect(
      within(card('Needs attention')).getByText('Nothing needs attention.'),
    ).toBeTruthy();
    expect(within(card('Needs attention')).queryByRole('list')).toBeNull();
  });

  it('shows pinned sites with access, 7-day requests and a sparkline', async () => {
    await show();
    const pinned = card('Pinned');
    const [docs, old] = within(pinned).getAllByRole('listitem');
    expect(within(docs!).getByRole('link', { name: 'docs' })).toBeTruthy();
    // The site page's access word: the ward for Protected, coral for Public.
    expect(docs!.textContent).toContain('Protected');
    expect(docs!.querySelector('.wl-status-tag-ward')).toBeTruthy();
    expect(docs!.textContent).toContain('1,207 requests in 7 days');
    expect(docs!.querySelector('svg.wl-sparkline')).toBeTruthy();
    expect(old!.textContent).toContain('Public');
    expect(old!.querySelector('.wl-status-tag-public')).toBeTruthy();
    expect(old!.textContent).toContain('Taken down');
    expect(old!.textContent).toContain('0 requests in 7 days');
    expect(within(pinned).queryByText(/pinned sites$/)).toBeNull();
  });

  it('links to the pinned view when there are more pins than it shows', async () => {
    const many = Array.from({ length: 10 }, (_, i) =>
      summary(`pin-${i}`, { pinned: true }),
    );
    await show({ ...full, sites: many });
    const pinned = card('Pinned');
    expect(within(pinned).getAllByRole('listitem')).toHaveLength(8);
    expect(
      within(pinned)
        .getByRole('link', { name: 'All 10 pinned sites' })
        .getAttribute('href'),
    ).toBe('/sites?v=pinned');
  });

  it('explains Pinned when no site is pinned', async () => {
    await show({
      ...full,
      sites: sites.map((s) => ({ ...s, pinned: false })),
    });
    expect(
      within(card('Pinned')).getByText(
        'Pin a site with the star on its page to keep it here.',
      ),
    ).toBeTruthy();
  });

  it('shows each group with its sites, requests and links', async () => {
    await show();
    const groupsCard = card('Groups');
    expect(
      within(groupsCard)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual([
      'Client work2 sites · 1,208 requests in 7 days · 4 links',
      'Archive0 sites · 0 requests in 7 days · 0 links',
      'No group1 site · 0 requests in 7 days · 0 links',
    ]);
    expect(hrefs(groupsCard)).toEqual([
      `/sites?g=${clients}`,
      `/sites?g=${archive}`,
      '/sites?g=none',
    ]);
  });

  it('explains Groups when there are none', async () => {
    await show({
      ...full,
      groups: [],
      counts: { ...counts, groups: [], noGroup: 3 },
    });
    const groupsCard = card('Groups');
    expect(within(groupsCard).queryByRole('list')).toBeNull();
    expect(
      within(groupsCard).getByText(
        'No groups yet. Put a site in a group from its Settings tab.',
      ),
    ).toBeTruthy();
  });

  it("lists each site's live deploy, newest first", async () => {
    await show();
    const deploys = card('Recent deploys');
    expect(
      within(deploys).getByText('Each site’s live deploy, newest first.'),
    ).toBeTruthy();
    const [docs, preview] = within(deploys).getAllByRole('listitem');
    expect(docs!.textContent).toContain('docsFix typo on the install page');
    expect(docs!.textContent).toContain('aaaa0001 · went live 1 hour ago');
    expect(preview!.textContent).toContain('previewNo message');
    expect(within(deploys).getAllByRole('listitem')).toHaveLength(2);
  });

  it('says when nothing is live', async () => {
    await show({
      ...full,
      sites: sites.map((s) => {
        const copy = { ...s };
        delete copy.liveDeploy;
        return copy;
      }),
    });
    expect(
      within(card('Recent deploys')).getByText('Nothing is live yet.'),
    ).toBeTruthy();
  });

  it('shows the empty account one empty state instead of the cards', async () => {
    const server = fakeServer({
      counts: {
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
      },
      sites: [],
      groups: [],
      expiring: [],
    });
    render(<OverviewPage api={server.api} />);
    expect(
      await screen.findByRole('heading', { name: 'No sites yet' }),
    ).toBeTruthy();
    expect(screen.getByText(/You have no sites yet\./)).toBeTruthy();
    // There is no create-site action in the dashboard: deploy from the CLI.
    const empty = screen.getByText(/to create your first site/);
    expect(empty.textContent).toContain('wardlock deploy');
    expect(empty.textContent).not.toContain('create a site on');
    expect(screen.queryByRole('region')).toBeNull();
    expect(
      screen.getByRole('link', { name: 'Go to Sites' }).getAttribute('href'),
    ).toBe('/sites');
  });

  it('says what went wrong and tries again', async () => {
    const down = () =>
      Response.json({ code: 'x', message: 'x' }, { status: 503 });
    const fx: Fixture = { ...full, fail: true, health: down };
    render(<OverviewPage api={fakeServer(fx).api} />);
    expect(screen.getByRole('status').textContent).toBe('Loading your sites…');
    expect(await screen.findByText(/Could not load your sites\./)).toBeTruthy();
    expect(screen.queryByText(/This server runs/)).toBeNull();
    fx.fail = false;
    delete fx.health;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(
      await screen.findByRole('region', { name: 'Needs attention' }),
    ).toBeTruthy();
    // Try again reads the version again too.
    expect(await screen.findByText(/This server runs Wardlock/)).toBeTruthy();
  });
});
