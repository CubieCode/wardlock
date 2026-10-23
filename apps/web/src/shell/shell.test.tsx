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
import { JumpTo } from './jump-to.js';
import { SitesNavigator, type NavigatorState } from './navigator.js';
import { readTheme, themeStorageKey } from './theme.js';

type SiteSummary = components['schemas']['SiteSummary'];
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

const groupId = '01928c5e-7a1b-7c3d-9e4f-00000000a001';
const emptyGroupId = '01928c5e-7a1b-7c3d-9e4f-00000000a002';
const tagId = '01928c5e-7a1b-7c3d-9e4f-00000000b001';

function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${name.padEnd(12, '0').slice(0, 12)}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: 1767225600000,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [],
    quiet: true,
    ...extra,
  };
}

const counts: SiteCounts = {
  all: 12,
  views: {
    pinned: 0,
    expiring: 2,
    public: 5,
    quiet: 0,
    untagged: 7,
    down: 0,
  },
  groups: [
    { groupId, sites: 9 },
    { groupId: emptyGroupId, sites: 0 },
  ],
  noGroup: 3,
  tags: [{ tagId, sites: 4 }],
};
const groups = {
  items: [
    { id: groupId, name: 'Client work', createdAt: 1767225600000 },
    { id: emptyGroupId, name: 'Archive', createdAt: 1767225600000 },
  ],
};
const tags = {
  items: [{ id: tagId, name: 'docs', colour: 3, createdAt: 1767225600000 }],
};

type Handler = (request: Request) => Response | Promise<Response>;

/** A fake server: `routes["GET /api/v0/me"]` answers that call. */
function fakeServer(routes: Record<string, Handler> = {}) {
  const calls: URL[] = [];
  const all: Record<string, Handler> = {
    'GET /api/v0/me': () => Response.json(me),
    'GET /api/v0/health': () =>
      Response.json({ status: 'ok', version: '0.0.0-test' }),
    'GET /api/v0/sites': () => Response.json({ items: [] }),
    'GET /api/v0/site-counts': () => Response.json(counts),
    'GET /api/v0/groups': () => Response.json(groups),
    'GET /api/v0/tags': () => Response.json(tags),
    'GET /api/v0/site-summaries': (request) => {
      const q = new URL(request.url).searchParams.get('q') ?? '';
      const items = [summary('docs', { pinned: true }), summary('drafts')];
      return Response.json({
        items: items.filter((site) => site.name.includes(q)),
      });
    },
    ...routes,
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      calls.push(url);
      const handler = all[`${request.method} ${url.pathname}`];
      if (handler) return handler(request);
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return { api, calls };
}

function at(path: string) {
  window.history.replaceState(null, '', path);
}

/** Pretends the screen is narrower than 48rem, or not. */
function narrow(matches: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: !matches && query.includes('min-width'),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
}

beforeEach(() => {
  at('/');
  window.localStorage.clear();
  delete document.documentElement.dataset.theme;
  // jsdom has no matchMedia: the shell is wide.
  // @ts-expect-error -- removed per test
  delete window.matchMedia;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('rail', () => {
  it('lists only built screens and marks the current one', async () => {
    at('/sites/docs');
    render(<App api={fakeServer().api} />);
    const rail = await screen.findByRole('navigation', { name: 'Main' });
    const links = within(rail).getAllByRole('link');
    expect(links.map((link) => link.textContent)).toEqual([
      'Wardlock',
      'Overview',
      'Sites',
      'Passkeys',
    ]);
    expect(
      within(rail)
        .getByRole('link', { name: 'Sites' })
        .getAttribute('aria-current'),
    ).toBe('page');
    expect(
      within(rail)
        .getByRole('link', { name: 'Overview' })
        .hasAttribute('aria-current'),
    ).toBe(false);
    expect(within(rail).getByRole('button', { name: 'Jump to' })).toBeTruthy();
    expect(within(rail).getByRole('button', { name: 'Account' })).toBeTruthy();
  });

  it('never links to an unbuilt screen', async () => {
    at('/sites');
    render(<App api={fakeServer().api} />);
    await screen.findByRole('complementary', { name: 'Sites navigator' });
    fireEvent.click(screen.getByRole('button', { name: 'Jump to' }));
    await screen.findByRole('option', { name: /docs/ });
    const hrefs = [...document.querySelectorAll('a')].map((a) =>
      a.getAttribute('href'),
    );
    for (const href of hrefs) expect(href).not.toMatch(/analytics|organise/);
    expect(screen.queryByText(/Analytics|Groups and tags/)).toBeNull();
  });

  it('shows no navigator outside the sites list', async () => {
    render(<App api={fakeServer().api} />);
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    expect(screen.queryByRole('complementary')).toBeNull();
  });
});

describe('sites navigator', () => {
  it('shows All sites with its count and nothing the list cannot filter', async () => {
    at('/sites');
    const server = fakeServer();
    render(<App api={server.api} />);
    const nav = await screen.findByRole('complementary', {
      name: 'Sites navigator',
    });
    const all = await within(nav).findByRole('link', { name: 'All sites 12' });
    expect(all.getAttribute('href')).toBe('/sites');
    expect(all.getAttribute('aria-current')).toBe('page');
    expect(within(nav).getAllByRole('link')).toHaveLength(1);
    // Group and tag names are only read once they are shown.
    expect(server.calls.some((url) => url.pathname === '/api/v0/groups')).toBe(
      false,
    );
  });

  it('offers to count again when the counts fail', async () => {
    at('/sites');
    let fail = true;
    render(
      <App
        api={
          fakeServer({
            'GET /api/v0/site-counts': () =>
              fail
                ? Response.json(
                    { code: 'internal', message: 'Oops.' },
                    { status: 500 },
                  )
                : Response.json(counts),
          }).api
        }
      />,
    );
    const retry = await screen.findByRole('button', { name: 'Count again' });
    fail = false;
    fireEvent.click(retry);
    expect(
      await screen.findByRole('link', { name: 'All sites 12' }),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Count again' })).toBeNull();
  });

  const ready: NavigatorState = {
    kind: 'ready',
    counts,
    groups: groups.items,
    tags: tags.items,
  };

  it('with filters, lists views, groups, No group and tags with counts', () => {
    at('/sites');
    render(
      <SitesNavigator
        state={ready}
        onRetry={() => {}}
        onJump={() => {}}
        filters
      />,
    );
    const names = (label: string) =>
      within(screen.getByRole('navigation', { name: label }))
        .getAllByRole('link')
        .map((link) => [link.textContent, link.getAttribute('href')]);
    // Views with no sites are left out, except Pinned.
    expect(names('Sites')).toEqual([
      ['All sites 12', '/sites'],
      ['Pinned 0', '/sites?v=pinned'],
      ['Links expiring soon 2', '/sites?v=expiring'],
      ['Public 5', '/sites?v=public'],
      ['Untagged 7', '/sites?v=untagged'],
    ]);
    expect(names('Groups')).toEqual([
      ['Client work 9', `/sites?g=${groupId}`],
      ['Archive 0', `/sites?g=${emptyGroupId}`],
      ['No group 3', '/sites?g=none'],
    ]);
    expect(names('Tags')).toEqual([['docs 4', `/sites?t=${tagId}`]]);
    // The organise screen is not built: no New group or tag management.
    expect(
      screen.queryByRole('link', { name: /New group|Manage tags/ }),
    ).toBeNull();
  });

  it.each([
    ['/sites', 'All sites 12'],
    [`/sites?g=${groupId}`, 'Client work 9'],
    ['/sites?g=none', 'No group 3'],
    [`/sites?t=${tagId}`, 'docs 4'],
    ['/sites?v=public', 'Public 5'],
  ])('marks %s as current', (path, name) => {
    at(path);
    render(
      <SitesNavigator
        state={ready}
        onRetry={() => {}}
        onJump={() => {}}
        filters
      />,
    );
    const current = document.querySelectorAll('[aria-current="page"]');
    expect([...current].map((link) => link.textContent)).toEqual([name]);
  });

  it('marks nothing current under two filters at once', () => {
    at(`/sites?g=${groupId}&v=public`);
    render(
      <SitesNavigator
        state={ready}
        onRetry={() => {}}
        onJump={() => {}}
        filters
      />,
    );
    expect(document.querySelector('[aria-current]')).toBeNull();
  });
});

describe('jump to', () => {
  it('opens on Ctrl+K and ⌘K, searches sites on the server and goes to the choice', async () => {
    const server = fakeServer();
    render(<App api={server.api} />);
    await screen.findByRole('heading', { level: 1, name: 'Overview' });

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    const input = screen.getByRole('combobox', { name: 'Jump to' });
    expect(document.activeElement).toBe(input);
    const list = screen.getByRole('listbox', { name: 'Jump to' });
    expect(
      await within(list).findByRole('option', { name: /docs/ }),
    ).toBeTruthy();
    expect(within(list).getByRole('group', { name: 'Pages' })).toBeTruthy();

    fireEvent.change(input, { target: { value: 'draf' } });
    await within(list).findByRole('option', { name: /drafts/ });
    const search = server.calls.filter(
      (url) => url.pathname === '/api/v0/site-summaries',
    );
    expect(search.at(-1)?.searchParams.get('q')).toBe('draf');
    expect(within(list).queryByRole('option', { name: /docs/ })).toBeNull();

    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(window.location.pathname).toBe('/sites/drafts'));
    expect(screen.queryByRole('combobox')).toBeNull();

    fireEvent.keyDown(document, { key: 'K', metaKey: true });
    expect(screen.getByRole('combobox', { name: 'Jump to' })).toBeTruthy();
    fireEvent.keyDown(document, { key: 'k', metaKey: true });
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('moves with the arrows, closes on Escape and returns focus to the opener', async () => {
    render(<App api={fakeServer().api} />);
    const opener = await screen.findByRole('button', { name: 'Jump to' });
    opener.focus();
    fireEvent.click(opener);
    const input = screen.getByRole('combobox', { name: 'Jump to' });
    await screen.findByRole('option', { name: /docs/ });
    const selected = () =>
      screen
        .getAllByRole('option')
        .find((option) => option.getAttribute('aria-selected') === 'true')
        ?.textContent;
    expect(selected()).toMatch(/^docs/);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(selected()).toMatch(/^drafts/);
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(selected()).toBe('Passkeys');

    act(() => {
      screen
        .getByRole('dialog', { name: 'Jump to' })
        .dispatchEvent(new Event('cancel', { cancelable: true }));
    });
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it('says when the sites search fails and still offers pages', async () => {
    render(
      <App
        api={
          fakeServer({
            'GET /api/v0/site-summaries': () =>
              Response.json(
                { code: 'internal', message: 'Oops.' },
                { status: 500 },
              ),
          }).api
        }
      />,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Jump to' }));
    expect(await screen.findByText('Could not search your sites')).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Overview' })).toBeTruthy();
  });

  it('with filters, also finds views, groups and tags by name', async () => {
    const onClose = vi.fn();
    render(<JumpTo api={fakeServer().api} onClose={onClose} filters />);
    const input = screen.getByRole('combobox', { name: 'Jump to' });
    fireEvent.change(input, { target: { value: 'doc' } });
    const sitesGroup = await screen.findByRole('group', { name: 'Sites' });
    expect(
      await within(sitesGroup).findByRole('option', { name: /^docs/ }),
    ).toBeTruthy();
    const tagsGroup = await screen.findByRole('group', { name: 'Tags' });
    expect(within(tagsGroup).getByRole('option').textContent).toBe(
      'docs4 sites',
    );
    fireEvent.change(input, { target: { value: 'client' } });
    const groupOption = await screen.findByRole('option', {
      name: /^Client work/,
    });
    expect(groupOption.textContent).toBe('Client work9 sites');
    fireEvent.click(groupOption);
    expect(onClose).toHaveBeenCalledOnce();
    expect(window.location.pathname + window.location.search).toBe(
      `/sites?g=${groupId}`,
    );
  });
});

describe('theme', () => {
  it('defaults to System and keeps a choice across reloads', async () => {
    const { unmount } = render(<App api={fakeServer().api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Account' }));
    const theme = screen.getByRole('group', { name: 'Theme' });
    expect(
      within(theme).getByRole<HTMLInputElement>('radio', { name: 'System' })
        .checked,
    ).toBe(true);
    expect(document.documentElement.dataset.theme).toBeUndefined();

    fireEvent.click(within(theme).getByRole('radio', { name: 'Dark' }));
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(window.localStorage.getItem(themeStorageKey)).toBe('dark');

    unmount();
    delete document.documentElement.dataset.theme;
    render(<App api={fakeServer().api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Account' }));
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: 'Dark' }).checked,
    ).toBe(true);

    fireEvent.click(screen.getByRole('radio', { name: 'System' }));
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(window.localStorage.getItem(themeStorageKey)).toBeNull();
  });

  it('falls back to System when storage cannot be read, and still applies a choice', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    expect(readTheme()).toBe('system');
    render(<App api={fakeServer().api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Account' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Light' }));
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: 'Light' }).checked,
    ).toBe(true);
  });

  it('ignores a stored value it does not know', () => {
    window.localStorage.setItem(themeStorageKey, 'sepia');
    expect(readTheme()).toBe('system');
  });
});

describe('below 48rem', () => {
  it('puts the places in a bottom bar and the navigator in a drawer', async () => {
    narrow(true);
    at('/sites');
    render(<App api={fakeServer().api} />);
    const bar = await screen.findByRole('navigation', { name: 'Main' });
    expect(
      within(bar)
        .getAllByRole('link')
        .map((l) => l.textContent),
    ).toEqual(['Overview', 'Sites', 'Passkeys']);
    expect(within(bar).getByRole('button', { name: 'Jump to' })).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Account' })).toBeTruthy();
    expect(screen.queryByRole('complementary')).toBeNull();

    const browse = screen.getByRole('button', { name: 'Browse sites' });
    browse.focus();
    fireEvent.click(browse);
    const drawer = screen.getByRole('dialog', { name: 'Browse sites' });
    expect(
      await within(drawer).findByRole('link', { name: 'All sites 12' }),
    ).toBeTruthy();
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(browse);

    // Jump to from the drawer closes it and opens the palette.
    fireEvent.click(browse);
    fireEvent.click(screen.getByRole('button', { name: /Jump to…/ }));
    expect(screen.queryByRole('dialog', { name: 'Browse sites' })).toBeNull();
    expect(screen.getByRole('combobox', { name: 'Jump to' })).toBeTruthy();
  });
});
