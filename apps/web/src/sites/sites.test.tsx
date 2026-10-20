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
import type { Deploy, Policy, Site } from './format.js';
import type { AccessLogEntry } from './visits.js';

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

function site(name: string, extra: Partial<Site> = {}): Site {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${name.padEnd(12, '0').slice(0, 12)}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: now - 48 * hour,
    unavailable: false,
    ...extra,
  };
}

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

function policy(path: string, visibility: Policy['visibility']): Policy {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${path.length.toString().padStart(12, '0')}`,
    path,
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
  const calls: string[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      const text = await request.text();
      calls.push(route + url.search);
      if (route === 'GET /api/v0/me') return Response.json(me);
      const handler = routes[route];
      return handler
        ? handler(request, text ? JSON.parse(text) : undefined)
        : notFound();
    },
  });
  return { api, calls };
}

/** Routes for one site with these deploys and policies. */
function oneSite(
  s: Site,
  deploys: Deploy[],
  policies: Policy[] = [policy('/', 'public')],
): Record<string, Handler> {
  const base = `/api/v0/sites/${s.name}`;
  return {
    [`GET ${base}`]: () => Response.json(s),
    [`GET ${base}/deploys`]: () => Response.json({ items: deploys }),
    [`GET ${base}/policies`]: () => Response.json({ items: policies }),
    [`GET ${base}/links`]: () => Response.json({ items: [] }),
    [`GET ${base}/domains`]: () => Response.json({ items: [] }),
  };
}

const at = (path: string) => window.history.replaceState(null, '', path);

beforeEach(() => at('/sites'));
afterEach(cleanup);

describe('site list', () => {
  it('lists each site with its access and last deploy', async () => {
    const docs = site('docs', { activatedAt: now - 3 * hour });
    const drafts = site('drafts');
    const { api } = fakeServer({
      'GET /api/v0/sites': () => Response.json({ items: [docs, drafts] }),
      ...oneSite(docs, [], [policy('/', 'protected')]),
      ...oneSite(drafts, []),
    });
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Sites' });
    const rows = within(list).getAllByRole('listitem');
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain('docs.sites.example.test');
    expect(rows[0]!.textContent).toContain('Protected');
    // Protected and Public carry the same weight; a lock tells them apart.
    const badge = within(rows[0]!).getByText('Protected');
    expect(badge.className).toContain('wl-badge-neutral');
    expect(badge.querySelector('[aria-hidden="true"] svg')).toBeTruthy();
    expect(rows[0]!.textContent).toContain('Deployed 3 hours ago');
    expect(rows[1]!.textContent).toContain('Public');
    expect(rows[1]!.textContent).toContain('Not deployed yet');
    expect(
      within(rows[0]!).getByRole('link', { name: 'docs' }).getAttribute('href'),
    ).toBe('/sites/docs');
    await waitFor(() => expect(document.title).toBe('Sites · Wardlock'));
    expect(
      screen.getByRole('link', { name: 'Sites' }).getAttribute('aria-current'),
    ).toBe('page');
  });

  it('marks a site the operator has taken down', async () => {
    const docs = site('docs', { unavailable: true, activatedAt: now - hour });
    const { api } = fakeServer({
      'GET /api/v0/sites': () => Response.json({ items: [docs] }),
      ...oneSite(docs, [], [policy('/', 'protected')]),
    });
    render(<App api={api} />);
    const list = await screen.findByRole('list', { name: 'Sites' });
    const badge = within(list).getByText('Taken down');
    expect(badge.className).toContain('wl-badge-warning');
    expect(list.textContent).not.toContain('Protected');
  });

  it('explains an empty list without offering unbuilt actions', async () => {
    const { api } = fakeServer({
      'GET /api/v0/sites': () => Response.json({ items: [] }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 2, name: 'No sites yet' }),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: /create|new/i })).toBeNull();
  });

  it('offers to try again when the list fails to load', async () => {
    let up = false;
    const { api } = fakeServer({
      'GET /api/v0/sites': () =>
        up
          ? Response.json({ items: [] })
          : Response.json(
              { code: 'internal', message: 'Internal error.' },
              { status: 500 },
            ),
    });
    render(<App api={api} />);
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Could not load your sites.',
    );
    up = true;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('No sites yet')).toBeTruthy();
  });

  it('loads further pages on request', async () => {
    const a = site('alpha');
    const b = site('beta');
    const { api, calls } = fakeServer({
      'GET /api/v0/sites': (request) =>
        new URL(request.url).searchParams.get('cursor') === 'next'
          ? Response.json({ items: [b] })
          : Response.json({ items: [a], nextCursor: 'next' }),
      ...oneSite(a, []),
      ...oneSite(b, []),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show more sites' }),
    );
    expect(await screen.findByRole('link', { name: 'beta' })).toBeTruthy();
    expect(
      screen.queryByRole('button', { name: 'Show more sites' }),
    ).toBeNull();
    expect(calls).toContain('GET /api/v0/sites?cursor=next');
  });
});

describe('site detail', () => {
  const live = deploy('00000003', {
    status: 'active',
    message: 'Fix typo on the install page',
    activatedAt: now - hour,
    immutableAssets: true,
  });
  const earlier = deploy('00000002', { message: 'First draft' });
  // A pending deploy has never been live, so it has no activation time.
  const pending: Deploy = {
    id: '01928c5e-7a1b-7c3d-9e4f-000000000004',
    status: 'pending',
    createdAt: now - hour,
    fileCount: 3,
    totalSize: 1_532_918,
    spa: false,
    immutableAssets: false,
  };
  const docs = site('docs', {
    activeDeployId: live.id,
    activatedAt: live.activatedAt!,
  });

  it('shows the address, access and deploy history', async () => {
    at('/sites/docs');
    const { api } = fakeServer(
      oneSite(
        docs,
        [pending, live, earlier],
        [policy('/', 'protected'), policy('/assets/', 'public')],
      ),
    );
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'docs' }),
    ).toBeTruthy();
    expect(
      (
        screen.getByRole('textbox', {
          name: 'Site address',
        }) as HTMLInputElement
      ).value,
    ).toBe('https://docs.sites.example.test/');
    // The access policies card further down lists /assets/ too once its own
    // load finishes, so look for the path in this card only.
    const access = screen.getByRole('region', { name: 'Address and access' });
    expect(
      within(access).getByText('Only visitors with access can open this site.'),
    ).toBeTruthy();
    expect(within(access).getByText('/assets/')).toBeTruthy();
    const rows = within(
      screen.getByRole('list', { name: 'Deploy history' }),
    ).getAllByRole('listitem');
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining('Not activated'),
      expect.stringContaining('Live'),
      expect.stringContaining('Not live'),
    ]);
    expect(rows[1]!.textContent).toContain('Fix typo on the install page');
    expect(rows[1]!.textContent).toContain('3 files');
    expect(rows[1]!.textContent).toContain('1.5 MB');
    // The long-term caching option is labelled only on deploys that set it.
    expect(rows[1]!.textContent).toContain('Immutable assets');
    expect(rows[0]!.textContent).not.toContain('Immutable assets');
    expect(rows[2]!.textContent).not.toContain('Immutable assets');
    // Only an earlier, once-live deploy can be rolled back to.
    expect(within(rows[0]!).queryByRole('button')).toBeNull();
    expect(within(rows[1]!).queryByRole('button')).toBeNull();
    expect(
      within(rows[2]!).getByRole('button', {
        name: 'Roll back to deploy 00000002',
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole('link', { name: 'Sites' }).getAttribute('aria-current'),
    ).toBe('page');
    expect(
      screen
        .getByRole('link', { name: 'View access log' })
        .getAttribute('href'),
    ).toBe('/sites/docs/access-log');
  });

  it('explains a taken-down site and offers no action it would refuse', async () => {
    at('/sites/docs');
    const down = { ...docs, unavailable: true };
    const { api } = fakeServer(
      oneSite(
        down,
        [live, earlier],
        [policy('/', 'protected'), policy('/drafts/', 'protected')],
      ),
    );
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'docs' }),
    ).toBeTruthy();
    expect(screen.getByText('Taken down').className).toContain(
      'wl-badge-warning',
    );
    expect(
      screen.getByText(/operator of this server has taken this site down/)
        .textContent,
    ).toContain('The operator of this server has taken this site down.');
    // Nothing to open, roll back to or tear down.
    expect(screen.queryByRole('button', { name: 'Open site' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Roll back/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Tear down site' })).toBeNull();
    // Domains are frozen and no policy may be deleted; editing stays.
    expect(
      await screen.findByText(/its domains stay as they are/),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Add domain' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Delete / })).toBeNull();
    expect(
      (await screen.findAllByRole('button', { name: /^Edit/ })).length,
    ).toBeGreaterThan(0);
    const teardown = screen.getByRole('region', {
      name: 'Tear down this site',
    });
    expect(teardown.textContent).toContain('Ask the operator of this server');
    // Access can still be changed.
    expect(
      screen.getByRole('region', { name: 'Address and access' }),
    ).toBeTruthy();
  });

  it('shows the same not-found page for missing and foreign sites', async () => {
    const texts: string[] = [];
    for (const path of ['/sites/missing', '/sites/someone-elses']) {
      at(path);
      const { api } = fakeServer({});
      const { unmount } = render(<App api={api} />);
      await screen.findByRole('heading', { level: 1, name: 'Site not found' });
      texts.push(screen.getByRole('main').textContent ?? '');
      await waitFor(() =>
        expect(document.title).toBe('Site not found · Wardlock'),
      );
      unmount();
    }
    expect(texts[0]).toBe(texts[1]);
    expect(texts[0]).not.toContain('missing');
  });

  it('rolls back after confirming and shows the new live deploy', async () => {
    at('/sites/docs');
    let deploys = [live, earlier];
    let current = docs;
    let body: unknown;
    const { api } = fakeServer({
      'GET /api/v0/sites/docs': () => Response.json(current),
      'GET /api/v0/sites/docs/deploys': () => Response.json({ items: deploys }),
      'GET /api/v0/sites/docs/policies': () =>
        Response.json({ items: [policy('/', 'public')] }),
      'POST /api/v0/sites/docs/rollback': (_, sent) => {
        body = sent;
        const now2 = Date.now();
        const back = {
          ...earlier,
          status: 'active' as const,
          activatedAt: now2,
        };
        deploys = [{ ...live, status: 'inactive' }, back];
        current = { ...docs, activeDeployId: back.id, activatedAt: now2 };
        return Response.json({ site: current, deploy: back });
      },
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Roll back to deploy 00000002',
      }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Roll back docs?' });
    expect(dialog.textContent).toContain('Live now');
    expect(dialog.textContent).toContain('Fix typo on the install page');
    // The live deploy shows when it went live; the target is named by ID.
    expect(dialog.textContent).toContain('Live since 1 hour ago');
    expect(dialog.textContent).toContain('see deploy 00000002 straight away');
    expect(dialog.textContent).not.toContain('earlier');
    expect(dialog.textContent).toContain('Goes live');
    expect(dialog.textContent).toContain('First draft');
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Roll back to this deploy' }),
    );
    await waitFor(() =>
      expect(screen.getByRole('main').textContent).toContain(
        'Rolled back. Deploy 00000002 is live.',
      ),
    );
    expect(body).toEqual({ deployId: earlier.id });
    expect(screen.queryByRole('dialog')).toBeNull();
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Roll back to deploy 00000003' }),
      ).toBeTruthy(),
    );
  });

  it('keeps the rollback dialog open and explains a refused rollback', async () => {
    at('/sites/docs');
    const { api } = fakeServer({
      ...oneSite(docs, [live, earlier]),
      'POST /api/v0/sites/docs/rollback': () =>
        Response.json(
          {
            code: 'deploy_not_activated',
            message: 'That deploy was never activated.',
          },
          { status: 409 },
        ),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Roll back to deploy 00000002',
      }),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Roll back to this deploy' }),
    );
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Could not roll back: That deploy was never activated.',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps the page after a rollback when the refresh fails', async () => {
    at('/sites/docs');
    let down = false;
    const routes = oneSite(docs, [live, earlier]);
    const { api } = fakeServer({
      ...routes,
      'GET /api/v0/sites/docs': (request, body) =>
        down
          ? Response.json(
              { code: 'internal', message: 'Internal error.' },
              { status: 500 },
            )
          : routes['GET /api/v0/sites/docs']!(request, body),
      'POST /api/v0/sites/docs/rollback': () => {
        down = true;
        const back = { ...earlier, status: 'active' as const };
        return Response.json({
          site: { ...docs, activeDeployId: back.id },
          deploy: back,
        });
      },
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Roll back to deploy 00000002',
      }),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Roll back to this deploy' }),
    );
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Could not refresh the deploy history.',
    );
    expect(screen.getByRole('main').textContent).toContain(
      'Rolled back. Deploy 00000002 is live.',
    );
    // The rollback's own answer already shows the new live deploy.
    const rows = within(
      screen.getByRole('list', { name: 'Deploy history' }),
    ).getAllByRole('listitem');
    expect(rows[0]!.textContent).toContain('Not live');
    expect(rows[1]!.textContent).toContain('Live');
    expect(
      screen.getByRole('button', { name: 'Roll back to deploy 00000003' }),
    ).toBeTruthy();

    down = false;
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  });

  it('drops a page of older deploys that a rollback refresh overtook', async () => {
    at('/sites/docs');
    const older = deploy('00000001', { message: 'Very first' });
    let releaseOlder!: () => void;
    const olderGate = new Promise<void>((resolve) => (releaseOlder = resolve));
    const routes = oneSite(docs, [live, earlier]);
    const { api } = fakeServer({
      ...routes,
      'GET /api/v0/sites/docs/deploys': (request) =>
        new URL(request.url).searchParams.get('cursor') === 'older'
          ? (olderGate.then(() =>
              Response.json({ items: [older] }),
            ) as unknown as Response)
          : Response.json({ items: [live, earlier], nextCursor: 'older' }),
      'POST /api/v0/sites/docs/rollback': () =>
        Response.json({
          site: { ...docs, activeDeployId: earlier.id },
          deploy: { ...earlier, status: 'active' },
        }),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show older deploys' }),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Roll back to deploy 00000002' }),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Roll back to this deploy' }),
    );
    await waitFor(() =>
      expect(screen.getByRole('main').textContent).toContain('Rolled back.'),
    );
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Show older deploys' }),
      ).toHaveProperty('disabled', false),
    );
    releaseOlder();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText('Very first')).toBeNull();
    expect(
      within(screen.getByRole('list', { name: 'Deploy history' })).getAllByRole(
        'listitem',
      ),
    ).toHaveLength(2);
  });

  it('tears down only after the name is typed, then returns to the list', async () => {
    at('/sites/docs');
    let deleted = false;
    const { api } = fakeServer({
      ...oneSite(docs, [live]),
      'DELETE /api/v0/sites/docs': () => {
        deleted = true;
        return new Response(null, { status: 204 });
      },
      'GET /api/v0/sites': () => Response.json({ items: [] }),
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Tear down site' }),
    );
    const dialog = screen.getByRole('dialog', { name: 'Tear down docs?' });
    expect(dialog.textContent).toContain('This cannot be undone.');
    const confirm = within(dialog).getByRole('button', {
      name: 'Tear down site',
    });
    const field = within(dialog).getByRole('textbox', {
      name: 'Type docs to confirm',
    });
    fireEvent.change(field, { target: { value: 'doc' } });
    expect((confirm as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(field, { target: { value: 'docs' } });
    expect((confirm as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(confirm);

    expect(
      await screen.findByRole('heading', { level: 1, name: 'Sites' }),
    ).toBeTruthy();
    expect(deleted).toBe(true);
    expect(window.location.pathname).toBe('/sites');
    expect(
      screen.getByText('docs was torn down. Its name is free to use again.'),
    ).toBeTruthy();
  });

  it('cancelling a teardown deletes nothing and clears the typed name', async () => {
    at('/sites/docs');
    let deleted = false;
    const { api } = fakeServer({
      ...oneSite(docs, [live]),
      'DELETE /api/v0/sites/docs': () => {
        deleted = true;
        return new Response(null, { status: 204 });
      },
    });
    render(<App api={api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Tear down site' }),
    );
    fireEvent.change(screen.getByRole('textbox', { name: /to confirm/ }), {
      target: { value: 'docs' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Tear down site' }));
    expect(
      (
        screen.getByRole('textbox', {
          name: /to confirm/,
        }) as HTMLInputElement
      ).value,
    ).toBe('');
    expect(deleted).toBe(false);
  });
});

describe('site access log', () => {
  const base = '/api/v0/sites/docs/access-log';
  const entry = (
    n: number,
    extra: Partial<AccessLogEntry> = {},
  ): AccessLogEntry => ({
    id: `01928c5e-7a1b-7c3d-9e4f-0000000001${n.toString().padStart(2, '0')}`,
    occurredAt: now - n * hour,
    host: 'docs.sites.example.test',
    path: `/page-${n}`,
    status: 200,
    via: 'public',
    ip: '203.0.113.0',
    userAgent: 'Mozilla/5.0 (synthetic)',
    ...extra,
  });

  beforeEach(() => at('/sites/docs/access-log'));

  it('shows every visit newest first, how it got in, and loads older ones', async () => {
    const refused = entry(5, { status: 401, via: 'none' });
    delete refused.ip;
    const { api, calls } = fakeServer({
      [`GET ${base}`]: (request) =>
        new URL(request.url).searchParams.get('cursor') === 'next'
          ? Response.json({ items: [entry(6, { via: 'oauth' })] })
          : Response.json({
              items: [
                entry(1, {
                  via: 'link',
                  linkPublicId: 'q4v7m2xkpa',
                  linkLabel: 'Linear ENG-123',
                }),
                entry(2, { via: 'link', linkPublicId: 'b3c4d5e6f7' }),
                entry(3, { via: 'password' }),
                entry(4, { via: 'email', status: 304 }),
                refused,
              ],
              nextCursor: 'next',
            }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Access log' }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(document.title).toBe('Access log · docs · Wardlock'),
    );
    const table = screen.getByRole('table', { name: 'Visits to docs' });
    let rows = within(table).getAllByRole('row').slice(1);
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining('Share link Linear ENG-123'),
      expect.stringContaining('Share link b3c4d5e6f7'),
      expect.stringContaining('Password'),
      expect.stringContaining('Email code'),
      expect.stringContaining('Not let in'),
    ]);
    expect(rows[0]!.textContent).toContain('/page-1');
    expect(rows[0]!.textContent).toContain('Opened · 200');
    expect(rows[0]!.textContent).toContain('203.0.113.0');
    expect(rows[3]!.textContent).toContain('Opened · 304');
    expect(rows[4]!.textContent).toContain('Refused · 401');
    expect(rows[4]!.textContent).toContain('Not recorded');
    expect(
      within(rows[0]!)
        .getByRole('link', { name: 'Linear ENG-123' })
        .getAttribute('href'),
    ).toBe('/sites/docs/links/q4v7m2xkpa');
    expect(
      within(rows[1]!)
        .getByRole('link', { name: 'b3c4d5e6f7' })
        .getAttribute('href'),
    ).toBe('/sites/docs/links/b3c4d5e6f7');

    fireEvent.click(screen.getByRole('button', { name: 'Show older visits' }));
    await waitFor(() =>
      expect(within(table).getAllByRole('row')).toHaveLength(7),
    );
    rows = within(table).getAllByRole('row').slice(1);
    expect(rows[5]!.textContent).toContain('Account sign-in');
    expect(
      screen.queryByRole('button', { name: 'Show older visits' }),
    ).toBeNull();
    expect(calls).toContain(`GET ${base}?cursor=next`);
    expect(
      screen.getByRole('link', { name: 'Back to docs' }).getAttribute('href'),
    ).toBe('/sites/docs');
    expect(document.body.innerHTML).not.toContain('wl_');
  });

  it('says when older visits fail to load and lets the owner try again', async () => {
    let fail = true;
    const { api } = fakeServer({
      [`GET ${base}`]: (request) =>
        new URL(request.url).searchParams.get('cursor')
          ? fail
            ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
            : Response.json({ items: [entry(2)] })
          : Response.json({ items: [entry(1)], nextCursor: 'next' }),
    });
    render(<App api={api} />);
    const older = await screen.findByRole('button', {
      name: 'Show older visits',
    });
    fireEvent.click(older);
    expect(
      await screen.findByText('Could not load older visits. Try again.'),
    ).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Show older visits' }));
    await waitFor(() => expect(screen.getAllByRole('row')).toHaveLength(3));
    expect(
      screen.queryByText('Could not load older visits. Try again.'),
    ).toBeNull();
  });

  it('shows an empty state for a site nobody has visited', async () => {
    const { api } = fakeServer({
      [`GET ${base}`]: () => Response.json({ items: [] }),
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'No visits yet' }),
    ).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
  });

  it('treats a missing or foreign site as not found', async () => {
    const { api } = fakeServer({});
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Site not found' }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(document.title).toBe('Site not found · Wardlock'),
    );
  });

  it('offers a retry when the log cannot load', async () => {
    let fail = true;
    const { api } = fakeServer({
      [`GET ${base}`]: () =>
        fail
          ? Response.json({ code: 'internal', message: 'x' }, { status: 500 })
          : Response.json({ items: [entry(1)] }),
    });
    render(<App api={api} />);
    expect(await screen.findByRole('status')).toBeTruthy();
    await screen.findByText(/Could not load the access log/);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('table')).toBeTruthy();
  });
});
