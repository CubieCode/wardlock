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
import type { Site } from '../sites/format.js';
import type { AccessLogEntry, CreatedShareLink, ShareLink } from './format.js';

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
const hour = 3600_000;
const day = 24 * hour;

const docs: Site = {
  id: '01928c5e-7a1b-7c3d-9e4f-000000000001',
  name: 'docs',
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now - 48 * hour,
  unavailable: false,
  tagIds: [],
  pinned: false,
};

function link(publicId: string, extra: Partial<ShareLink> = {}): ShareLink {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-00${publicId}`,
    publicId,
    path: '/',
    status: 'active',
    uses: 0,
    createdAt: now - 2 * hour,
    ...extra,
  };
}

// Synthetic tokens in the shape the server issues; never real ones.
const secret = 'TESTsecretTESTsecretTESTsecretTESTsecret123';
const token = (publicId: string, tail = secret) => `wl_${publicId}_${tail}`;
const created = (l: ShareLink, tail = secret): CreatedShareLink => ({
  shareLink: l,
  token: token(l.publicId, tail),
  url: `https://docs.sites.example.test${l.path}?t=${token(l.publicId, tail)}`,
});

const notFound = () =>
  Response.json({ code: 'not_found', message: 'Not found.' }, { status: 404 });

type Handler = (request: Request, body: unknown) => Response;

function fakeServer(routes: Record<string, Handler>) {
  const calls: { route: string; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route: route + url.search, body });
      if (route === 'GET /api/v0/me') return Response.json(me);
      const handler = routes[route];
      return handler ? handler(request, body) : notFound();
    },
  });
  return { api, calls };
}

const base = '/api/v0/sites/docs';
const sitePage = (links: ShareLink[]): Record<string, Handler> => ({
  [`GET ${base}`]: () => Response.json(docs),
  [`GET ${base}/deploys`]: () => Response.json({ items: [] }),
  [`GET ${base}/policies`]: () =>
    Response.json({
      items: [
        {
          id: '01928c5e-7a1b-7c3d-9e4f-000000000010',
          path: '/',
          visibility: 'protected',
          methods: ['links'],
          version: 1,
          createdAt: now,
          updatedAt: now,
        },
      ],
    }),
  [`GET ${base}/links`]: () => Response.json({ items: links }),
  [`GET ${base}/domains`]: () => Response.json({ items: [] }),
});

const at = (path: string) => window.history.replaceState(null, '', path);

/** Fails if a token, or any part of its secret, is on the page or in the title. */
function expectNoToken() {
  expect(document.body.innerHTML).not.toContain('wl_');
  expect(document.body.innerHTML).not.toContain('TESTsecret');
  expect(document.title).not.toContain('wl_');
}

beforeEach(() => at('/sites/docs/links'));
afterEach(cleanup);

describe('share links on the site page', () => {
  it('lists each link with its status, label, ID and what it grants', async () => {
    const { api } = fakeServer(
      sitePage([
        link('q4v7m2xkpa', {
          label: 'Linear ENG-123',
          path: '/docs/',
          expiresAt: now + 7 * day,
          maxUses: 3,
          uses: 1,
          lastAccessedAt: now - 3 * hour,
        }),
        link('b2c3d4e5f6', { status: 'expired', expiresAt: now - day }),
      ]),
    );
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Share links' });
    const [first, second] = within(list).getAllByRole('listitem');
    expect(first!.textContent).toContain('Active');
    expect(first!.textContent).toContain('Linear ENG-123');
    expect(first!.textContent).toContain('q4v7m2xkpa');
    expect(first!.textContent).toContain('Only /docs/');
    expect(first!.textContent).toContain('Expires in 7 days');
    expect(first!.textContent).toContain('1 of 3 uses');
    expect(first!.textContent).toContain('Last opened 3 hours ago');
    expect(second!.textContent).toContain('Expired');
    expect(second!.textContent).toContain('No label');
    expect(second!.textContent).toContain('Whole site');
    expect(second!.textContent).toContain('Unlimited uses');
    expect(second!.textContent).toContain('Not opened yet');
    expect(
      within(first!)
        .getByRole('link', { name: 'Access log of “Linear ENG-123”' })
        .getAttribute('href'),
    ).toBe('/sites/docs/links/q4v7m2xkpa');
    expect(
      within(second!).getByRole('button', { name: 'Revoke link b2c3d4e5f6' }),
    ).toBeTruthy();
    expectNoToken();
  });

  it('shows an empty state and a retry after a failed load', async () => {
    let fail = true;
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () =>
        fail
          ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
          : Response.json({ items: [] }),
    });
    render(<App api={api} />);
    await screen.findByText(/Could not load the share links/);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(
      await screen.findByRole('heading', { name: 'No share links yet' }),
    ).toBeTruthy();
  });

  it('creates a link and shows its URL once, with a copy action', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    const made = link('k7m2q6xw4a', {
      label: 'sent to a reviewer',
      path: '/docs/',
      maxUses: 1,
      expiresAt: now + 7 * day,
    });
    const { api, calls } = fakeServer({
      ...sitePage([]),
      [`POST ${base}/links`]: () =>
        Response.json(created(made), { status: 201 }),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { name: 'No share links yet' });
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }));
    const dialog = screen.getByRole('dialog', {
      name: 'Create a share link for docs',
    });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Label' }), {
      target: { value: '  sent to a reviewer ' },
    });
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Opens' }), {
      target: { value: '/docs/' },
    });
    fireEvent.change(
      within(dialog).getByRole('combobox', { name: 'Expires' }),
      {
        target: { value: '7d' },
      },
    );
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Uses' }), {
      target: { value: 'once' },
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Create link' }),
    );

    const reveal = await screen.findByRole('dialog', {
      name: 'Copy your share link',
    });
    const post = calls.find((c) => c.route === `POST ${base}/links`)!;
    const body = post.body as Record<string, number | string>;
    expect(body.label).toBe('sent to a reviewer');
    expect(body.path).toBe('/docs/');
    expect(body.maxUses).toBe(1);
    expect(
      Math.abs((body.expiresAt as number) - (Date.now() + 7 * day)),
    ).toBeLessThan(60_000);
    expect(reveal.textContent).toContain('only time Wardlock shows this link');
    const field = within(reveal).getByRole('textbox', { name: 'Share link' });
    expect((field as HTMLInputElement).value).toBe(created(made).url);
    fireEvent.click(
      within(reveal).getByRole('button', { name: 'Copy share link' }),
    );
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(created(made).url),
    );
    expect(await within(reveal).findByText('Share link copied.')).toBeTruthy();
    expect(document.title).not.toContain('wl_');

    fireEvent.click(within(reveal).getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const list = screen.getByRole('list', { name: 'Share links' });
    expect(list.textContent).toContain('sent to a reviewer');
    expect(list.textContent).toContain('k7m2q6xw4a');
    expectNoToken();
  });

  it('checks the form before sending and shows server field errors', async () => {
    const { api, calls } = fakeServer({
      ...sitePage([]),
      [`POST ${base}/links`]: () =>
        Response.json(
          {
            code: 'invalid_share_link',
            message: 'The label has control characters.',
            target: 'label',
          },
          { status: 400 },
        ),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { name: 'No share links yet' });
    fireEvent.click(screen.getByRole('button', { name: 'Create link' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Opens' }), {
      target: { value: 'docs' },
    });
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Uses' }), {
      target: { value: 'limited' },
    });
    fireEvent.change(
      within(dialog).getByRole('spinbutton', { name: 'Number of uses' }),
      { target: { value: '0' } },
    );
    fireEvent.change(
      within(dialog).getByRole('combobox', { name: 'Expires' }),
      {
        target: { value: 'custom' },
      },
    );
    fireEvent.change(within(dialog).getByLabelText('Expiry date and time'), {
      target: { value: '2001-01-01T09:00' },
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Create link' }),
    );
    expect(
      within(dialog)
        .getByRole('textbox', { name: 'Opens' })
        .getAttribute('aria-invalid'),
    ).toBe('true');
    expect(dialog.textContent).toContain('Start with a slash');
    expect(dialog.textContent).toContain('Enter a whole number from 1 to');
    expect(dialog.textContent).toContain('Choose a time in the future.');
    expect(calls.some((c) => c.route.startsWith('POST'))).toBe(false);

    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Opens' }), {
      target: { value: '/' },
    });
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Uses' }), {
      target: { value: 'unlimited' },
    });
    fireEvent.change(
      within(dialog).getByRole('combobox', { name: 'Expires' }),
      {
        target: { value: 'never' },
      },
    );
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Create link' }),
    );
    await waitFor(() =>
      expect(
        within(dialog)
          .getByRole('textbox', { name: 'Label' })
          .getAttribute('aria-invalid'),
      ).toBe('true'),
    );
    expect(dialog.textContent).toContain('The label has control characters.');
    // Never and unlimited send neither field: the API's defaults.
    const post = calls.find((c) => c.route === `POST ${base}/links`)!;
    expect(post.body).toEqual({ path: '/' });
  });

  it('revokes a link only after a confirmation naming it', async () => {
    const l = link('q4v7m2xkpa', { label: 'Linear ENG-123' });
    const { api, calls } = fakeServer({
      ...sitePage([l, link('b2c3d4e5f6')]),
      [`DELETE ${base}/links/q4v7m2xkpa`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Share links' });
    fireEvent.click(
      within(list).getByRole('button', { name: 'Revoke “Linear ENG-123”' }),
    );
    let dialog = screen.getByRole('dialog', {
      name: 'Revoke “Linear ENG-123”?',
    });
    expect(dialog.textContent).toContain(
      'loses access to docs.sites.example.test',
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(calls.some((c) => c.route.startsWith('DELETE'))).toBe(false);

    fireEvent.click(
      within(list).getByRole('button', { name: 'Revoke “Linear ENG-123”' }),
    );
    dialog = screen.getByRole('dialog');
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Revoke link' }),
    );
    expect(
      await screen.findByText('Revoked “Linear ENG-123”. It no longer works.'),
    ).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
    expect(list.textContent).not.toContain('Linear ENG-123');
  });

  it('keeps the revoke dialog open with the reason when it fails', async () => {
    const { api } = fakeServer(sitePage([link('q4v7m2xkpa')]));
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Share links' });
    fireEvent.click(
      within(list).getByRole('button', { name: 'Revoke link q4v7m2xkpa' }),
    );
    const dialog = screen.getByRole('dialog', {
      name: 'Revoke link q4v7m2xkpa?',
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Revoke link' }),
    );
    expect(await within(dialog).findByRole('alert')).toBeTruthy();
    expect(dialog.textContent).toContain('it no longer exists');
  });

  it('rotates a link after a confirmation and shows the new URL once', async () => {
    const l = link('q4v7m2xkpa', { label: 'Linear ENG-123' });
    const rotated = { ...l, rotatedAt: now };
    const { api, calls } = fakeServer({
      ...sitePage([l]),
      [`POST ${base}/links/q4v7m2xkpa/rotate`]: () =>
        Response.json(
          created(rotated, 'NEWsecretNEWsecretNEWsecretNEWsecretNEW1234'),
        ),
    });
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Share links' });
    fireEvent.click(
      within(list).getByRole('button', { name: 'Rotate “Linear ENG-123”' }),
    );
    const confirm = screen.getByRole('dialog', {
      name: 'Rotate “Linear ENG-123”?',
    });
    expect(confirm.textContent).toContain('current link stops working');
    expect(calls.some((c) => c.route.endsWith('/rotate'))).toBe(false);
    fireEvent.click(
      within(confirm).getByRole('button', { name: 'Rotate link' }),
    );
    const reveal = await screen.findByRole('dialog', {
      name: 'Copy the new link',
    });
    expect(
      (
        within(reveal).getByRole('textbox', {
          name: 'Share link',
        }) as HTMLInputElement
      ).value,
    ).toContain('?t=wl_q4v7m2xkpa_NEWsecret');
    // Escape: the native dialog's cancel event.
    fireEvent(reveal, new Event('cancel', { cancelable: true }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.body.innerHTML).not.toContain('NEWsecret');
    expectNoToken();
  });
});

describe('link access log', () => {
  const entry = (
    n: number,
    extra: Partial<AccessLogEntry> = {},
  ): AccessLogEntry => ({
    id: `01928c5e-7a1b-7c3d-9e4f-0000000001${n.toString().padStart(2, '0')}`,
    occurredAt: now - n * hour,
    host: 'docs.sites.example.test',
    path: `/page-${n}`,
    status: 200,
    via: 'link',
    linkPublicId: 'q4v7m2xkpa',
    ip: '203.0.113.0',
    userAgent: 'Mozilla/5.0 (synthetic)',
    ...extra,
  });

  beforeEach(() => at('/sites/docs/links/q4v7m2xkpa'));

  it('shows each visit’s country and the credit only while the lookup is on', async () => {
    const attribution =
      'Synthetic country data (https://example.test/countries)';
    for (const lookup of ['on', 'off', 'failed'] as const) {
      const { api, calls } = fakeServer({
        ...sitePage([]),
        [`GET ${base}/links`]: () =>
          Response.json({ items: [link('q4v7m2xkpa')] }),
        'GET /api/v0/analytics': () =>
          lookup === 'failed'
            ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
            : Response.json({
                days: 7,
                from: now - 7 * day,
                to: now,
                total: { allowed: 1, refused: 0 },
                hours: [],
                via: [],
                sites: [],
                groups: [],
                tags: [],
                links: [],
                paths: [],
                otherPaths: { allowed: 0, refused: 0 },
                browsers: [],
                ...(lookup === 'on'
                  ? { countries: [], countryLookup: { attribution } }
                  : {}),
              }),
        [`GET ${base}/links/q4v7m2xkpa/access-log`]: () =>
          Response.json({
            items: [entry(1, lookup === 'off' ? {} : { country: 'AU' })],
          }),
      });
      render(<App api={api} />);
      const table = await screen.findByRole('table', {
        name: 'Requests through link q4v7m2xkpa',
      });
      const headers = () =>
        within(table)
          .getAllByRole('columnheader')
          .map((th) => th.textContent);
      // The link's site's lookup.
      await waitFor(() =>
        expect(calls.map((c) => c.route)).toContain(
          'GET /api/v0/analytics?site=docs&days=7',
        ),
      );
      if (lookup === 'on') {
        await waitFor(() =>
          expect(headers()).toEqual([
            'Time',
            'Path',
            'Result',
            'IP address',
            'Country',
            'Browser or client',
          ]),
        );
        expect(within(table).getByText('Australia')).toBeTruthy();
        expect(
          screen
            .getByRole('link', { name: 'https://example.test/countries' })
            .getAttribute('href'),
        ).toBe('https://example.test/countries');
      } else {
        // Let the read settle before checking nothing changed.
        if (lookup === 'off') await screen.findByText('1 request in 7 days');
        else await new Promise((resolve) => setTimeout(resolve, 20));
        expect(headers()).not.toContain('Country');
        expect(document.body.textContent).not.toMatch(/countr|Australia/i);
      }
      cleanup();
    }
  });

  it('shows the visits newest first and loads older ones', async () => {
    // A refused request with no IP recorded and no User-Agent sent.
    const refused = entry(2, { status: 401 });
    delete refused.ip;
    delete refused.userAgent;
    const { api, calls } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () =>
        Response.json({
          items: [link('q4v7m2xkpa', { label: 'Linear ENG-123' })],
        }),
      [`GET ${base}/links/q4v7m2xkpa/access-log`]: (request) =>
        new URL(request.url).searchParams.get('cursor') === 'next'
          ? Response.json({ items: [entry(3)] })
          : Response.json({
              items: [entry(1), refused],
              nextCursor: 'next',
            }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'Linear ENG-123' }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(document.title).toBe('Link access log · docs · Wardlock'),
    );
    const table = await screen.findByRole('table', {
      name: 'Requests through link q4v7m2xkpa',
    });
    let rows = within(table).getAllByRole('row').slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain('/page-1');
    expect(rows[0]!.textContent).toContain('Opened · 200');
    expect(rows[0]!.textContent).toContain('203.0.113.0');
    expect(rows[0]!.textContent).toContain('Mozilla/5.0 (synthetic)');
    expect(rows[1]!.textContent).toContain('Refused · 401');
    expect(rows[1]!.textContent).toContain('Not recorded');
    expect(rows[1]!.textContent).toContain('None sent');
    fireEvent.click(
      screen.getByRole('button', { name: 'Show older requests' }),
    );
    await waitFor(() =>
      expect(within(table).getAllByRole('row')).toHaveLength(4),
    );
    rows = within(table).getAllByRole('row').slice(1);
    expect(rows[2]!.textContent).toContain('/page-3');
    expect(
      screen.queryByRole('button', { name: 'Show older requests' }),
    ).toBeNull();
    expect(calls.map((c) => c.route)).toContain(
      `GET ${base}/links/q4v7m2xkpa/access-log?cursor=next`,
    );
    expect(
      screen
        .getByRole('link', { name: 'All share links' })
        .getAttribute('href'),
    ).toBe('/sites/docs/links');
    // The page stays the site's, on its Links tab.
    expect(
      screen.getByRole('heading', { level: 1, name: 'docs' }),
    ).toBeTruthy();
    expect(
      screen.getByRole('link', { name: 'Links' }).getAttribute('aria-current'),
    ).toBe('page');
    expectNoToken();
  });

  it('keeps a revoked link’s history and says it no longer works', async () => {
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () => Response.json({ items: [] }),
      [`GET ${base}/links/q4v7m2xkpa/access-log`]: () =>
        Response.json({ items: [entry(1)] }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'Link q4v7m2xkpa' }),
    ).toBeTruthy();
    expect(screen.getByText('Revoked')).toBeTruthy();
    expect(screen.getByText(/This link no longer works/)).toBeTruthy();
  });

  it('names a revoked link by the label its visits carry', async () => {
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () => Response.json({ items: [] }),
      [`GET ${base}/links/q4v7m2xkpa/access-log`]: () =>
        Response.json({ items: [entry(1, { linkLabel: 'Linear ENG-123' })] }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'Linear ENG-123' }),
    ).toBeTruthy();
    expect(screen.getByText('Revoked')).toBeTruthy();
  });

  it('shows an empty state for a link nobody has opened', async () => {
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () =>
        Response.json({ items: [link('q4v7m2xkpa')] }),
      [`GET ${base}/links/q4v7m2xkpa/access-log`]: () =>
        Response.json({ items: [] }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'No requests yet' }),
    ).toBeTruthy();
  });

  it('treats a missing or foreign link as not found', async () => {
    const { api } = fakeServer(sitePage([]));
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'Link not found' }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(document.title).toBe('Link not found · Wardlock'),
    );
  });

  it('offers a retry when the log cannot load', async () => {
    let fail = true;
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/links`]: () => Response.json({ items: [] }),
      [`GET ${base}/links/q4v7m2xkpa/access-log`]: () =>
        fail
          ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
          : Response.json({ items: [entry(1)] }),
    });
    render(<App api={api} />);
    await screen.findByText(/Could not load the access log/);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('table')).toBeTruthy();
  });
});
