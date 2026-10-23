// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { App } from '../app.js';
import type { ShareLink } from '../links/format.js';
import type { Policy, Site } from './format.js';
import type { Group, Tag } from './site-page-organise.js';

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
const id = (tail: string) =>
  `01928c5e-7a1b-7c3d-9e4f-${tail.padStart(12, '0')}`;

const groups: Group[] = [
  { id: id('70'), name: 'Client work', createdAt: now },
  { id: id('71'), name: 'Internal', createdAt: now },
];
const tags: Tag[] = [
  { id: id('80'), name: 'client', colour: 1, createdAt: now },
  { id: id('81'), name: 'preview', colour: 2, createdAt: now },
  { id: id('82'), name: 'staging', colour: 3, createdAt: now },
];

const policy: Policy = {
  id: id('10'),
  path: '/',
  visibility: 'protected',
  methods: ['links'],
  allowlist: { emails: [], domains: [] },
  version: 1,
  createdAt: now,
  updatedAt: now,
};

type Handler = (request: Request, body: unknown) => Response;

/**
 * A server holding one site, `docs`, with the owner's groups and tags. Pin
 * and organisation changes update it as the real one does; `routes`
 * overrides any answer.
 */
function fakeServer(
  start: Partial<Site> = {},
  routes: Record<string, Handler> = {},
) {
  let site: Site = {
    id: id('01'),
    name: 'docs',
    hostname: 'docs.sites.example.test',
    url: 'https://docs.sites.example.test/',
    createdAt: now,
    unavailable: false,
    tagIds: [],
    pinned: false,
    ...start,
  };
  let owned = [...tags];
  const calls: { route: string; body: unknown }[] = [];
  const base = '/api/v0/sites/docs';
  const answers: Record<string, Handler> = {
    'GET /api/v0/me': () => Response.json(me),
    [`GET ${base}`]: () => Response.json(site),
    [`GET ${base}/policies`]: () => Response.json({ items: [policy] }),
    [`GET ${base}/deploys`]: () => Response.json({ items: [] }),
    [`GET ${base}/links`]: () => Response.json({ items: [] }),
    [`GET ${base}/domains`]: () => Response.json({ items: [] }),
    'GET /api/v0/groups': () => Response.json({ items: groups }),
    'GET /api/v0/tags': () => Response.json({ items: owned }),
    [`PUT ${base}/pin`]: () =>
      Response.json((site = { ...site, pinned: true })),
    [`DELETE ${base}/pin`]: () =>
      Response.json((site = { ...site, pinned: false })),
    [`PUT ${base}/organisation`]: (_, body) => {
      const { groupId, tagIds } = body as {
        groupId?: string;
        tagIds: string[];
      };
      site = { ...site, tagIds };
      if (groupId) site.groupId = groupId;
      else delete site.groupId;
      return Response.json(site);
    },
    'POST /api/v0/tags': (_, body) => {
      const tag: Tag = {
        id: id('89'),
        name: (body as { name: string }).name,
        colour: 4,
        createdAt: now,
      };
      owned = [...owned, tag];
      return Response.json(tag, { status: 201 });
    },
    ...routes,
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route, body });
      const answer = answers[route];
      return answer
        ? answer(request, body)
        : Response.json(
            { code: 'not_found', message: 'Not found.' },
            { status: 404 },
          );
    },
  });
  return {
    api,
    calls,
    writes: () => calls.filter((c) => !c.route.startsWith('GET')),
  };
}

const at = (path: string) => window.history.replaceState(null, '', path);
const chips = () => screen.getByRole('group', { name: 'Group and tags' });
const tab = (name: string) =>
  within(screen.getByRole('navigation', { name: 'Site sections' })).getByRole(
    'link',
    { name },
  );

beforeEach(() => at('/sites/docs'));
afterEach(cleanup);

describe('site header', () => {
  it('shows the group and tags, each tag with its colour beside its name', async () => {
    const { api } = fakeServer({
      groupId: groups[0]!.id,
      tagIds: [tags[1]!.id],
    });
    render(<App api={api} />);
    await screen.findByRole('group', { name: 'Group and tags' });
    expect(chips().textContent).toContain('Group: Client work');
    const tag = within(chips()).getByText('preview').closest('.wl-tag-chip')!;
    expect(tag.querySelector('.wl-tag-dot')?.getAttribute('style')).toContain(
      'var(--color-tag-2)',
    );
    expect(within(chips()).queryByText('client')).toBeNull();
  });

  it('says when a site has no group', async () => {
    const { api } = fakeServer();
    render(<App api={api} />);
    expect(
      within(
        await screen.findByRole('group', { name: 'Group and tags' }),
      ).getByText('No group'),
    ).toBeTruthy();
  });

  it('pins and unpins the site', async () => {
    const { api, writes } = fakeServer();
    render(<App api={api} />);
    const pin = await screen.findByRole('button', { name: 'Pin site' });
    expect(pin.getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(pin);
    await waitFor(() => expect(pin.getAttribute('aria-pressed')).toBe('true'));
    fireEvent.click(pin);
    await waitFor(() => expect(pin.getAttribute('aria-pressed')).toBe('false'));
    expect(writes().map((w) => w.route)).toEqual([
      'PUT /api/v0/sites/docs/pin',
      'DELETE /api/v0/sites/docs/pin',
    ]);
  });

  it('removes a tag and keeps the group', async () => {
    const { api, writes } = fakeServer({
      groupId: groups[0]!.id,
      tagIds: [tags[0]!.id, tags[1]!.id],
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Remove client' }),
    );
    await waitFor(() =>
      expect(within(chips()).queryByText('client')).toBeNull(),
    );
    expect(writes()).toEqual([
      {
        route: 'PUT /api/v0/sites/docs/organisation',
        body: { groupId: groups[0]!.id, tagIds: [tags[1]!.id] },
      },
    ]);
  });

  it('adds an existing tag from + Tag', async () => {
    const { api, writes } = fakeServer({ tagIds: [tags[1]!.id] });
    render(<App api={api} />);
    fireEvent.click(
      within(
        await screen.findByRole('group', { name: 'Group and tags' }),
      ).getByRole('button', { name: 'Tag' }),
    );
    const panel = screen.getByRole('dialog', { name: 'Tags' });
    const items = within(panel).getAllByRole('menuitemcheckbox');
    expect(
      items.map((i) => [
        i.querySelector('.wl-menu-label')?.textContent?.trim(),
        i.getAttribute('aria-checked'),
      ]),
    ).toEqual([
      ['client', 'false'],
      ['preview', 'true'],
      ['staging', 'false'],
    ]);
    fireEvent.click(
      within(panel).getByRole('menuitemcheckbox', { name: 'staging' }),
    );
    await waitFor(() =>
      expect(
        within(chips()).getByRole('button', { name: 'Remove staging' }),
      ).toBeTruthy(),
    );
    // Sent without a group, since the site has none.
    expect(writes()).toEqual([
      {
        route: 'PUT /api/v0/sites/docs/organisation',
        body: { tagIds: [tags[1]!.id, tags[2]!.id] },
      },
    ]);
  });

  it('creates a new tag from the filter and gives it to the site', async () => {
    const { api, writes } = fakeServer();
    render(<App api={api} />);
    fireEvent.click(
      within(
        await screen.findByRole('group', { name: 'Group and tags' }),
      ).getByRole('button', { name: 'Tag' }),
    );
    const filter = screen.getByRole('searchbox', {
      name: 'Find or create a tag',
    });
    // A name breaking the rules is explained, not offered.
    fireEvent.change(filter, { target: { value: 'Not valid!' } });
    expect(screen.queryByRole('menuitem')).toBeNull();
    expect(
      screen.getByText(
        'Tag names use lowercase letters, digits and hyphens, up to 30 characters.',
      ),
    ).toBeTruthy();
    // An existing name is offered as itself, not created again.
    fireEvent.change(filter, { target: { value: 'preview' } });
    expect(screen.queryByRole('menuitem')).toBeNull();
    fireEvent.change(filter, { target: { value: 'launch' } });
    fireEvent.click(
      screen.getByRole('menuitem', { name: 'Create tag “launch”' }),
    );
    await waitFor(() =>
      expect(
        within(chips()).getByRole('button', { name: 'Remove launch' }),
      ).toBeTruthy(),
    );
    expect(writes()).toEqual([
      { route: 'POST /api/v0/tags', body: { name: 'launch' } },
      {
        route: 'PUT /api/v0/sites/docs/organisation',
        body: { tagIds: [id('89')] },
      },
    ]);
  });

  it('explains a change the server refuses', async () => {
    const { api } = fakeServer(
      { tagIds: [] },
      {
        'PUT /api/v0/sites/docs/organisation': () =>
          Response.json(
            {
              code: 'limit_exceeded',
              message: 'A site can have at most 20 tags.',
              target: 'tagIds',
            },
            { status: 413 },
          ),
      },
    );
    render(<App api={api} />);
    fireEvent.click(
      within(
        await screen.findByRole('group', { name: 'Group and tags' }),
      ).getByRole('button', { name: 'Tag' }),
    );
    fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'client' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Could not change the tags: A site can have at most 20 tags.',
    );
    expect(
      within(chips()).queryByRole('button', { name: 'Remove client' }),
    ).toBeNull();
  });

  it('keeps the page when groups and tags cannot load, and reloads them', async () => {
    let fail = true;
    const { api } = fakeServer(
      { tagIds: [tags[0]!.id] },
      {
        'GET /api/v0/tags': () =>
          fail
            ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
            : Response.json({ items: tags }),
      },
    );
    render(<App api={api} />);
    expect(
      await screen.findByText('Could not load groups and tags.'),
    ).toBeTruthy();
    expect(
      screen.getByRole('heading', { level: 1, name: 'docs' }),
    ).toBeTruthy();
    fail = false;
    fireEvent.click(
      screen.getByRole('button', { name: 'Reload groups and tags' }),
    );
    expect(
      within(
        await screen.findByRole('group', { name: 'Group and tags' }),
      ).getByText('client'),
    ).toBeTruthy();
  });

  it('says when the site has never been deployed', async () => {
    const { api } = fakeServer();
    render(<App api={api} />);
    expect(
      (await screen.findAllByText(/^Not deployed yet/)).length,
    ).toBeGreaterThan(0);
  });
});

describe('7-day requests in the header', () => {
  /** An analytics answer with these totals and nothing broken down. */
  const analytics = (allowed: number, refused: number) => () =>
    Response.json({
      days: 7,
      from: now - 7 * 24 * 3600_000,
      to: now,
      total: { allowed, refused },
      hours: [],
      via: [],
      sites: [],
      groups: [],
      tags: [],
      links: [],
      paths: [],
      otherPaths: { allowed: 0, refused: 0 },
      browsers: [],
    });
  // The header's line under the address, beside Open site.
  const meta = () => document.querySelector('.site-header-meta')!.textContent;

  it('counts allowed and refused requests for this site', async () => {
    let query = '';
    const { api } = fakeServer(
      { activatedAt: now - 3 * 3600_000 },
      {
        'GET /api/v0/analytics': (request) => {
          query = new URL(request.url).search;
          return analytics(1200, 4)();
        },
      },
    );
    render(<App api={api} />);
    expect(
      await screen.findByText('1,204 requests in 7 days (4 refused)'),
    ).toBeTruthy();
    expect(meta()).toBe(
      '1,204 requests in 7 days (4 refused) · Live since 3 hours ago',
    );
    expect(new URLSearchParams(query).get('site')).toBe('docs');
    expect(new URLSearchParams(query).get('days')).toBe('7');
    // Requests, never visits.
    expect(meta()).not.toMatch(/visit/i);
  });

  it('says so when nobody has requested the site', async () => {
    const { api } = fakeServer(
      {},
      { 'GET /api/v0/analytics': analytics(0, 0) },
    );
    render(<App api={api} />);
    expect(await screen.findByText('0 requests in 7 days')).toBeTruthy();
  });

  it('names one request in the singular', async () => {
    const { api } = fakeServer(
      {},
      { 'GET /api/v0/analytics': analytics(1, 0) },
    );
    render(<App api={api} />);
    expect(await screen.findByText('1 request in 7 days')).toBeTruthy();
  });

  it('shows nothing in its place while the count loads', async () => {
    const { api } = fakeServer(
      {},
      {
        'GET /api/v0/analytics': () =>
          new Promise<Response>(() => {}) as unknown as Response,
      },
    );
    render(<App api={api} />);
    await screen.findByRole('heading', { level: 1, name: 'docs' });
    expect(meta()).toBe('Not deployed yet');
    expect(screen.queryByText(/requests? in 7 days/)).toBeNull();
  });

  it('leaves the count out quietly when it cannot be read', async () => {
    let reads = 0;
    const { api } = fakeServer(
      {},
      {
        'GET /api/v0/analytics': () => {
          reads += 1;
          return Response.json(
            { code: 'internal', message: 'x' },
            { status: 500 },
          );
        },
      },
    );
    render(<App api={api} />);
    await screen.findByRole('heading', { level: 1, name: 'docs' });
    await waitFor(() => expect(reads).toBe(1));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(meta()).toBe('Not deployed yet');
    expect(screen.queryByRole('alert')).toBeNull();
    // The rest of the page still works.
    expect(
      screen.getByRole('region', { name: 'Who can open it' }),
    ).toBeTruthy();
  });
});

describe('site tabs', () => {
  it('reads the site once while the owner moves between tabs', async () => {
    const { api, calls } = fakeServer();
    render(<App api={api} />);
    const tabs = await screen.findByRole('navigation', {
      name: 'Site sections',
    });
    for (const [name, path, title] of [
      ['Links', '/sites/docs/links', 'Links · docs'],
      ['Access', '/sites/docs/access', 'Access · docs'],
      ['Domains', '/sites/docs/domains', 'Domains · docs'],
      ['Deploys', '/sites/docs/deploys', 'Deploys · docs'],
      ['Access log', '/sites/docs/access-log', 'Access log · docs'],
      ['Settings', '/sites/docs/settings', 'Settings · docs'],
      ['Overview', '/sites/docs', 'docs'],
    ] as const) {
      fireEvent.click(within(tabs).getByRole('link', { name }));
      expect(window.location.pathname).toBe(path);
      expect(
        within(tabs).getByRole('link', { name }).getAttribute('aria-current'),
      ).toBe('page');
      await waitFor(() => expect(document.title).toBe(`${title} · Wardlock`));
      // One heading for the page: the site's name.
      expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    }
    expect(
      calls.filter((c) => c.route === 'GET /api/v0/sites/docs'),
    ).toHaveLength(1);
  });

  it('opens a deep link to a tab', async () => {
    at('/sites/docs/settings');
    const { api } = fakeServer();
    render(<App api={api} />);
    expect(
      await screen.findByRole('region', { name: 'Tear down this site' }),
    ).toBeTruthy();
  });

  it('never shows a share link’s URL again after its dialog closes', async () => {
    at('/sites/docs/links');
    const token = `wl_q4v7m2xkpa_${'S'.repeat(43)}`;
    const created: ShareLink = {
      id: id('20'),
      publicId: 'q4v7m2xkpa',
      label: 'Review',
      path: '/',
      uses: 0,
      status: 'active',
      createdAt: now,
    };
    let links: ShareLink[] = [];
    const { api } = fakeServer(
      {},
      {
        'GET /api/v0/sites/docs/links': () => Response.json({ items: links }),
        'POST /api/v0/sites/docs/links': () => {
          links = [created];
          return Response.json(
            {
              shareLink: created,
              token,
              url: `https://docs.sites.example.test/?t=${token}`,
            },
            { status: 201 },
          );
        },
      },
    );
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Create link' }));
    const create = screen.getByRole('dialog', { name: /Create a share link/ });
    fireEvent.click(
      within(create).getByRole('button', { name: 'Create link' }),
    );
    await waitFor(() => expect(document.body.innerHTML).toContain(token));
    // Leave without closing the dialog, then come back.
    fireEvent.click(tab('Overview'));
    await screen.findByRole('region', { name: 'Who can open it' });
    fireEvent.click(tab('Links'));
    await screen.findByRole('list', { name: 'Share links' });
    expect(document.body.innerHTML).not.toContain(token);
    expect(document.body.innerHTML).not.toContain('SSSS');
  });
});

describe('site settings', () => {
  beforeEach(() => at('/sites/docs/settings'));

  it('moves the site to a group and out of it', async () => {
    const { api, writes } = fakeServer({ tagIds: [tags[0]!.id] });
    render(<App api={api} />);
    const group = (await screen.findByRole('combobox', {
      name: 'Group',
    })) as HTMLSelectElement;
    expect(group.value).toBe('');
    expect([...group.options].map((o) => o.textContent)).toEqual([
      'No group',
      'Client work',
      'Internal',
    ]);
    fireEvent.change(group, { target: { value: groups[1]!.id } });
    await waitFor(() =>
      expect(chips().textContent).toContain('Group: Internal'),
    );
    fireEvent.change(group, { target: { value: '' } });
    await waitFor(() => expect(chips().textContent).toContain('No group'));
    expect(writes().map((w) => w.body)).toEqual([
      { groupId: groups[1]!.id, tagIds: [tags[0]!.id] },
      { tagIds: [tags[0]!.id] },
    ]);
  });

  it('lists the site’s tags with + Tag', async () => {
    const { api } = fakeServer({ tagIds: [tags[2]!.id] });
    render(<App api={api} />);
    const settings = await screen.findByRole('region', {
      name: 'Group and tags',
    });
    const tagsGroup = await within(settings).findByRole('group', {
      name: 'Tags',
    });
    expect(within(tagsGroup).getByText('staging')).toBeTruthy();
    expect(within(tagsGroup).getByRole('button', { name: 'Tag' })).toBeTruthy();
  });
});
