// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { StrictMode } from 'react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { App } from './app.js';
import { runEffects } from './test/effects.js';

const owner = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

type Handler = (
  body: unknown,
  request: Request,
) => Response | Promise<Response>;

/** A response the test releases later. */
function deferred() {
  let release!: (response: Response) => void;
  const promise = new Promise<Response>((resolve) => (release = resolve));
  return { promise, release };
}

/** A fake server: `routes["POST /api/v0/setup"]` answers that call. */
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
      if (handler) return handler(body, request);
      if (route === 'GET /api/v0/health')
        return Response.json({ status: 'ok', version: '0.0.0-test' });
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

const unauthenticated = () =>
  Response.json(
    { code: 'unauthenticated', message: 'Sign in.' },
    { status: 401 },
  );
const invalidCode = () =>
  Response.json(
    { code: 'invalid_code', message: 'Invalid code.' },
    { status: 400 },
  );

function at(path: string) {
  window.history.replaceState(null, '', path);
}

const here = () => window.location.pathname + window.location.search;

function type(label: string | RegExp, value: string) {
  fireEvent.change(screen.getByRole('textbox', { name: label }), {
    target: { value },
  });
}

beforeEach(() => at('/'));
afterEach(cleanup);

describe('signed out', () => {
  it('sends a visitor to sign-in and keeps where they were going', async () => {
    at('/somewhere?tab=1');
    const { api } = fakeServer({ 'GET /api/v0/me': unauthenticated });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: 'Sign in to Wardlock',
      }),
    ).toBeTruthy();
    expect(here()).toBe(
      `/sign-in?next=${encodeURIComponent('/somewhere?tab=1')}`,
    );
    await waitFor(() => expect(document.title).toBe('Sign in · Wardlock'));
    expect(screen.queryByRole('navigation')).toBeNull();
  });

  it('explains how to recover when the server is unreachable', async () => {
    let up = false;
    const { api } = fakeServer({
      'GET /api/v0/me': () => {
        if (!up) throw new TypeError('offline');
        return unauthenticated();
      },
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', {
        name: 'Could not reach the server',
      }),
    ).toBeTruthy();
    up = true;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(
      await screen.findByRole('heading', { name: 'Sign in to Wardlock' }),
    ).toBeTruthy();
  });
});

describe('email-code sign-in', () => {
  it('signs in after a wrong code and returns to the requested page', async () => {
    at('/sign-in?next=%2F');
    let signedIn = false;
    const server = fakeServer({
      'GET /api/v0/me': () =>
        signedIn ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/email-code': () => new Response(null, { status: 202 }),
      'POST /api/v0/auth/email-code/verify': (body) => {
        if ((body as { code: string }).code !== '482913') return invalidCode();
        signedIn = true;
        return Response.json(owner);
      },
    });
    render(<App api={server.api} />);

    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    expect(await screen.findByText('Enter your email address.')).toBeTruthy();
    expect(server.count('POST /api/v0/auth/email-code')).toBe(0);

    type('Email address', ' owner@example.test ');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    const heading = await screen.findByRole('heading', {
      name: 'Check your email',
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(server.calls.at(-1)?.body).toEqual({ email: 'owner@example.test' });

    type('Sign-in code', '12');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(
      await screen.findByText('Enter the 6-digit code from the email.'),
    ).toBeTruthy();

    type('Sign-in code', '000000');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    const input = await screen.findByRole('textbox', {
      name: 'Sign-in code',
      description: /That code is not right, or it has expired/,
    });
    expect(input.getAttribute('aria-invalid')).toBe('true');

    type('Sign-in code', '482 913');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
    expect(server.calls.at(-2)?.body).toEqual({
      email: 'owner@example.test',
      code: '482913',
    });
    expect(here()).toBe('/');
    expect(screen.getByText('owner@example.test')).toBeTruthy();
    expect(
      screen
        .getByRole('link', { name: 'Overview' })
        .getAttribute('aria-current'),
    ).toBe('page');
  });

  it('sends a new code and lets the owner change address', async () => {
    at('/sign-in');
    const server = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/auth/email-code': () => new Response(null, { status: 202 }),
    });
    render(<App api={server.api} />);
    await screen.findByLabelText('Email address');
    type('Email address', 'owner@example.test');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Send a new code' }),
    );
    expect(
      await screen.findByText(
        'We sent a new code. Earlier codes no longer work.',
      ),
    ).toBeTruthy();
    expect(server.count('POST /api/v0/auth/email-code')).toBe(2);

    fireEvent.click(
      screen.getByRole('button', { name: 'Use a different email' }),
    );
    expect(
      await screen.findByRole('heading', { name: 'Sign in to Wardlock' }),
    ).toBeTruthy();
  });

  it('says how long to wait when rate-limited', async () => {
    at('/sign-in');
    const { api } = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/auth/email-code': () =>
        Response.json(
          { code: 'rate_limited', message: 'Slow down.' },
          { status: 429, headers: { 'Retry-After': '125' } },
        ),
    });
    render(<App api={api} />);
    await screen.findByLabelText('Email address');
    type('Email address', 'owner@example.test');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Too many attempts. Try again in 3 minutes.',
    );
  });

  it('never follows a return path to another origin', async () => {
    at('/sign-in?next=%2F%2Fevil.example');
    let signedIn = false;
    const { api } = fakeServer({
      'GET /api/v0/me': () =>
        signedIn ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/email-code': () => new Response(null, { status: 202 }),
      'POST /api/v0/auth/email-code/verify': () => {
        signedIn = true;
        return Response.json(owner);
      },
    });
    render(<App api={api} />);
    await screen.findByLabelText('Email address');
    type('Email address', 'owner@example.test');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    await screen.findByRole('heading', { name: 'Check your email' });
    type('Sign-in code', '482913');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    expect(here()).toBe('/');
  });
});

describe('signed in', () => {
  it('signs out and says so', async () => {
    let signedIn = true;
    const server = fakeServer({
      'GET /api/v0/me': () =>
        signedIn ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/sign-out': () => {
        signedIn = false;
        return new Response(null, { status: 204 });
      },
    });
    render(<App api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect(await screen.findByText('You have signed out.')).toBeTruthy();
    expect(here()).toBe('/sign-in');
    expect(server.count('POST /api/v0/auth/sign-out')).toBe(1);
  });

  it('keeps the owner signed in when sign-out fails', async () => {
    const { api } = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
      'POST /api/v0/auth/sign-out': () =>
        Response.json({ code: 'internal', message: 'Oops.' }, { status: 500 }),
    });
    render(<App api={api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Could not sign out. Try again.',
    );
    expect(
      screen.getByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
  });

  it('returns to sign-in when the session ends, then back to the page', async () => {
    at('/missing');
    let valid = true;
    const { api } = fakeServer({
      'GET /api/v0/me': () =>
        valid ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/email-code': () => new Response(null, { status: 202 }),
      'POST /api/v0/auth/email-code/verify': () => {
        valid = true;
        return Response.json(owner);
      },
    });
    const app = <App api={api} />;
    const { rerender } = render(app);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeTruthy();
    runEffects(rerender, app);

    valid = false;
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    expect(
      await screen.findByText(
        'Your session has ended. Sign in again to continue.',
      ),
    ).toBeTruthy();
    expect(here()).toBe('/sign-in?next=%2Fmissing');

    type('Email address', 'owner@example.test');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    await screen.findByRole('heading', { name: 'Check your email' });
    type('Sign-in code', '482913');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Page not found' }),
    ).toBeTruthy();
    expect(here()).toBe('/missing');
  });

  it('ignores a 401 that started before the owner signed in again', async () => {
    let held: ReturnType<typeof deferred> | null = null;
    let signedIn = true;
    const server = fakeServer({
      'GET /api/v0/me': () =>
        held
          ? held.promise
          : signedIn
            ? Response.json(owner)
            : unauthenticated(),
      'POST /api/v0/auth/sign-out': () => {
        signedIn = false;
        return new Response(null, { status: 204 });
      },
      'POST /api/v0/auth/email-code': () => new Response(null, { status: 202 }),
      'POST /api/v0/auth/email-code/verify': () => {
        signedIn = true;
        return Response.json(owner);
      },
    });
    const app = <App api={server.api} />;
    const { rerender } = render(app);
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    runEffects(rerender, app);

    // A recheck goes out and stalls.
    const stale = deferred();
    held = stale;
    const checks = server.count('GET /api/v0/me');
    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    await waitFor(() =>
      expect(server.count('GET /api/v0/me')).toBe(checks + 1),
    );
    held = null;

    // Meanwhile the owner signs out and back in.
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    await screen.findByText('You have signed out.');
    type('Email address', 'owner@example.test');
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    await screen.findByRole('heading', { name: 'Check your email' });
    type('Sign-in code', '482913');
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    await screen.findByRole('heading', { level: 1, name: 'Overview' });

    // The stale check now answers 401: the new session stays.
    await act(async () => {
      stale.release(unauthenticated());
      await stale.promise;
    });
    expect(
      screen.getByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
    expect(screen.queryByText(/Your session has ended/)).toBeNull();
    expect(here()).toBe('/');
  });

  it('checks the session once when the tab comes back', async () => {
    const server = fakeServer({ 'GET /api/v0/me': () => Response.json(owner) });
    const app = <App api={server.api} />;
    const { rerender } = render(app);
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    runEffects(rerender, app);
    const before = server.count('GET /api/v0/me');
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
      window.dispatchEvent(new Event('focus'));
    });
    await act(async () => {});
    expect(server.count('GET /api/v0/me')).toBe(before + 1);
  });

  it('redirects away from sign-in', async () => {
    at('/sign-in?next=%2Felsewhere');
    const { api } = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { level: 1, name: 'Page not found' });
    expect(here()).toBe('/elsewhere');
  });
});

describe('one-time links', () => {
  it('redeems a sign-in link once and drops the code from the address', async () => {
    at('/sign-in?code=EXAMPLEsignInCode');
    let signedIn = false;
    const server = fakeServer({
      'GET /api/v0/me': () =>
        signedIn ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/sign-in-link': (body) => {
        expect(body).toEqual({ code: 'EXAMPLEsignInCode' });
        signedIn = true;
        return Response.json(owner);
      },
    });
    render(
      <StrictMode>
        <App api={server.api} />
      </StrictMode>,
    );
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
    expect(server.count('POST /api/v0/auth/sign-in-link')).toBe(1);
    expect(here()).toBe('/');
  });

  it('drops the code from the address before redeeming it', async () => {
    at('/sign-in?code=EXAMPLEheldCode&next=%2Fsomewhere');
    const held = deferred();
    const server = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/auth/sign-in-link': () => held.promise,
    });
    render(<App api={server.api} />);
    await screen.findByText('Checking your sign-in link…');
    await waitFor(() =>
      expect(server.count('POST /api/v0/auth/sign-in-link')).toBe(1),
    );
    expect(here()).toBe('/sign-in?next=%2Fsomewhere');
    expect(window.location.href).not.toContain('EXAMPLEheldCode');
    await act(async () => {
      held.release(invalidCode());
      await held.promise;
    });
    await screen.findByRole('heading', {
      name: 'This sign-in link no longer works',
    });
    // A failed link keeps where the owner was going.
    expect(here()).toBe('/sign-in?next=%2Fsomewhere');
  });

  it('offers email-code sign-in when a link no longer works', async () => {
    at('/sign-in?code=EXAMPLEusedCode');
    const { api } = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/auth/sign-in-link': invalidCode,
    });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', {
        name: 'This sign-in link no longer works',
      }),
    ).toBeTruthy();
    expect(here()).toBe('/sign-in');
    fireEvent.click(
      screen.getByRole('button', { name: 'Sign in with an email code' }),
    );
    expect(
      await screen.findByRole('heading', { name: 'Sign in to Wardlock' }),
    ).toBeTruthy();
  });

  it('creates the first owner from the setup URL', async () => {
    at('/setup?code=EXAMPLEsetupCode');
    const server = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/setup': () => Response.json(owner, { status: 201 }),
    });
    render(<App api={server.api} />);
    await screen.findByRole('heading', { name: 'Create the first owner' });
    await waitFor(() => expect(here()).toBe('/setup'));
    type('Email address', 'owner@example.test');
    fireEvent.click(
      screen.getByRole('button', { name: 'Create owner and sign in' }),
    );
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
    expect(server.calls.at(-2)?.body).toEqual({
      code: 'EXAMPLEsetupCode',
      email: 'owner@example.test',
    });
  });

  it('explains what to do with a used setup URL', async () => {
    at('/setup?code=EXAMPLEusedCode');
    const { api } = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/setup': invalidCode,
    });
    render(<App api={api} />);
    await screen.findByLabelText('Email address');
    type('Email address', 'owner@example.test');
    fireEvent.click(
      screen.getByRole('button', { name: 'Create owner and sign in' }),
    );
    expect(
      await screen.findByRole('heading', {
        name: 'This setup link no longer works',
      }),
    ).toBeTruthy();
  });

  it('sends a signed-in owner home from a setup URL', async () => {
    at('/setup?code=EXAMPLEsetupCode');
    const { api } = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { level: 1, name: 'Overview' });
    expect(here()).toBe('/');
  });

  it('explains where the setup URL comes from', async () => {
    at('/setup');
    const { api } = fakeServer({ 'GET /api/v0/me': unauthenticated });
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'Set up Wardlock' }),
    ).toBeTruthy();
  });
});
