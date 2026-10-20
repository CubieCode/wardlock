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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app.js';
import type { Policy, Site } from './format.js';

const now = Date.now();

const me = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

const docs: Site = {
  id: '01928c5e-7a1b-7c3d-9e4f-646f63730000',
  name: 'docs',
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now,
};

function policy(visibility: Policy['visibility']): Policy {
  return {
    id: '01928c5e-7a1b-7c3d-9e4f-000000000001',
    path: '/',
    visibility,
    methods: visibility === 'public' ? [] : ['links'],
    allowlist: { emails: [], domains: [] },
    version: 1,
    createdAt: now,
    updatedAt: now,
  };
}

const link = {
  id: '01928c5e-7a1b-7c3d-9e4f-000000000002',
  publicId: 'abcdefghij',
  label: 'Opened from dashboard',
  path: '/',
  expiresAt: now + 10 * 60_000,
  useCount: 0,
  status: 'active',
  createdAt: now,
};
// A synthetic token in the documented shape.
const linkUrl = `https://docs.sites.example.test/?t=wl_abcdefghij_${'A'.repeat(43)}`;

/** The docs site with this `/` policy; `create` answers the link POST. */
function fakeServer(
  visibility: Policy['visibility'],
  create: () => Response = () =>
    Response.json(
      { shareLink: link, token: linkUrl.split('t=')[1], url: linkUrl },
      { status: 201 },
    ),
) {
  const bodies: unknown[] = [];
  let linkLists = 0;
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const route = `${request.method} ${new URL(request.url).pathname}`;
      const base = '/api/v0/sites/docs';
      if (route === 'GET /api/v0/me') return Response.json(me);
      if (route === `GET ${base}`) return Response.json(docs);
      if (route === `GET ${base}/deploys`) return Response.json({ items: [] });
      if (route === `GET ${base}/domains`) return Response.json({ items: [] });
      if (route === `GET ${base}/policies`)
        return Response.json({ items: [policy(visibility)] });
      if (route === `GET ${base}/links`) {
        linkLists += 1;
        return Response.json({ items: bodies.length > 0 ? [link] : [] });
      }
      if (route === `POST ${base}/links`) {
        bodies.push(await request.json());
        return create();
      }
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return { api, bodies, linkLists: () => linkLists };
}

/** A stand-in for the new tab that `window.open` returns. */
function fakeTab() {
  return {
    opener: {} as unknown,
    location: { href: '' },
    close: vi.fn(),
  };
}

beforeEach(() => window.history.replaceState(null, '', '/sites/docs'));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('Open site', () => {
  it('opens a public site at its address', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const { api, bodies } = fakeServer('public');
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Open site' }));
    expect(open).toHaveBeenCalledWith(
      'https://docs.sites.example.test/',
      '_blank',
      'noopener',
    );
    expect(bodies).toEqual([]);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('opens a protected site through a new 10-minute labelled link', async () => {
    const tab = fakeTab();
    const open = vi
      .spyOn(window, 'open')
      .mockReturnValue(tab as unknown as Window);
    const { api, bodies, linkLists } = fakeServer('protected');
    render(<App api={api} />);
    const button = await screen.findByRole('button', { name: 'Open site' });
    await screen.findByText('No share links yet');
    fireEvent.click(button);
    // The tab opens inside the click, before the link exists.
    expect(open).toHaveBeenCalledWith('', '_blank');
    expect(tab.opener).toBeNull();
    await waitFor(() => expect(tab.location.href).toBe(linkUrl));
    expect(bodies).toHaveLength(1);
    const body = bodies[0] as Record<string, unknown>;
    expect(body).toMatchObject({ label: 'Opened from dashboard', path: '/' });
    expect(body).not.toHaveProperty('maxUses');
    const lifetime = (body.expiresAt as number) - Date.now();
    expect(lifetime).toBeGreaterThan(9 * 60_000);
    expect(lifetime).toBeLessThanOrEqual(10 * 60_000);
    expect(tab.close).not.toHaveBeenCalled();
    // The new link shows in the list like any other.
    const list = await screen.findByRole('list', { name: 'Share links' });
    expect(within(list).getByText(/Opened from dashboard/)).toBeTruthy();
    expect(linkLists()).toBe(2);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('closes the tab and explains a failed link', async () => {
    const tab = fakeTab();
    vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
    const { api } = fakeServer('protected', () =>
      Response.json(
        { code: 'internal', message: 'Something went wrong.' },
        { status: 500 },
      ),
    );
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Open site' }));
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Could not open the site',
    );
    expect(tab.close).toHaveBeenCalled();
    expect(tab.location.href).toBe('');
    expect(
      (screen.getByRole('button', { name: 'Open site' }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });

  it('creates no link when the browser blocks the tab', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    const { api, bodies } = fakeServer('protected');
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Open site' }));
    expect((await screen.findByRole('alert')).textContent).toContain(
      'blocked the new tab',
    );
    expect(bodies).toEqual([]);
  });
});
