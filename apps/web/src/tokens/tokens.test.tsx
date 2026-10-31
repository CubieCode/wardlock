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
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiTokensPage } from './tokens-page.js';

// Synthetic: shaped like a real token, valid nowhere.
const secret = 'wl_k7m2q6xw4a_TESTonlyTESTonlyTESTonlyTESTonlyTESTonlyTESTonl';
const day = 24 * 3600_000;

const token = (over: Record<string, unknown> = {}) => ({
  id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e20',
  publicId: 'k7m2q6xw4a',
  name: 'CI deploys',
  scopes: ['deploy'],
  createdAt: Date.UTC(2026, 9, 1),
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
  };
}

const list =
  (...items: unknown[]): Handler =>
  () =>
    Response.json({ items });

const created = (over: Record<string, unknown> = {}) =>
  Response.json(
    { token: secret, apiToken: token({ name: 'New token', ...over }) },
    { status: 201 },
  );

const fail = (status: number) => () =>
  Response.json({ code: 'internal', message: 'Boom.' }, { status });

const row = (name: string) =>
  screen.getByRole('cell', { name: new RegExp(name) }).closest('tr')!;

async function openCreate() {
  const button = await screen.findByRole('button', { name: 'Create token' });
  await waitFor(() => expect(button).toHaveProperty('disabled', false));
  fireEvent.click(button);
  await screen.findByRole('heading', { name: 'Create an API token' });
}

const fillName = (name: string) =>
  fireEvent.change(screen.getByRole('textbox', { name: /^Name/ }), {
    target: { value: name },
  });

const submit = () =>
  fireEvent.click(
    within(screen.getByRole('dialog')).getByRole('button', {
      name: 'Create token',
    }),
  );

afterEach(() => {
  cleanup();
});

describe('the list', () => {
  it('says so while loading, then when there are no tokens', async () => {
    const server = fakeServer({ 'GET /api/v0/tokens': list() });
    render(<ApiTokensPage api={server.api} />);
    screen.getByRole('status');
    await screen.findByRole('heading', { name: 'No API tokens yet' });
    expect(document.title).toContain('API tokens');
  });

  it('shows each token, newest first as the server sends them', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(
        token({
          name: 'Used',
          publicId: 'aaaaaaaaaa',
          id: 'a',
          scopes: ['deploy', 'manage'],
          expiresAt: Date.UTC(2099, 0, 15),
          lastUsedAt: Date.now() - 2 * day,
        }),
        token({ name: 'Idle', publicId: 'bbbbbbbbbb', id: 'b' }),
      ),
    });
    render(<ApiTokensPage api={server.api} />);
    await screen.findByRole('cell', { name: /Used/ });
    const names = screen
      .getAllByRole('row')
      .slice(1)
      .map((r) => within(r).getAllByRole('cell')[0]!.textContent);
    expect(names).toEqual(['Usedaaaaaaaaaa', 'Idlebbbbbbbbbb']);
    const used = within(row('Used'));
    used.getByRole('cell', { name: 'Deploy, Manage' });
    used.getByRole('cell', { name: '1 Oct 2026' });
    used.getByRole('cell', { name: '15 Jan 2099' });
    used.getByRole('cell', { name: '2 days ago' });
    const idle = within(row('Idle'));
    idle.getByRole('cell', { name: 'Deploy' });
    expect(idle.getAllByRole('cell', { name: 'Never' })).toHaveLength(2);
    expect(screen.queryByText('Expires soon')).toBeNull();
  });

  it('marks a token expiring within 7 days, and only that one', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(
        token({ id: 'a', name: 'Soon', expiresAt: Date.now() + 6 * day }),
        token({ id: 'b', name: 'Later', expiresAt: Date.now() + 8 * day }),
      ),
    });
    render(<ApiTokensPage api={server.api} />);
    await screen.findByRole('cell', { name: /Soon/ });
    within(row('Soon')).getByText('Expires soon');
    expect(within(row('Later')).queryByText('Expires soon')).toBeNull();
  });

  it('pages through older tokens with the cursor', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': (url) =>
        url.searchParams.get('cursor') === 'next'
          ? Response.json({ items: [token({ id: 'b', name: 'Older' })] })
          : Response.json({
              items: [token({ id: 'a', name: 'Newest' })],
              nextCursor: 'next',
            }),
    });
    render(<ApiTokensPage api={server.api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show older tokens' }),
    );
    await screen.findByRole('cell', { name: /Older/ });
    screen.getByRole('cell', { name: /Newest/ });
    expect(screen.queryByRole('button', { name: 'Show older tokens' })).toBe(
      null,
    );
  });

  it('reports a failed page and keeps what it has', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': (url) =>
        url.searchParams.has('cursor')
          ? fail(500)()
          : Response.json({ items: [token()], nextCursor: 'next' }),
    });
    render(<ApiTokensPage api={server.api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show older tokens' }),
    );
    await screen.findByText('Could not load more tokens. Try again.');
    screen.getByRole('cell', { name: /CI deploys/ });
  });

  it('offers another try when the list fails to load', async () => {
    let attempts = 0;
    const server = fakeServer({
      'GET /api/v0/tokens': () => {
        attempts += 1;
        return attempts === 1
          ? fail(500)()
          : Response.json({ items: [token()] });
      },
    });
    render(<ApiTokensPage api={server.api} />);
    await screen.findByText(/Could not load your API tokens/);
    expect(screen.getByRole('button', { name: 'Create token' })).toHaveProperty(
      'disabled',
      true,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await screen.findByRole('cell', { name: /CI deploys/ });
  });
});

describe('creating a token', () => {
  it('defaults to Deploy only, for 90 days', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () => created(),
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    expect(screen.getByRole('checkbox', { name: /Deploy/ })).toHaveProperty(
      'checked',
      true,
    );
    expect(screen.getByRole('checkbox', { name: /Manage/ })).toHaveProperty(
      'checked',
      false,
    );
    expect(
      (screen.getByRole('combobox', { name: /Expires/ }) as HTMLSelectElement)
        .value,
    ).toBe('90d');
    fillName('  New token ');
    const before = Date.now();
    submit();
    await screen.findByRole('heading', { name: 'Copy your new token' });
    const body = server.bodyOf('POST /api/v0/tokens') as {
      name: string;
      scopes: string[];
      expiresAt: number;
    };
    expect(body.name).toBe('New token');
    expect(body.scopes).toEqual(['deploy']);
    expect(body.expiresAt).toBeGreaterThanOrEqual(before + 90 * day);
    expect(body.expiresAt).toBeLessThanOrEqual(Date.now() + 90 * day);
  });

  it.each([
    ['30d', 30],
    ['365d', 365],
  ])('offers an expiry of %s', async (value, days) => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () => created(),
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    fireEvent.change(screen.getByRole('combobox', { name: /Expires/ }), {
      target: { value },
    });
    submit();
    await screen.findByRole('heading', { name: 'Copy your new token' });
    const { expiresAt } = server.bodyOf('POST /api/v0/tokens') as {
      expiresAt: number;
    };
    expect(Math.round((expiresAt - Date.now()) / day)).toBe(days);
  });

  it('sends no expiry for Never, and both scopes when ticked', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () => created({ scopes: ['deploy', 'manage'] }),
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    fireEvent.click(screen.getByRole('checkbox', { name: /Manage/ }));
    fireEvent.change(screen.getByRole('combobox', { name: /Expires/ }), {
      target: { value: 'never' },
    });
    submit();
    await screen.findByText(/New token · Deploy, Manage · Never expires/);
    expect(server.bodyOf('POST /api/v0/tokens')).toEqual({
      name: 'New token',
      scopes: ['deploy', 'manage'],
    });
  });

  it('expires at the end of a chosen date, which must not be past', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () => created(),
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    fireEvent.change(screen.getByRole('combobox', { name: /Expires/ }), {
      target: { value: 'custom' },
    });
    const date = await screen.findByLabelText(/Expiry date/);
    fireEvent.change(date, { target: { value: '2020-01-01' } });
    submit();
    await screen.findByText('Choose today or a date in the future.');
    expect(server.bodyOf('POST /api/v0/tokens')).toBeUndefined();
    fireEvent.change(date, { target: { value: '2099-03-04' } });
    submit();
    await screen.findByRole('heading', { name: 'Copy your new token' });
    const { expiresAt } = server.bodyOf('POST /api/v0/tokens') as {
      expiresAt: number;
    };
    expect(new Date(expiresAt).getTime()).toBe(
      new Date(2099, 2, 4, 23, 59, 59, 999).getTime(),
    );
  });

  it('asks for a name and a scope before calling the server', async () => {
    const server = fakeServer({ 'GET /api/v0/tokens': list() });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fireEvent.click(screen.getByRole('checkbox', { name: /Deploy/ }));
    submit();
    await screen.findByText(/Enter a name/);
    screen.getByText('Choose at least one thing this token may do.');
    expect(server.bodyOf('POST /api/v0/tokens')).toBeUndefined();
  });

  it('puts the server’s field error on its field', async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () =>
        Response.json(
          {
            code: 'invalid_request',
            message: 'name must be 1 to 100 characters.',
            target: 'name',
          },
          { status: 400 },
        ),
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    submit();
    await screen.findByText('name must be 1 to 100 characters.');
    expect(
      screen
        .getByRole('textbox', { name: /^Name/ })
        .getAttribute('aria-invalid'),
    ).toBe('true');
  });

  it.each([
    ['a server error', () => Promise.resolve(fail(500)()), /went wrong/],
    [
      'no connection',
      () => Promise.reject(new TypeError('offline')),
      /Could not reach the server/,
    ],
  ])('stays open after %s', async (_, answer, message) => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': answer,
    });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    submit();
    await screen.findByText(message);
    screen.getByRole('heading', { name: 'Create an API token' });
  });

  it('cancels without creating anything', async () => {
    const server = fakeServer({ 'GET /api/v0/tokens': list() });
    render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(server.bodyOf('POST /api/v0/tokens')).toBeUndefined();
  });
});

describe('the token shown once', () => {
  const create = async () => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(),
      'POST /api/v0/tokens': () => created(),
    });
    const view = render(<ApiTokensPage api={server.api} />);
    await openCreate();
    fillName('New token');
    submit();
    const field = await screen.findByRole('textbox', { name: 'API token' });
    return { field, view, server };
  };

  it('shows it in full with a copy button, then lists the token', async () => {
    const writeText = vi.fn(() => Promise.resolve());
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    const { field } = await create();
    expect((field as HTMLInputElement).value).toBe(secret);
    screen.getByText(/cannot be shown again/);
    fireEvent.click(screen.getByRole('button', { name: 'Copy api token' }));
    await screen.findByText('API token copied.');
    expect(writeText).toHaveBeenCalledWith(secret);
    screen.getByRole('cell', { name: /New token/ });
    expect(document.title).not.toContain('wl_');
  });

  it('is gone after Done', async () => {
    const { view } = await create();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(view.container.ownerDocument.body.innerHTML).not.toContain(secret);
    screen.getByRole('cell', { name: /New token/ });
    screen.getByText('k7m2q6xw4a');
  });

  it('is gone after leaving the page', async () => {
    const { view, server } = await create();
    view.unmount();
    render(<ApiTokensPage api={server.api} />);
    await screen.findByRole('heading', { name: 'No API tokens yet' });
    expect(document.body.innerHTML).not.toContain(secret);
  });

  it('survives Escape, so it is not lost before being copied', async () => {
    const { field } = await create();
    fireEvent(
      screen.getByRole('dialog'),
      new Event('cancel', { cancelable: true, bubbles: true }),
    );
    expect((field as HTMLInputElement).value).toBe(secret);
    screen.getByRole('button', { name: 'Done' });
  });
});

describe('revoking a token', () => {
  const open = async (revoke: Handler) => {
    const server = fakeServer({
      'GET /api/v0/tokens': list(token()),
      [`DELETE /api/v0/tokens/${token().id}`]: revoke,
    });
    render(<ApiTokensPage api={server.api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Revoke CI deploys' }),
    );
    await screen.findByRole('heading', { name: 'Revoke “CI deploys”?' });
    return server;
  };

  const confirm = () =>
    fireEvent.click(screen.getByRole('button', { name: 'Revoke token' }));

  it('removes the token once confirmed', async () => {
    await open(() => new Response(null, { status: 204 }));
    confirm();
    await screen.findByText('Revoked “CI deploys”. It no longer works.');
    expect(screen.queryByRole('cell', { name: /CI deploys/ })).toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('calls nothing when cancelled', async () => {
    const server = await open(() => new Response(null, { status: 204 }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    screen.getByRole('cell', { name: /CI deploys/ });
    expect(server.calls.some((c) => c.route.startsWith('DELETE'))).toBe(false);
  });

  it('treats a token already gone as revoked', async () => {
    await open(fail(404));
    confirm();
    await screen.findByText('Revoked “CI deploys”. It no longer works.');
    expect(screen.queryByRole('cell', { name: /CI deploys/ })).toBeNull();
  });

  it('keeps the token and says so when it fails', async () => {
    await open(fail(500));
    confirm();
    await screen.findByText(/went wrong/);
    screen.getByRole('heading', { name: 'Revoke “CI deploys”?' });
    screen.getByRole('cell', { name: /CI deploys/, hidden: true });
  });

  it('does not bring a revoked token back from an older page', async () => {
    let answer!: (r: Response) => void;
    const server = fakeServer({
      'GET /api/v0/tokens': (url) =>
        url.searchParams.has('cursor')
          ? new Promise((resolve) => (answer = resolve))
          : Response.json({ items: [token()], nextCursor: 'next' }),
      [`DELETE /api/v0/tokens/${token().id}`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<ApiTokensPage api={server.api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Show older tokens' }),
    );
    await waitFor(() => expect(answer).toBeDefined());
    fireEvent.click(screen.getByRole('button', { name: 'Revoke CI deploys' }));
    await screen.findByRole('heading', { name: 'Revoke “CI deploys”?' });
    fireEvent.click(screen.getByRole('button', { name: 'Revoke token' }));
    await screen.findByText(/It no longer works/);
    answer(Response.json({ items: [token()] }));
    await waitFor(() =>
      expect(screen.queryByRole('cell', { name: /CI deploys/ })).toBeNull(),
    );
  });
});
