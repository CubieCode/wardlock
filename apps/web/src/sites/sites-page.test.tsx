// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient, type components } from '@wardlock/apiclient-ts';
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app.js';
import type { Group, Tag } from './site-page-organise.js';
import type { OrganiseRequest } from './sites-page-organise.js';
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

const now = Date.now();
const id = (n: number) =>
  `01928c5e-7a1b-7c3d-9e4f-${String(n).padStart(12, '0')}`;
const work = id(9001);
const docs = id(9002);
const client = id(9101);
const preview = id(9102);
const legal = id(9103);
const archive = id(9104);

function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${name.padEnd(12, '0').slice(0, 12)}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: now - 48 * 3600_000,
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

/** Three groups' worth of synthetic sites. */
function world(): World {
  const sites = [
    summary('portal', {
      groupId: work,
      tagIds: [archive, client, legal, preview],
      visibility: 'protected',
      methods: ['links', 'password'],
      workingLinks: 3,
      expiringLinks: 1,
      requests: [10, 20, 30, 40, 50, 60, 1000],
      quiet: false,
      pinned: true,
      liveDeploy: {
        id: id(5001),
        shortId: '00005001',
        message: 'Add March invoices',
        activatedAt: now - 3600_000,
      },
    }),
    ...Array.from({ length: 6 }, (_, i) =>
      summary(`client-${i + 1}`, { groupId: work, tagIds: [client] }),
    ),
    summary('handbook', { groupId: docs }),
    summary('scratch'),
    summary('gone', { unavailable: true }),
  ];
  return {
    sites,
    groups: [
      { id: work, name: 'Client work', note: 'For clients', createdAt: 0 },
      { id: docs, name: 'Docs', createdAt: 0 },
    ],
    tags: [
      { id: archive, name: 'archive', colour: 4, createdAt: 0 },
      { id: client, name: 'client', colour: 1, createdAt: 0 },
      { id: legal, name: 'legal', colour: 2, createdAt: 0 },
      { id: preview, name: 'preview', colour: 3, createdAt: 0 },
    ],
  };
}

function counts(w: World): SiteCounts {
  const of = (test: (s: SiteSummary) => boolean) => w.sites.filter(test).length;
  return {
    all: w.sites.length,
    views: {
      pinned: of((s) => s.pinned),
      expiring: of((s) => s.expiringLinks > 0),
      public: of((s) => !s.unavailable && s.visibility === 'public'),
      quiet: of((s) => !s.unavailable && s.quiet),
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

type Handler = (
  request: Request,
  body: unknown,
) => Response | Promise<Response>;

/**
 * A fake server over `w`: summaries filter by group, tag, view and plain
 * search words, page by `limit`, and organise changes `w` and answers the
 * prior state.
 */
function fakeServer(w: World, routes: Record<string, Handler> = {}) {
  const calls: URL[] = [];
  const bodies: { route: string; body: unknown }[] = [];
  const all: Record<string, Handler> = {
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
      let items = w.sites.filter(
        (s) =>
          (!group || (group === 'none' ? !s.groupId : s.groupId === group)) &&
          (!tag || s.tagIds.includes(tag)) &&
          (p.get('view') !== 'pinned' || s.pinned) &&
          words.every((word) =>
            word.startsWith('is:')
              ? word === 'is:pinned'
                ? s.pinned
                : word === 'is:protected'
                  ? s.visibility === 'protected'
                  : false
              : s.name.includes(word),
          ),
      );
      if (p.get('sort') === 'name')
        items = items.toSorted((a, b) => (a.name < b.name ? -1 : 1));
      const limit = Number(p.get('limit') ?? 50);
      const start = Number(p.get('cursor') ?? 0);
      const page = items.slice(start, start + limit);
      return Response.json({
        items: page,
        ...(start + limit < items.length
          ? { nextCursor: String(start + limit) }
          : {}),
      });
    },
    'POST /api/v0/organise': (_request, body) => {
      const change = body as OrganiseRequest;
      const prior = change.sites.map((name) => {
        const s = w.sites.find((x) => x.name === name)!;
        return {
          site: name,
          tagIds: s.tagIds,
          pinned: s.pinned,
          ...(s.groupId ? { groupId: s.groupId } : {}),
        };
      });
      w.sites = w.sites.map((s) => {
        if (!change.sites.includes(s.name)) return s;
        const next = { ...s };
        next.tagIds = [
          ...new Set([...s.tagIds, ...(change.addTagIds ?? [])]),
        ].filter((t) => !(change.removeTagIds ?? []).includes(t));
        if (change.groupId) next.groupId = change.groupId;
        if (change.ungroup) delete next.groupId;
        if (change.pinned !== undefined) next.pinned = change.pinned;
        return next;
      });
      return Response.json({ prior });
    },
    'POST /api/v0/tags': (_request, body) => {
      const tag = {
        id: id(9200 + w.tags.length),
        name: (body as { name: string }).name,
        colour: 5,
        createdAt: 0,
      };
      w.tags = [...w.tags, tag].toSorted((a, b) => (a.name < b.name ? -1 : 1));
      return Response.json(tag, { status: 201 });
    },
    'POST /api/v0/groups': (_request, body) => {
      const group = {
        id: id(9300 + w.groups.length),
        name: (body as { name: string }).name,
        createdAt: 0,
      };
      w.groups = [...w.groups, group];
      return Response.json(group, { status: 201 });
    },
    ...routes,
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      calls.push(url);
      const text = await request.text();
      const body = text ? JSON.parse(text) : undefined;
      if (body !== undefined) bodies.push({ route, body });
      const handler = all[route];
      return handler
        ? handler(request, body)
        : Response.json(
            { code: 'not_found', message: 'Not found.' },
            { status: 404 },
          );
    },
  });
  const organised = () =>
    bodies
      .filter((b) => b.route === 'POST /api/v0/organise')
      .map((b) => b.body);
  const summaries = () =>
    calls.filter((u) => u.pathname === '/api/v0/site-summaries');
  return { api, calls, organised, summaries, handlers: all };
}

const at = (path: string) => window.history.replaceState(null, '', path);
const query = () => new URLSearchParams(window.location.search);

const h2s = () =>
  within(screen.getByRole('main'))
    .getAllByRole('heading', { level: 2 })
    .map((h) => h.textContent);
const section = (name: string) => screen.getByRole('region', { name });
const rows = (name: string) =>
  within(within(section(name)).getByRole('list', { name: `Sites in ${name}` }))
    .getAllByRole('listitem')
    .map((li) => within(li).getAllByRole('link')[0]!.textContent);
const row = (name: string) =>
  screen
    .getAllByRole('listitem')
    .filter((li) => within(li).queryByRole('link', { name }) !== null)
    .at(-1)!;

async function open(w = world(), path = '/sites', routes = {}) {
  at(path);
  const server = fakeServer(w, routes);
  render(<App api={server.api} />);
  await screen.findByRole('list', { name: 'Sections' });
  return server;
}

beforeEach(() => at('/sites'));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('sites list', () => {
  it('shows each site with its access, tags, links, requests and live deploy', async () => {
    await open();
    expect(
      screen.getByRole('heading', { level: 1, name: 'Sites' }),
    ).toBeTruthy();
    expect(screen.getByText('10 sites', { selector: 'p' })).toBeTruthy();
    const portal = row('portal');
    expect(within(portal).getByText('Protected')).toBeTruthy();
    expect(within(portal).getByText('share links · password')).toBeTruthy();
    // Three tags shown and the rest counted.
    expect(within(portal).getByText('archive')).toBeTruthy();
    expect(within(portal).getByText('client')).toBeTruthy();
    expect(within(portal).getByText('legal')).toBeTruthy();
    expect(within(portal).queryByText('preview')).toBeNull();
    expect(within(portal).getByText('+1')).toBeTruthy();
    expect(within(portal).getByText('3 links')).toBeTruthy();
    expect(within(portal).getByText('Soon')).toBeTruthy();
    expect(
      within(portal).getByText(', 1 expiring within 48 hours'),
    ).toBeTruthy();
    expect(portal.textContent).toContain('1.2K requests in 7 days');
    expect(within(portal).getByText('Add March invoices')).toBeTruthy();
    expect(within(portal).getByText('00005001')).toBeTruthy();
    expect(
      within(portal)
        .getByRole('button', { name: 'Pin portal' })
        .getAttribute('aria-pressed'),
    ).toBe('true');
    const scratch = row('scratch');
    expect(within(scratch).getByText('Public')).toBeTruthy();
    expect(within(scratch).getByText('No links')).toBeTruthy();
    expect(within(scratch).getByText('Not deployed yet')).toBeTruthy();
    // A taken-down site keeps its access word, with Taken down beside it.
    expect(within(row('gone')).getByText('Taken down')).toBeTruthy();
    expect(within(row('gone')).getByText('Public')).toBeTruthy();
  });

  it('groups by group in order, then No group, with counts and notes', async () => {
    await open();
    expect(h2s()).toEqual(['Client work', 'Docs', 'No group']);
    const head = section('Client work');
    expect(head.querySelector('.sites-section-meta')!.textContent).toBe(
      '7 sites3 links1,210 requestsFor clients',
    );
    expect(within(head).getByText('For clients')).toBeTruthy();
    expect(
      within(head)
        .getByRole('link', { name: 'Client work' })
        .getAttribute('href'),
    ).toBe(`/sites?g=${work}`);
  });

  it('groups by tag (a site under each of its tags), access or nothing', async () => {
    await open();
    fireEvent.change(screen.getByRole('combobox', { name: 'Group by' }), {
      target: { value: 'tag' },
    });
    expect(query().get('by')).toBe('tag');
    expect(h2s()).toEqual([
      'archive',
      'client',
      'legal',
      'preview',
      'Untagged',
    ]);
    expect(rows('legal')).toEqual(['portal']);
    expect(
      screen.getByText('A site with several tags appears under each of them.'),
    ).toBeTruthy();

    fireEvent.change(screen.getByRole('combobox', { name: 'Group by' }), {
      target: { value: 'access' },
    });
    expect(h2s()).toEqual(['Protected', 'Public', 'Taken down']);
    expect(rows('Taken down')).toEqual(['gone']);

    fireEvent.change(screen.getByRole('combobox', { name: 'Group by' }), {
      target: { value: 'none' },
    });
    expect(h2s()).toEqual(['All sites']);
    // One section shows every row.
    expect(rows('All sites')).toHaveLength(10);
    fireEvent.change(screen.getByRole('combobox', { name: 'Group by' }), {
      target: { value: 'group' },
    });
    expect(query().has('by')).toBe(false);
  });

  it('sorts on the server and keeps its order within sections', async () => {
    const server = await open();
    fireEvent.change(screen.getByRole('combobox', { name: 'Sort' }), {
      target: { value: 'name' },
    });
    expect(query().get('sort')).toBe('name');
    await waitFor(() =>
      expect(rows('Client work').slice(0, 2)).toEqual(['client-1', 'client-2']),
    );
    expect(server.summaries().at(-1)!.searchParams.get('sort')).toBe('name');
  });

  it('switches density and keeps it in the address', async () => {
    await open();
    fireEvent.click(screen.getByRole('radio', { name: 'Compact' }));
    expect(query().get('d')).toBe('compact');
    expect(document.querySelector('.sites-list-compact')).not.toBeNull();
    fireEvent.click(screen.getByRole('radio', { name: 'Comfortable' }));
    expect(query().has('d')).toBe(false);
  });

  it('shows five rows per section, then Show all N in the section', async () => {
    await open();
    expect(rows('Client work')).toHaveLength(5);
    const more = within(section('Client work')).getByRole('button', {
      name: 'Show all 7 in Client work',
    });
    fireEvent.click(more);
    expect(rows('Client work')).toHaveLength(7);
    expect(
      within(section('Client work')).queryByRole('button', {
        name: /Show all/,
      }),
    ).toBeNull();
  });

  it('shows three rows per section below 48rem', async () => {
    window.matchMedia = ((q: string) => ({
      matches: !q.includes('min-width'),
      media: q,
      addEventListener: () => {},
      removeEventListener: () => {},
    })) as unknown as typeof window.matchMedia;
    try {
      await open();
      expect(rows('Client work')).toHaveLength(3);
      expect(
        within(section('Client work')).getByRole('button', {
          name: 'Show all 7 in Client work',
        }),
      ).toBeTruthy();
    } finally {
      // @ts-expect-error jsdom has none; the other tests expect that.
      delete window.matchMedia;
    }
  });

  it('collapses and expands a section', async () => {
    await open();
    const toggle = within(section('Docs')).getByRole('button', {
      name: 'Collapse Docs',
    });
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    fireEvent.click(toggle);
    expect(within(section('Docs')).queryByRole('list')).toBeNull();
    const expand = within(section('Docs')).getByRole('button', {
      name: 'Expand Docs',
    });
    expect(expand.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(expand);
    expect(rows('Docs')).toEqual(['handbook']);
  });
});

describe('filters and search', () => {
  it('filters by the navigator’s group, tag and view on the server', async () => {
    const server = await open(world(), `/sites?g=${work}`);
    expect(
      screen.getByRole('heading', { level: 1, name: 'Client work' }),
    ).toBeTruthy();
    expect(screen.getByText('7 of 10 sites')).toBeTruthy();
    expect(server.summaries().at(-1)!.searchParams.get('group')).toBe(work);
    const filters = screen.getByRole('group', { name: 'Filters' });
    expect(within(filters).getByText('Group: Client work')).toBeTruthy();
    // The section is the filter, so its title is not a link to itself.
    expect(
      within(section('Client work')).queryByRole('link', {
        name: 'Client work',
      }),
    ).toBeNull();
    // One section: every row shows.
    expect(rows('Client work')).toHaveLength(7);

    fireEvent.click(within(filters).getByRole('button', { name: 'Clear all' }));
    expect(window.location.search).toBe('');
    await waitFor(() => expect(rows('Docs')).toEqual(['handbook']));

    act(() => {
      window.history.pushState(null, '', `/sites?t=${legal}`);
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Tagged legal' }),
    ).toBeTruthy();
    expect(server.summaries().at(-1)!.searchParams.get('tag')).toBe(legal);
    act(() => {
      window.history.pushState(null, '', '/sites?v=pinned');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Pinned' }),
    ).toBeTruthy();
    expect(server.summaries().at(-1)!.searchParams.get('view')).toBe('pinned');
  });

  it('sends the search once typing pauses and shows its tokens as chips', async () => {
    const server = await open();
    vi.useFakeTimers();
    const search = screen.getByRole('searchbox', { name: 'Search sites' });
    fireEvent.change(search, { target: { value: 'client' } });
    fireEvent.change(search, { target: { value: 'client is:pinned' } });
    const before = server.summaries().length;
    act(() => vi.advanceTimersByTime(100));
    expect(server.summaries()).toHaveLength(before);
    act(() => vi.advanceTimersByTime(200));
    vi.useRealTimers();
    expect(query().get('q')).toBe('client is:pinned');
    await waitFor(() =>
      expect(server.summaries().at(-1)!.searchParams.get('q')).toBe(
        'client is:pinned',
      ),
    );
    // Only the last words typed were sent.
    expect(server.summaries()).toHaveLength(before + 1);
    const filters = screen.getByRole('group', { name: 'Filters' });
    fireEvent.click(
      within(filters).getByRole('button', { name: 'Remove filter Pinned' }),
    );
    expect(query().get('q')).toBe('client');
    expect((search as HTMLInputElement).value).toBe('client');
    expect(screen.queryByRole('group', { name: 'Filters' })).toBeNull();
  });

  it('drops an answer a later search overtook', async () => {
    let release: (() => void) | undefined;
    const server = fakeServer(world());
    const summaries = server.handlers['GET /api/v0/site-summaries']!;
    server.handlers['GET /api/v0/site-summaries'] = async (request, body) => {
      if (new URL(request.url).searchParams.get('q') === 'client')
        await new Promise<void>((r) => (release = r));
      return summaries(request, body);
    };
    at('/sites?q=client');
    render(<App api={server.api} />);
    await waitFor(() => expect(release).toBeDefined());
    act(() => {
      window.history.pushState(null, '', '/sites?q=handbook');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    await waitFor(() => expect(rows('Docs')).toEqual(['handbook']));
    release!();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole('region', { name: 'Client work' })).toBeNull();
    expect(rows('Docs')).toEqual(['handbook']);
  });

  it('focuses the search on /, but not while typing elsewhere', async () => {
    await open();
    const search = screen.getByRole('searchbox', { name: 'Search sites' });
    fireEvent.keyDown(document.body, { key: '/' });
    expect(document.activeElement).toBe(search);
    search.blur();
    const sort = screen.getByRole('combobox', { name: 'Sort' });
    sort.focus();
    fireEvent.keyDown(sort, { key: '/' });
    expect(document.activeElement).toBe(sort);
  });

  it('says when nothing matches and clears the filters', async () => {
    at('/sites?q=nothing-like-this');
    render(<App api={fakeServer(world()).api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'No sites match' }),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(window.location.search).toBe('');
    expect(await screen.findByRole('region', { name: 'Docs' })).toBeTruthy();
  });

  it('treats a group that is not the owner’s as not found', async () => {
    at(`/sites?g=${id(4040)}`);
    render(<App api={fakeServer(world()).api} />);
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: 'No such group or tag',
      }),
    ).toBeTruthy();
  });
});

describe('states', () => {
  it('explains an empty list without offering unbuilt actions', async () => {
    at('/sites');
    render(<App api={fakeServer({ sites: [], groups: [], tags: [] }).api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'No sites yet' }),
    ).toBeTruthy();
    expect(
      screen.queryByRole('button', { name: /new site|create/i }),
    ).toBeNull();
  });

  it('offers to try again when the list fails to load', async () => {
    let fail = true;
    const server = fakeServer(world());
    const summaries = server.handlers['GET /api/v0/site-summaries']!;
    server.handlers['GET /api/v0/site-summaries'] = (request, body) =>
      fail
        ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
        : summaries(request, body);
    at('/sites');
    render(<App api={server.api} />);
    expect(await screen.findByText(/Could not load your sites/)).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('region', { name: 'Docs' })).toBeTruthy();
  });

  it('reads every page of a long list', async () => {
    const w = world();
    w.sites = Array.from({ length: 130 }, (_, i) =>
      summary(`s-${String(i).padStart(3, '0')}`),
    );
    const server = await open(w, '/sites?by=none');
    expect(rows('All sites')).toHaveLength(130);
    expect(server.summaries().map((u) => u.searchParams.get('cursor'))).toEqual(
      [null, '100'],
    );
    expect(server.summaries()[0]!.searchParams.get('limit')).toBe('100');
  });
});

describe('selection and bulk changes', () => {
  async function selectClientWork() {
    const all = within(section('Client work')).getByRole('checkbox', {
      name: 'Select all in Client work',
    });
    fireEvent.click(all);
    return screen.getByRole('toolbar', { name: 'Bulk actions' });
  }

  it('selects rows one by one or a section at a time, with a tri-state Select all', async () => {
    await open();
    const all = within(section('Client work')).getByRole('checkbox', {
      name: 'Select all in Client work',
    }) as HTMLInputElement;
    fireEvent.click(
      within(row('portal')).getByRole('checkbox', { name: 'Select portal' }),
    );
    expect(all.indeterminate).toBe(true);
    expect(
      screen.getByRole('toolbar', { name: 'Bulk actions' }).textContent,
    ).toContain('1 selected');
    fireEvent.click(all);
    expect(all.indeterminate).toBe(false);
    expect(all.checked).toBe(true);
    // Rows hidden behind Show all are selected too.
    expect(screen.getByRole('toolbar').textContent).toContain('7 selected');
    fireEvent.click(all);
    expect(screen.queryByRole('toolbar')).toBeNull();
    await selectClientWork();
    fireEvent.click(screen.getByRole('button', { name: 'Clear selection' }));
    expect(screen.queryByRole('toolbar')).toBeNull();
  });

  it('adds a tag to every selected site, then Undo removes it where it was added', async () => {
    const server = await open();
    const bar = await selectClientWork();
    fireEvent.click(within(bar).getByRole('button', { name: 'Tags' }));
    const picker = screen.getByRole('dialog', { name: 'Tags for 7 sites' });
    const legalItem = within(picker).getByRole('menuitemcheckbox', {
      name: /legal/,
    });
    expect(legalItem.getAttribute('aria-checked')).toBe('mixed');
    expect(legalItem.textContent).toContain('1 of 7');
    expect(
      within(picker)
        .getByRole('menuitemcheckbox', { name: /client/ })
        .getAttribute('aria-checked'),
    ).toBe('true');
    fireEvent.click(legalItem);
    expect(await screen.findByText('Added legal to 7 sites')).toBeTruthy();
    expect(server.organised().at(-1)).toEqual({
      sites: [
        'portal',
        'client-1',
        'client-2',
        'client-3',
        'client-4',
        'client-5',
        'client-6',
      ],
      addTagIds: [legal],
    });
    await waitFor(() =>
      expect(legalItem.getAttribute('aria-checked')).toBe('true'),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    expect(await screen.findByText('Undone.')).toBeTruthy();
    // Portal had the tag before, so it keeps it.
    expect(server.organised().at(-1)).toEqual({
      removeTagIds: [legal],
      sites: [
        'client-1',
        'client-2',
        'client-3',
        'client-4',
        'client-5',
        'client-6',
      ],
    });
    await waitFor(() =>
      expect(within(row('client-1')).queryByText('legal')).toBeNull(),
    );
  });

  it('takes a tag every selected site has off them all, and undoes it', async () => {
    const server = await open();
    const bar = await selectClientWork();
    fireEvent.click(within(bar).getByRole('button', { name: 'Tags' }));
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /client/ }));
    expect(await screen.findByText('Removed client from 7 sites')).toBeTruthy();
    expect(server.organised().at(-1)).toMatchObject({ removeTagIds: [client] });
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    await screen.findByText('Undone.');
    expect(server.organised().at(-1)).toMatchObject({ addTagIds: [client] });
  });

  it('creates a tag from the picker and gives it to the selection', async () => {
    const server = await open();
    const bar = await selectClientWork();
    fireEvent.click(within(bar).getByRole('button', { name: 'Tags' }));
    const filter = screen.getByRole('searchbox', {
      name: 'Find or create a tag',
    });
    fireEvent.change(filter, { target: { value: 'Not Valid' } });
    expect(screen.getByText(/Tag names use lowercase letters/)).toBeTruthy();
    fireEvent.change(filter, { target: { value: 'launch' } });
    fireEvent.click(
      screen.getByRole('menuitem', { name: 'Create tag “launch”' }),
    );
    expect(await screen.findByText('Added launch to 7 sites')).toBeTruthy();
    const created = (server.organised().at(-1) as OrganiseRequest).addTagIds;
    expect(created).toHaveLength(1);
    await waitFor(() =>
      expect(within(row('client-1')).getByText('launch')).toBeTruthy(),
    );
  });

  it('moves the selection to a group, and Undo puts each back where it was', async () => {
    const server = await open();
    fireEvent.click(
      within(row('handbook')).getByRole('checkbox', {
        name: 'Select handbook',
      }),
    );
    fireEvent.click(
      within(row('scratch')).getByRole('checkbox', { name: 'Select scratch' }),
    );
    const bar = screen.getByRole('toolbar', { name: 'Bulk actions' });
    fireEvent.click(within(bar).getByRole('button', { name: 'Move to group' }));
    const picker = screen.getByRole('dialog', {
      name: 'Move 2 sites to a group',
    });
    expect(
      within(picker)
        .getByRole('menuitemcheckbox', { name: /Docs/ })
        .getAttribute('aria-checked'),
    ).toBe('mixed');
    fireEvent.click(
      within(picker).getByRole('menuitemcheckbox', { name: /Client work/ }),
    );
    expect(
      await screen.findByText('Moved 2 sites to Client work'),
    ).toBeTruthy();
    expect(server.organised().at(-1)).toEqual({
      sites: ['handbook', 'scratch'],
      groupId: work,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    await screen.findByText('Undone.');
    expect(server.organised().slice(-2)).toEqual([
      { groupId: docs, sites: ['handbook'] },
      { ungroup: true, sites: ['scratch'] },
    ]);
    await waitFor(() => expect(rows('Docs')).toEqual(['handbook']));
  });

  it('creates a group from the picker and moves the selection into it', async () => {
    const server = await open();
    fireEvent.click(
      within(row('scratch')).getByRole('checkbox', { name: 'Select scratch' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Move to group' }));
    const filter = screen.getByRole('searchbox', {
      name: 'Find or create a group',
    });
    fireEvent.change(filter, { target: { value: 'docs' } });
    // An existing name in another case is not offered again.
    expect(screen.queryByRole('menuitem', { name: /Create group/ })).toBeNull();
    fireEvent.change(filter, { target: { value: 'Kōwhai Studio' } });
    fireEvent.click(
      screen.getByRole('menuitem', { name: 'Create group “Kōwhai Studio”' }),
    );
    expect(
      await screen.findByText('Moved scratch to Kōwhai Studio'),
    ).toBeTruthy();
    expect(server.organised().at(-1)).toMatchObject({ sites: ['scratch'] });
    expect(
      await screen.findByRole('region', { name: 'Kōwhai Studio' }),
    ).toBeTruthy();
  });

  it('pins the selection, and Undo unpins only what it pinned', async () => {
    const server = await open();
    const bar = await selectClientWork();
    fireEvent.click(within(bar).getByRole('button', { name: 'Pin' }));
    const pinned = screen.getByRole('menuitemcheckbox', { name: /Pinned/ });
    expect(pinned.getAttribute('aria-checked')).toBe('mixed');
    fireEvent.click(pinned);
    expect(await screen.findByText('Pinned 7 sites')).toBeTruthy();
    expect(server.organised().at(-1)).toMatchObject({ pinned: true });
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    await screen.findByText('Undone.');
    expect(server.organised().at(-1)).toEqual({
      pinned: false,
      sites: [
        'client-1',
        'client-2',
        'client-3',
        'client-4',
        'client-5',
        'client-6',
      ],
    });
  });

  it('pins one site from its star, with Undo', async () => {
    const server = await open();
    const star = within(row('handbook')).getByRole('button', {
      name: 'Pin handbook',
    });
    fireEvent.click(star);
    await waitFor(() => expect(star.getAttribute('aria-pressed')).toBe('true'));
    expect(screen.getByText('Pinned handbook')).toBeTruthy();
    expect(server.organised().at(-1)).toEqual({
      sites: ['handbook'],
      pinned: true,
    });
  });

  it('says what went wrong when a change is refused, and changes nothing', async () => {
    await open(world(), '/sites', {
      'POST /api/v0/organise': () =>
        Response.json(
          {
            code: 'limit_exceeded',
            message: 'A site can have at most 20 tags.',
          },
          { status: 413 },
        ),
    });
    fireEvent.click(
      within(row('handbook')).getByRole('button', { name: 'Pin handbook' }),
    );
    expect(
      await screen.findByText(
        'Could not pin the sites: A site can have at most 20 tags.',
      ),
    ).toBeTruthy();
    expect(
      within(row('handbook'))
        .getByRole('button', { name: 'Pin handbook' })
        .getAttribute('aria-pressed'),
    ).toBe('false');
  });
});

describe('row menu', () => {
  it('opens the site, its access log, and copies its address', async () => {
    await open();
    const writeText = vi.fn(() => Promise.resolve());
    Object.assign(navigator, { clipboard: { writeText } });
    const menu = () =>
      fireEvent.click(
        within(row('handbook')).getByRole('button', {
          name: 'Actions for handbook',
        }),
      );
    menu();
    expect(
      screen.getAllByRole('menuitem').map((item) => item.textContent),
    ).toEqual([
      'Open',
      'Pin',
      'Edit tags…',
      'Move to group…',
      'Copy address',
      'Access log',
    ]);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Copy address' }));
    expect(writeText).toHaveBeenCalledWith(
      'https://handbook.sites.example.test/',
    );
    expect(
      await screen.findByText('Copied https://handbook.sites.example.test/'),
    ).toBeTruthy();
    menu();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Access log' }));
    expect(window.location.pathname).toBe('/sites/handbook/access-log');
  });

  it('edits one site’s tags and group in place', async () => {
    const server = await open();
    fireEvent.click(
      within(row('handbook')).getByRole('button', {
        name: 'Actions for handbook',
      }),
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Edit tags…' }));
    const tags = screen.getByRole('dialog', { name: 'Tags for handbook' });
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(tags).getByRole('searchbox', { name: 'Find or create a tag' }),
      ),
    );
    fireEvent.click(
      within(tags).getByRole('menuitemcheckbox', { name: /preview/ }),
    );
    expect(await screen.findByText('Added preview to handbook')).toBeTruthy();
    fireEvent.keyDown(tags, { key: 'Escape' });

    fireEvent.click(
      within(row('handbook')).getByRole('button', {
        name: 'Actions for handbook',
      }),
    );
    fireEvent.click(screen.getByRole('menuitem', { name: 'Move to group…' }));
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: /No group/ }));
    expect(
      await screen.findByText('Took handbook out of its group'),
    ).toBeTruthy();
    expect(server.organised().at(-1)).toEqual({
      sites: ['handbook'],
      ungroup: true,
    });
  });
});
