// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient, type components } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../app.js';
import type { Group, Tag } from './site-page-organise.js';
import type { SiteSummary } from './sites-page-sections.js';

type SiteCounts = components['schemas']['SiteCounts'];

const me = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

const id = (n: number) =>
  `01928c5e-7a1b-7c3d-9e4f-${String(n).padStart(12, '0')}`;
const docs = id(9001);
const work = id(9002);
const client = id(9101);

let serial = 0;
function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  serial += 1;
  return {
    id: id(serial),
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: 1767225600000 + serial,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [0, 0, 0, 0, 0, 0, 0],
    quiet: true,
    ...extra,
  };
}

interface World {
  sites: SiteSummary[];
  groups: Group[];
  tags: Tag[];
}

function world(): World {
  return {
    sites: [
      summary('portal', {
        groupId: work,
        visibility: 'protected',
        methods: ['links'],
        pinned: true,
        tagIds: [client],
        requests: [100, 200, 0, 0, 0, 0, 900],
        quiet: false,
      }),
      summary('pitch', { groupId: work, tagIds: [client] }),
      summary('handbook', { groupId: docs, requests: [5, 0, 0, 0, 0, 0, 0] }),
      summary('scratch'),
      summary('gone', { unavailable: true, visibility: 'protected' }),
    ],
    groups: [
      { id: work, name: 'Client work', createdAt: 0 },
      { id: docs, name: 'Docs', createdAt: 0 },
    ],
    tags: [{ id: client, name: 'client', colour: 1, createdAt: 0 }],
  };
}

function counts(w: World): SiteCounts {
  const of = (test: (s: SiteSummary) => boolean) => w.sites.filter(test).length;
  return {
    all: w.sites.length,
    views: {
      pinned: of((s) => s.pinned),
      expiring: 0,
      public: of((s) => !s.unavailable && s.visibility === 'public'),
      quiet: of((s) => s.quiet),
      untagged: of((s) => s.tagIds.length === 0),
      down: of((s) => s.unavailable),
    },
    groups: w.groups.map((g) => ({
      groupId: g.id,
      sites: of((s) => s.groupId === g.id),
    })),
    noGroup: of((s) => !s.groupId),
    tags: w.tags.map((t) => ({
      tagId: t.id,
      sites: of((s) => s.tagIds.includes(t.id)),
    })),
  };
}

/** A fake server: summaries filter by group, tag and name words, two a page. */
function fakeServer(w: World) {
  const calls: URL[] = [];
  const routes: Record<string, (request: Request) => Response> = {
    'GET /api/v0/me': () => Response.json(me),
    'GET /api/v0/groups': () => Response.json({ items: w.groups }),
    'GET /api/v0/tags': () => Response.json({ items: w.tags }),
    'GET /api/v0/site-counts': () => Response.json(counts(w)),
    'GET /api/v0/site-summaries': (request) => {
      const p = new URL(request.url).searchParams;
      const group = p.get('group');
      const tag = p.get('tag');
      if (group && group !== 'none' && !w.groups.some((g) => g.id === group))
        return Response.json(
          { code: 'not_found', message: 'Not found.' },
          { status: 404 },
        );
      const words = (p.get('q') ?? '').split(' ').filter(Boolean);
      const items = w.sites.filter(
        (s) =>
          (!group || (group === 'none' ? !s.groupId : s.groupId === group)) &&
          (!tag || s.tagIds.includes(tag)) &&
          words.every((word) => s.name.includes(word)),
      );
      const start = Number(p.get('cursor') ?? 0);
      const page = items.slice(start, start + 2);
      return Response.json({
        items: page,
        ...(start + 2 < items.length ? { nextCursor: String(start + 2) } : {}),
      });
    },
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      calls.push(url);
      const handler = routes[`${request.method} ${url.pathname}`];
      return handler
        ? handler(request)
        : Response.json(
            { code: 'not_found', message: 'Not found.' },
            { status: 404 },
          );
    },
  });
  return { api, calls };
}

async function open(path = '/sites?view=map', w = world()) {
  window.history.replaceState(null, '', path);
  const server = fakeServer(w);
  render(<App api={server.api} />);
  await screen.findByRole('group', { name: /^Map of / });
  return server;
}

const map = () => screen.getByRole('group', { name: /^Map of / });

afterEach(() => cleanup());

describe('sites map', () => {
  it('draws every site around the server, by group, with accessible names', async () => {
    const { calls } = await open();
    expect(
      screen.getByRole('heading', { level: 1, name: 'Sites map' }),
    ).toBeTruthy();
    expect(map().getAttribute('aria-label')).toBe(
      'Map of 5 sites in 3 groups around wardlockd',
    );
    const links = within(map())
      .getAllByRole('link')
      .map((a) => a.getAttribute('aria-label'));
    expect(links).toEqual([
      'Client work: 2 sites',
      'portal: Protected, 1,200 requests in 7 days, pinned',
      'pitch: Public, 0 requests in 7 days',
      'Docs: 1 site',
      'handbook: Public, 5 requests in 7 days',
      'No group: 2 sites',
      'scratch: Public, 0 requests in 7 days',
      'gone: Protected, taken down, 0 requests in 7 days',
    ]);
    const portal = within(map()).getByRole('link', { name: /^portal:/ });
    expect(portal.getAttribute('href')).toBe('/sites/portal');
    expect(portal.getAttribute('data-access')).toBe('protected');
    expect(
      within(map())
        .getByRole('link', { name: /^gone:/ })
        .getAttribute('data-access'),
    ).toBe('down');
    // Only the pinned site is labelled.
    expect(portal.textContent).toBe('portal');
    expect(
      within(map()).getByRole('link', { name: /^pitch:/ }).textContent,
    ).toBe('');
    // The busiest site has the largest dot.
    const r = (name: RegExp) =>
      Number(within(map()).getByRole('link', { name }).getAttribute('data-r'));
    expect(r(/^portal:/)).toBeGreaterThan(r(/^handbook:/));
    expect(r(/^handbook:/)).toBeGreaterThan(r(/^pitch:/));
    // A hub filters the map to its group.
    expect(
      within(map())
        .getByRole('link', { name: 'Docs: 1 site' })
        .getAttribute('href'),
    ).toBe(`/sites?g=${docs}&view=map`);
    // All pages, and no per-site reads.
    const summaries = calls.filter(
      (u) => u.pathname === '/api/v0/site-summaries',
    );
    expect(summaries.map((u) => u.searchParams.get('cursor'))).toEqual([
      null,
      '2',
      '4',
    ]);
    expect(
      calls.every((u) =>
        [
          '/api/v0/me',
          '/api/v0/groups',
          '/api/v0/tags',
          '/api/v0/site-counts',
          '/api/v0/site-summaries',
        ].includes(u.pathname),
      ),
    ).toBe(true);
  });

  it('has a legend and lists groups and their sites as text', async () => {
    await open();
    const legend = screen.getByRole('list', { name: 'Legend' });
    expect(
      within(legend)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual([
      'Protected',
      'Public',
      'Taken down',
      'Group has protected sites',
      'Size: requests in 7 days (largest 1,200)',
      'Names on pinned sites',
    ]);
    const work = screen.getByRole('list', { name: 'Sites in Client work' });
    expect(
      within(work)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual([
      'portalProtected1,200 requests in 7 days · pinned',
      'pitchPublic0 requests in 7 days',
    ]);
    expect(
      within(screen.getByRole('list', { name: 'Sites in No group' })).getByRole(
        'link',
        { name: 'gone' },
      ).parentElement!.textContent,
    ).toContain('Taken down');
  });

  it('dims the sites the filters leave out instead of hiding them', async () => {
    const { calls } = await open('/sites?q=po&view=map');
    await waitFor(() =>
      expect(screen.getByText('1 of 5 sites match')).toBeTruthy(),
    );
    const portal = within(map()).getByRole('link', { name: /^portal:/ });
    const pitch = within(map()).getByRole('link', { name: /^pitch:/ });
    expect(portal.getAttribute('class')).not.toContain('map-dim');
    expect(pitch.getAttribute('class')).toContain('map-dim');
    expect(pitch.getAttribute('aria-label')).toBe(
      'pitch: Public, 0 requests in 7 days, outside the filters',
    );
    expect(
      within(map())
        .getByRole('link', { name: 'Docs: 1 site, 0 matching' })
        .getAttribute('class'),
    ).toContain('map-dim');
    expect(
      within(map())
        .getByRole('link', { name: 'Client work: 2 sites, 1 matching' })
        .getAttribute('class'),
    ).not.toContain('map-dim');
    // Every site is still drawn.
    expect(within(map()).getAllByRole('link')).toHaveLength(8);
    expect(screen.getByRole('list', { name: 'Legend' }).textContent).toContain(
      'Faded: outside the filters',
    );
    expect(
      within(screen.getByRole('list', { name: 'Sites in Client work' }))
        .getAllByText('Matches')
        .map((tag) => tag.closest('li')!.querySelector('a')!.textContent),
    ).toEqual(['portal']);
    // The server decides what matches.
    expect(
      calls.some(
        (u) =>
          u.pathname === '/api/v0/site-summaries' &&
          u.searchParams.get('q') === 'po',
      ),
    ).toBe(true);
    // Emptying the search brings every site back.
    const search = screen.getByRole('searchbox', { name: 'Search sites' });
    fireEvent.change(search, { target: { value: '' } });
    fireEvent.keyDown(search, { key: 'Enter' });
    await waitFor(() => expect(window.location.search).toBe('?view=map'));
    await waitFor(() =>
      expect(
        within(map())
          .getByRole('link', { name: /^pitch:/ })
          .getAttribute('class'),
      ).not.toContain('map-dim'),
    );
  });

  it('shows a tooltip when a dot is focused, and hides it on Escape', async () => {
    await open();
    const portal = within(map()).getByRole('link', { name: /^portal:/ });
    fireEvent.focus(portal);
    const tip = document.querySelector('.map-tip');
    expect(tip?.textContent).toBe(
      'portalProtected1,200 requests in 7 daysPinned',
    );
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(document.querySelector('.map-tip')).toBeNull();
    fireEvent.focus(within(map()).getByRole('link', { name: 'Docs: 1 site' }));
    expect(document.querySelector('.map-tip')?.textContent).toContain(
      '5 requests in 7 days',
    );
    fireEvent.blur(within(map()).getByRole('link', { name: 'Docs: 1 site' }));
    expect(document.querySelector('.map-tip')).toBeNull();
  });

  it('switches between list and map keeping the filters', async () => {
    window.history.replaceState(null, '', `/sites?g=${work}&sort=name`);
    const server = fakeServer(world());
    render(<App api={server.api} />);
    const views = await screen.findByRole('navigation', {
      name: 'Show sites as',
    });
    expect(
      within(views)
        .getByRole('link', { name: 'List' })
        .getAttribute('aria-current'),
    ).toBe('page');
    fireEvent.click(within(views).getByRole('link', { name: 'Map' }));
    await screen.findByRole('group', { name: /^Map of / });
    expect(window.location.pathname).toBe('/sites');
    expect(window.location.search).toBe(`?g=${work}&sort=name&view=map`);
    expect(
      screen.getByRole('group', { name: 'Filters' }).textContent,
    ).toContain('Group: Client work');
    const back = screen.getByRole('navigation', { name: 'Show sites as' });
    expect(
      within(back)
        .getByRole('link', { name: 'Map' })
        .getAttribute('aria-current'),
    ).toBe('page');
    expect(
      within(back).getByRole('link', { name: 'List' }).getAttribute('href'),
    ).toBe(`/sites?g=${work}&sort=name`);
  });

  it('keeps the navigator’s filters on the map', async () => {
    await open(`/sites?g=${docs}&view=map`);
    const nav = screen.getByRole('complementary', { name: 'Sites navigator' });
    const group = await within(nav).findByRole('link', { name: 'Docs 1' });
    expect(group.getAttribute('href')).toBe(`/sites?g=${docs}&view=map`);
    expect(group.getAttribute('aria-current')).toBe('page');
    expect(
      within(nav)
        .getByRole('link', { name: /^All sites/ })
        .getAttribute('href'),
    ).toBe('/sites?view=map');
  });

  it('says when a group or tag in the address is not the owner’s', async () => {
    window.history.replaceState(null, '', `/sites?g=${id(1)}&view=map`);
    const server = fakeServer(world());
    render(<App api={server.api} />);
    expect(
      await screen.findByRole('heading', { name: 'No such group or tag' }),
    ).toBeTruthy();
  });

  it('links a site named map to its own Overview', async () => {
    const w = world();
    w.sites.push(summary('map'));
    await open('/sites?view=map', w);
    expect(
      within(map()).getByRole('link', { name: /^map:/ }).getAttribute('href'),
    ).toBe('/sites/map');
  });

  it('shows the empty state with no sites', async () => {
    window.history.replaceState(null, '', '/sites?view=map');
    const server = fakeServer({ sites: [], groups: [], tags: [] });
    render(<App api={server.api} />);
    expect(
      await screen.findByRole('heading', { name: 'No sites yet' }),
    ).toBeTruthy();
  });
});
