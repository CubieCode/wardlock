// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app.js';

const owner = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

const laptop = {
  id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e40',
  name: 'Laptop',
  createdAt: Date.UTC(2026, 9, 1),
  lastUsedAt: Date.UTC(2026, 9, 5),
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
    last: (route: string) => calls.findLast((c) => c.route === route),
  };
}

const unauthenticated = () =>
  Response.json(
    { code: 'unauthenticated', message: 'Sign in.' },
    { status: 401 },
  );

const bytes = (...b: number[]) => new Uint8Array(b).buffer;

/** Stands in for the browser's PublicKeyCredential. */
class FakeCredential {
  id = 'AQI';
  rawId = bytes(1, 2);
  type = 'public-key';
  authenticatorAttachment = 'platform';
  constructor(public response: Record<string, unknown>) {}
  getClientExtensionResults() {
    return {};
  }
}

const assertion = () =>
  new FakeCredential({
    clientDataJSON: bytes(3),
    authenticatorData: bytes(4),
    signature: bytes(5),
    userHandle: bytes(6),
  });

const attestation = () =>
  new FakeCredential({
    clientDataJSON: bytes(3),
    attestationObject: bytes(7),
    getTransports: () => ['internal'],
  });

const credentials = { get: vi.fn(), create: vi.fn() };

function at(path: string) {
  window.history.replaceState(null, '', path);
}

beforeEach(() => {
  vi.stubGlobal('PublicKeyCredential', FakeCredential);
  Object.defineProperty(navigator, 'credentials', {
    value: credentials,
    configurable: true,
  });
  credentials.get.mockReset();
  credentials.create.mockReset();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const challenge = {
  ceremonyId: 'ceremony-1',
  options: { challenge: 'AwQF', rpId: 'wardlock.test' },
};

describe('passkey sign-in', () => {
  it('signs in and returns to the requested page', async () => {
    at('/sign-in?next=%2Fpasskeys');
    let signedIn = false;
    const server = fakeServer({
      'GET /api/v0/me': () =>
        signedIn ? Response.json(owner) : unauthenticated(),
      'POST /api/v0/auth/passkey': () => Response.json(challenge),
      'POST /api/v0/auth/passkey/verify': () => {
        signedIn = true;
        return Response.json(owner);
      },
      'GET /api/v0/passkeys': () => Response.json({ items: [laptop] }),
    });
    credentials.get.mockResolvedValue(assertion());
    render(<App api={server.api} />);

    fireEvent.click(
      await screen.findByRole('button', { name: 'Sign in with a passkey' }),
    );
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Passkeys' }),
    ).toBeTruthy();
    const publicKey = credentials.get.mock.calls[0]![0].publicKey;
    expect(Array.from(new Uint8Array(publicKey.challenge))).toEqual([3, 4, 5]);
    expect(publicKey.rpId).toBe('wardlock.test');
    expect(server.last('POST /api/v0/auth/passkey/verify')?.body).toEqual({
      ceremonyId: 'ceremony-1',
      credential: {
        id: 'AQI',
        rawId: 'AQI',
        type: 'public-key',
        authenticatorAttachment: 'platform',
        clientExtensionResults: {},
        response: {
          clientDataJSON: 'Aw',
          authenticatorData: 'BA',
          signature: 'BQ',
          userHandle: 'Bg',
        },
      },
    });
    expect(window.location.pathname).toBe('/passkeys');
  });

  it('explains a cancelled prompt and a refused passkey', async () => {
    at('/sign-in');
    const server = fakeServer({
      'GET /api/v0/me': unauthenticated,
      'POST /api/v0/auth/passkey': () => Response.json(challenge),
      'POST /api/v0/auth/passkey/verify': () =>
        Response.json(
          { code: 'invalid_passkey', message: 'Not accepted.' },
          { status: 400 },
        ),
    });
    render(<App api={server.api} />);
    const button = await screen.findByRole('button', {
      name: 'Sign in with a passkey',
    });

    credentials.get.mockRejectedValueOnce(
      new DOMException('cancelled', 'NotAllowedError'),
    );
    fireEvent.click(button);
    expect(
      await screen.findByText(/Passkey sign-in was cancelled or timed out/),
    ).toBeTruthy();
    expect(server.last('POST /api/v0/auth/passkey/verify')).toBeUndefined();

    credentials.get.mockResolvedValueOnce(assertion());
    fireEvent.click(button);
    expect(
      await screen.findByText(/That passkey was not accepted/),
    ).toBeTruthy();
    expect(window.location.pathname).toBe('/sign-in');
  });

  it('is not offered when the browser has no passkey support', async () => {
    vi.stubGlobal('PublicKeyCredential', undefined);
    at('/sign-in');
    render(<App api={fakeServer({ 'GET /api/v0/me': unauthenticated }).api} />);
    await screen.findByRole('button', { name: 'Send code' });
    expect(
      screen.queryByRole('button', { name: 'Sign in with a passkey' }),
    ).toBeNull();
  });
});

describe('passkeys page', () => {
  it('lists, adds and removes passkeys', async () => {
    at('/passkeys');
    const added = {
      id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e41',
      name: 'Chrome on macOS',
      createdAt: Date.UTC(2026, 9, 6),
    };
    const server = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
      'GET /api/v0/passkeys': () => Response.json({ items: [laptop] }),
      'POST /api/v0/passkeys/registration': () =>
        Response.json({
          ceremonyId: 'ceremony-2',
          options: {
            challenge: 'AwQF',
            rp: { id: 'wardlock.test', name: 'Wardlock' },
            user: {
              id: 'AQI',
              name: 'owner@example.test',
              displayName: 'owner@example.test',
            },
            pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
            excludeCredentials: [{ type: 'public-key', id: 'AQI' }],
          },
        }),
      'POST /api/v0/passkeys': () => Response.json(added, { status: 201 }),
      [`DELETE /api/v0/passkeys/${laptop.id}`]: () =>
        new Response(null, { status: 204 }),
    });
    credentials.create.mockResolvedValue(attestation());
    render(<App api={server.api} />);

    expect(await screen.findByText('Laptop')).toBeTruthy();
    expect(
      screen.getByText('Added 1 Oct 2026 · Last used 5 Oct 2026'),
    ).toBeTruthy();
    expect(
      screen
        .getByRole('link', { name: 'Passkeys' })
        .getAttribute('aria-current'),
    ).toBe('page');

    fireEvent.click(screen.getByRole('button', { name: 'Add a passkey' }));
    expect(await screen.findByText('Chrome on macOS')).toBeTruthy();
    expect(screen.getByText(/Passkey added as “Chrome on macOS”/)).toBeTruthy();
    expect(screen.getByText(/Not used yet/)).toBeTruthy();
    const publicKey = credentials.create.mock.calls[0]![0].publicKey;
    expect(
      Array.from(new Uint8Array(publicKey.excludeCredentials[0].id)),
    ).toEqual([1, 2]);
    const body = server.last('POST /api/v0/passkeys')?.body as {
      ceremonyId: string;
      name: string;
      credential: { response: unknown };
    };
    expect(body.ceremonyId).toBe('ceremony-2');
    expect(body.credential.response).toEqual({
      clientDataJSON: 'Aw',
      attestationObject: 'Bw',
      transports: ['internal'],
    });

    fireEvent.click(screen.getByRole('button', { name: 'Remove Laptop' }));
    expect(screen.getByText(/Remove the passkey “Laptop”\?/)).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Cancel' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove passkey' }));
    expect(await screen.findByText('Passkey “Laptop” removed.')).toBeTruthy();
    expect(screen.queryByText('Laptop')).toBeNull();
    expect(screen.getByText('Chrome on macOS')).toBeTruthy();
  });

  it('shows the empty state and the limit', async () => {
    at('/passkeys');
    const server = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
      'GET /api/v0/passkeys': () => Response.json({ items: [] }),
      'POST /api/v0/passkeys/registration': () =>
        Response.json(
          {
            code: 'passkey_limit',
            message:
              'You have 20 passkeys, the most allowed. Remove one first.',
          },
          { status: 409 },
        ),
    });
    render(<App api={server.api} />);
    expect(await screen.findByText(/You have no passkeys yet/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Add a passkey' }));
    expect(await screen.findByText(/the most allowed/)).toBeTruthy();
    expect(credentials.create).not.toHaveBeenCalled();
  });

  it('cancelling a removal keeps the passkey', async () => {
    at('/passkeys');
    const server = fakeServer({
      'GET /api/v0/me': () => Response.json(owner),
      'GET /api/v0/passkeys': () => Response.json({ items: [laptop] }),
    });
    render(<App api={server.api} />);
    fireEvent.click(
      await screen.findByRole('button', { name: 'Remove Laptop' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Remove Laptop' }),
    );
    expect(server.calls.some((c) => c.route.startsWith('DELETE'))).toBe(false);
  });
});
