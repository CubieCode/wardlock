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
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../app.js';
import { normaliseUserCode } from './cli-login.js';

const owner = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

type Handler = (body: unknown) => Response | Promise<Response>;

function fakeServer(routes: Record<string, Handler>) {
  const calls: { route: string; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const route = `${request.method} ${new URL(request.url).pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route, body });
      const handler = routes[route];
      if (handler) return handler(body);
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return {
    api,
    calls,
    count: (route: string) => calls.filter((c) => c.route === route).length,
  };
}

const signedIn = () => Response.json(owner);
const noContent = () => new Response(null, { status: 204 });
const invalidCode = () =>
  Response.json(
    { code: 'invalid_code', message: 'Invalid code.' },
    { status: 400 },
  );

function at(path: string) {
  window.history.replaceState(null, '', path);
}

const here = () => window.location.pathname + window.location.search;

afterEach(cleanup);

describe('normaliseUserCode', () => {
  it('accepts the code as typed and rejects anything else', () => {
    expect(normaliseUserCode('BDFH-KMPR')).toBe('BDFH-KMPR');
    expect(normaliseUserCode(' bdfh kmpr ')).toBe('BDFH-KMPR');
    expect(normaliseUserCode('bdfhkmpr')).toBe('BDFH-KMPR');
    expect(normaliseUserCode('BDFH-KMP')).toBeNull();
    expect(normaliseUserCode('ADFH-KMPR')).toBeNull();
    expect(normaliseUserCode('BDF1-KMPR')).toBeNull();
    expect(normaliseUserCode('')).toBeNull();
  });
});

describe('/cli-login', () => {
  it('sends a signed-out owner to sign in and back with the code', async () => {
    at('/cli-login?code=BDFH-KMPR');
    const { api } = fakeServer({
      'GET /api/v0/me': () =>
        Response.json(
          { code: 'unauthenticated', message: 'x' },
          { status: 401 },
        ),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { name: 'Sign in to Wardlock' });
    expect(here()).toBe(
      `/sign-in?next=${encodeURIComponent('/cli-login?code=BDFH-KMPR')}`,
    );
  });

  it('shows the code and approves with the chosen scopes', async () => {
    at('/cli-login?code=bdfh-kmpr');
    const server = fakeServer({
      'GET /api/v0/me': signedIn,
      'POST /api/v0/cli-login/approve': noContent,
    });
    render(<App api={server.api} />);
    await screen.findByRole('heading', { level: 1, name: 'Approve CLI login' });
    await waitFor(() =>
      expect(document.title).toBe('Approve CLI login · Wardlock'),
    );
    expect(screen.getByText('BDFH-KMPR')).toBeTruthy();
    expect(screen.getByText('owner@example.test')).toBeTruthy();
    expect(screen.queryByRole('navigation')).toBeNull();

    const deploy = screen.getByRole('checkbox', { name: /Deploy/ });
    const manage = screen.getByRole('checkbox', { name: /Manage/ });
    expect((deploy as HTMLInputElement).checked).toBe(true);
    expect((manage as HTMLInputElement).checked).toBe(false);

    // Nothing chosen: say so, send nothing.
    fireEvent.click(deploy);
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    expect(
      await screen.findByText(
        'Choose at least one thing this computer may do.',
      ),
    ).toBeTruthy();
    expect(server.count('POST /api/v0/cli-login/approve')).toBe(0);

    fireEvent.click(manage);
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    const heading = await screen.findByRole('heading', {
      name: 'CLI login approved',
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(server.calls.at(-1)?.body).toEqual({
      userCode: 'BDFH-KMPR',
      scopes: ['manage'],
    });
    expect(screen.getByText(/Return to your terminal/)).toBeTruthy();
  });

  it('denies a login', async () => {
    at('/cli-login?code=BDFH-KMPR');
    const server = fakeServer({
      'GET /api/v0/me': signedIn,
      'POST /api/v0/cli-login/deny': noContent,
    });
    render(<App api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Deny' }));
    await screen.findByRole('heading', { name: 'CLI login denied' });
    expect(server.calls.at(-1)).toEqual({
      route: 'POST /api/v0/cli-login/deny',
      body: { userCode: 'BDFH-KMPR' },
    });
  });

  it('asks for the code again when it is wrong or expired', async () => {
    at('/cli-login?code=BDFH-KMPR');
    let attempts = 0;
    const server = fakeServer({
      'GET /api/v0/me': signedIn,
      'POST /api/v0/cli-login/approve': () =>
        attempts++ === 0 ? invalidCode() : noContent(),
    });
    render(<App api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));
    await screen.findByRole('heading', { name: 'Log in to the Wardlock CLI' });
    expect(
      screen.getByText(/That code is wrong, has expired or was already used/),
    ).toBeTruthy();
    expect(here()).toBe('/cli-login');

    const field = screen.getByRole('textbox', {
      name: 'Code from your terminal',
    });
    fireEvent.change(field, { target: { value: 'nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(
      await screen.findByText(/Enter the 8-letter code from your terminal/),
    ).toBeTruthy();

    fireEvent.change(field, { target: { value: 'gjkl mnpq' } });
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    await screen.findByText('GJKL-MNPQ');
    expect(here()).toBe('/cli-login?code=GJKL-MNPQ');
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    await screen.findByRole('heading', { name: 'CLI login approved' });
    expect(server.calls.at(-1)?.body).toEqual({
      userCode: 'GJKL-MNPQ',
      scopes: ['deploy'],
    });
  });

  it('explains a failure and keeps the page', async () => {
    at('/cli-login?code=BDFH-KMPR');
    const server = fakeServer({
      'GET /api/v0/me': signedIn,
      'POST /api/v0/cli-login/approve': () =>
        Response.json(
          { code: 'rate_limited', message: 'x' },
          { status: 429, headers: { 'Retry-After': '120' } },
        ),
    });
    render(<App api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));
    expect(
      await screen.findByText('Too many attempts. Try again in 2 minutes.'),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Approve' })).toBeTruthy();
  });
});
