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
import type { Deploy, Policy, Site } from './format.js';
import type { Preview } from './previews.js';

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

function deploy(tail: string, extra: Partial<Deploy> = {}): Deploy {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-0000${tail}`,
    status: 'inactive',
    createdAt: now - 5 * hour,
    activatedAt: now - 5 * hour,
    fileCount: 3,
    totalSize: 1_532_918,
    spa: false,
    immutableAssets: false,
    ...extra,
  };
}

const live = deploy('00000001', {
  status: 'active',
  message: 'Launch the field guide',
  activatedAt: now - day,
});

const docs: Site = {
  id: '01928c5e-7a1b-7c3d-9e4f-000000000010',
  name: 'docs',
  owner: {
    kind: 'owner',
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    name: 'owner@example.test',
  },
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now - 10 * day,
  unavailable: false,
  tagIds: [],
  pinned: false,
  activeDeployId: live.id,
  activatedAt: live.activatedAt!,
};

function preview(
  name: string,
  tail: string,
  extra: Partial<Preview> = {},
  deployExtra: Partial<Deploy> = {},
): Preview {
  // Never live on the site unless promoted.
  const served = deploy(tail, { createdAt: now - 2 * hour });
  delete served.activatedAt;
  return {
    name,
    hostname: `docs--${name}.sites.example.test`,
    url: `https://docs--${name}.sites.example.test/`,
    createdAt: now - 3 * day,
    deployedAt: now - 2 * hour,
    expiresAt: now + 5 * day,
    deploy: { ...served, status: 'preview', preview: name, ...deployExtra },
    ...extra,
  };
}

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

const notFound = () =>
  Response.json({ code: 'not_found', message: 'Not found.' }, { status: 404 });

type Handler = (request: Request, body: unknown) => Response;

function fakeServer(routes: Record<string, Handler>) {
  const calls: { route: string; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const route = `${request.method} ${new URL(request.url).pathname}`;
      const text = await request.text();
      const body = text ? JSON.parse(text) : undefined;
      calls.push({ route, body });
      if (route === 'GET /api/v0/me') return Response.json(me);
      const handler = routes[route];
      return handler ? handler(request, body) : notFound();
    },
  });
  return { api, calls };
}

const base = '/api/v0/sites/docs';
function sitePage(
  previews: Preview[] | (() => Response),
  {
    site = docs,
    visibility = 'public',
  }: { site?: Site; visibility?: Policy['visibility'] } = {},
): Record<string, Handler> {
  return {
    [`GET ${base}`]: () => Response.json(site),
    [`GET ${base}/deploys`]: () => Response.json({ items: [live] }),
    [`GET ${base}/policies`]: () =>
      Response.json({ items: [policy(visibility)] }),
    [`GET ${base}/links`]: () => Response.json({ items: [] }),
    [`GET ${base}/previews`]:
      typeof previews === 'function'
        ? previews
        : () => Response.json({ items: previews }),
    'GET /api/v0/groups': () => Response.json({ items: [] }),
    'GET /api/v0/tags': () => Response.json({ items: [] }),
  };
}

const card = () => screen.getByRole('region', { name: 'Previews' });
const row = (name: string) =>
  within(card()).getByRole('listitem', { name: name });

beforeEach(() => window.history.replaceState(null, '', '/sites/docs/previews'));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('previews on the site page', () => {
  it('shows the command to make one when there are none', async () => {
    const { api } = fakeServer(sitePage([]));
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'No previews yet' }),
    ).toBeTruthy();
    expect(document.title).toBe('Previews · docs · Wardlock');
    expect(
      (within(card()).getByLabelText('Command') as HTMLInputElement).value,
    ).toBe('wardlock deploy dist docs --preview pr-42');
    // The tab is current among the site's tabs.
    expect(
      within(screen.getByRole('navigation', { name: 'Site sections' }))
        .getByRole('link', { name: 'Previews' })
        .getAttribute('aria-current'),
    ).toBe('page');
  });

  it('lists each preview with its address, deploy and expiry', async () => {
    // Made with --expires never.
    const lasting = preview('redesign', '00000043');
    delete lasting.expiresAt;
    const { api } = fakeServer(
      sitePage([
        preview('pr-42', '00000042', {}, { message: 'Try the new menu' }),
        lasting,
        // Promoted: its deploy is the live one.
        preview(
          'launch',
          '00000001',
          {},
          { status: 'active', activatedAt: now - day },
        ),
      ]),
    );
    render(<App api={api} />);
    await screen.findByRole('list', { name: 'Previews' });

    const first = row('pr-42');
    expect(first.textContent).toContain('docs--pr-42.sites.example.test');
    expect(first.textContent).toContain('00000042');
    expect(first.textContent).toContain('Try the new menu');
    expect(first.textContent).toContain('Deployed 2 hours ago');
    expect(first.textContent).toContain('3 files · 1.5 MB');
    expect(first.textContent).toContain('Expires in 5 days');
    expect(within(first).queryByText('Live on site')).toBeNull();
    for (const name of [
      'Open preview pr-42',
      'Promote pr-42',
      'Delete preview pr-42',
    ])
      expect(within(first).getByRole('button', { name })).toBeTruthy();

    const second = row('redesign');
    expect(second.textContent).toContain('No message');
    expect(second.textContent).toContain('Never expires');

    // The deploy already live has nothing to promote.
    const promoted = row('launch');
    expect(within(promoted).getByText('Live on site')).toBeTruthy();
    expect(
      within(promoted).queryByRole('button', { name: 'Promote launch' }),
    ).toBeNull();
    expect(
      within(promoted).getByRole('button', { name: 'Delete preview launch' }),
    ).toBeTruthy();
  });

  it('promotes after a confirmation naming both deploys', async () => {
    const candidate = preview(
      'pr-42',
      '00000042',
      {},
      { message: 'Try the new menu' },
    );
    const promoted = {
      ...candidate.deploy,
      status: 'active' as const,
      activatedAt: now,
    };
    // The server's site, as it is after each change.
    let current = docs;
    const { api, calls } = fakeServer({
      ...sitePage([candidate]),
      [`GET ${base}`]: () => Response.json(current),
      [`POST ${base}/previews/pr-42/promote`]: () => {
        current = { ...docs, activeDeployId: promoted.id, activatedAt: now };
        return Response.json({
          site: current,
          deploy: promoted,
          preview: { ...candidate, deploy: promoted },
        });
      },
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Promote pr-42' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Promote pr-42?',
    });
    expect(dialog.textContent).toContain('Live now');
    expect(dialog.textContent).toContain('00000001 Launch the field guide');
    expect(dialog.textContent).toContain('Goes live from pr-42');
    expect(dialog.textContent).toContain('00000042 Try the new menu');
    expect(dialog.textContent).toContain('docs.sites.example.test');

    // Cancel sends nothing.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(calls.some((c) => c.route.endsWith('/promote'))).toBe(false);

    fireEvent.click(screen.getByRole('button', { name: 'Promote pr-42' }));
    fireEvent.click(
      within(
        await screen.findByRole('dialog', { name: 'Promote pr-42?' }),
      ).getByRole('button', { name: 'Put this deploy live' }),
    );
    expect(
      await within(card()).findByText(
        'Promoted pr-42. Deploy 00000042 is live.',
      ),
    ).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    // The preview's deploy is live now, so Promote goes.
    expect(await within(row('pr-42')).findByText('Live on site')).toBeTruthy();
    expect(
      within(row('pr-42')).queryByRole('button', { name: 'Promote pr-42' }),
    ).toBeNull();
    // Focus lands on the result, and the site is read again in place.
    await waitFor(() =>
      expect(document.activeElement?.textContent).toContain('Promoted pr-42'),
    );
    await waitFor(() =>
      expect(calls.filter((c) => c.route === `GET ${base}`)).toHaveLength(2),
    );
  });

  it('explains a path preview the live site has moved past', async () => {
    const { api } = fakeServer({
      ...sitePage([preview('pr-42', '00000042', {}, { path: '/guide/' })]),
      [`POST ${base}/previews/pr-42/promote`]: () =>
        Response.json(
          { code: 'stale_base', message: 'The site changed.' },
          { status: 409 },
        ),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Promote pr-42' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Promote pr-42?',
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Put this deploy live' }),
    );
    expect(
      await within(dialog).findByText(
        /Deploy the preview again, then promote it\./,
      ),
    ).toBeTruthy();
    // The dialog stays open, and nothing is announced as done.
    expect(screen.getByRole('dialog', { name: 'Promote pr-42?' })).toBeTruthy();
  });

  it('shows the server’s reason when a promotion fails', async () => {
    const { api } = fakeServer({
      ...sitePage([preview('pr-42', '00000042')]),
      [`POST ${base}/previews/pr-42/promote`]: () =>
        Response.json(
          { code: 'limit_exceeded', message: 'Too many deploys today.' },
          { status: 413 },
        ),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Promote pr-42' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Promote pr-42?',
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Put this deploy live' }),
    );
    expect(
      await within(dialog).findByText(
        'Could not promote the preview: Too many deploys today.',
      ),
    ).toBeTruthy();
  });

  it('deletes a preview after a confirmation', async () => {
    const { api, calls } = fakeServer({
      ...sitePage([preview('pr-42', '00000042'), preview('pr-7', '00000007')]),
      [`DELETE ${base}/previews/pr-42`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Delete preview pr-42' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Delete preview pr-42?',
    });
    expect(dialog.textContent).toContain('docs--pr-42.sites.example.test');
    expect(dialog.textContent).toContain('stays as it is');
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Delete preview' }),
    );
    expect(
      await within(card()).findByText('Deleted preview pr-42.'),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        within(card()).queryByRole('listitem', { name: 'pr-42' }),
      ).toBeNull(),
    );
    expect(row('pr-7')).toBeTruthy();
    expect(
      calls.filter((c) => c.route === `DELETE ${base}/previews/pr-42`),
    ).toHaveLength(1);
    await waitFor(() =>
      expect(document.activeElement?.textContent).toBe(
        'Deleted preview pr-42.',
      ),
    );
  });

  it('keeps the dialog open with the reason when a delete fails', async () => {
    const { api } = fakeServer({
      ...sitePage([preview('pr-42', '00000042')]),
      [`DELETE ${base}/previews/pr-42`]: () =>
        Response.json(
          { code: 'internal', message: 'Something went wrong.' },
          { status: 500 },
        ),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Delete preview pr-42' }),
    );
    const dialog = await screen.findByRole('dialog', {
      name: 'Delete preview pr-42?',
    });
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Delete preview' }),
    );
    expect(
      await within(dialog).findByText(
        'Could not delete the preview: Something went wrong.',
      ),
    ).toBeTruthy();
    expect(row('pr-42')).toBeTruthy();
  });

  it('offers a retry when the list cannot load', async () => {
    let reads = 0;
    const { api } = fakeServer(
      sitePage(() => {
        reads += 1;
        return reads === 1
          ? Response.json(
              { code: 'internal', message: 'Something went wrong.' },
              { status: 500 },
            )
          : Response.json({ items: [preview('pr-42', '00000042')] });
      }),
    );
    render(<App api={api} />);
    expect(
      await screen.findByText(/Could not load the previews\./),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('listitem', { name: 'pr-42' })).toBeTruthy();
  });

  it('shows more previews on request', async () => {
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/previews`]: (request) =>
        new URL(request.url).searchParams.get('cursor')
          ? Response.json({ items: [preview('pr-7', '00000007')] })
          : Response.json({
              items: [preview('pr-42', '00000042')],
              nextCursor: 'next',
            }),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show more previews' }),
    );
    expect(await screen.findByRole('listitem', { name: 'pr-7' })).toBeTruthy();
    expect(row('pr-42')).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Show more previews' }),
      ).toBeNull(),
    );
  });

  it('opens a public site’s preview at its address', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const { api, calls } = fakeServer(sitePage([preview('pr-42', '00000042')]));
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Open preview pr-42' }),
    );
    expect(open).toHaveBeenCalledWith(
      'https://docs--pr-42.sites.example.test/',
      '_blank',
      'noopener',
    );
    expect(calls.some((c) => c.route === `POST ${base}/links`)).toBe(false);
  });

  it('opens a protected site’s preview through a short-lived link', async () => {
    const tab = {
      opener: {} as unknown,
      location: { href: '' },
      close: vi.fn(),
    };
    vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
    // A synthetic token in the documented shape.
    const token = `wl_abcdefghij_${'A'.repeat(43)}`;
    const { api, calls } = fakeServer({
      ...sitePage([preview('pr-42', '00000042')], { visibility: 'protected' }),
      [`POST ${base}/links`]: () =>
        Response.json(
          {
            shareLink: {
              id: '01928c5e-7a1b-7c3d-9e4f-000000000002',
              publicId: 'abcdefghij',
              label: 'Opened from dashboard',
              path: '/',
              expiresAt: now + 10 * 60_000,
              useCount: 0,
              status: 'active',
              createdAt: now,
            },
            token,
            url: `https://docs.sites.example.test/?t=${token}`,
          },
          { status: 201 },
        ),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Open preview pr-42' }),
    );
    // The site-wide link opens on the preview's own hostname.
    await waitFor(() =>
      expect(tab.location.href).toBe(
        `https://docs--pr-42.sites.example.test/?t=${token}`,
      ),
    );
    expect(
      calls.find((c) => c.route === `POST ${base}/links`)?.body,
    ).toMatchObject({ label: 'Opened from dashboard', path: '/' });
  });

  it('offers no action on a taken-down site', async () => {
    const { api } = fakeServer(
      sitePage([preview('pr-42', '00000042')], {
        site: { ...docs, unavailable: true },
      }),
    );
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'pr-42' });
    expect(card().textContent).toContain('its previews stay as they are');
    expect(within(row('pr-42')).queryByRole('button')).toBeNull();
  });
});
