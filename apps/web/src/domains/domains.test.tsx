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
import type { Site } from '../sites/format.js';
import type { Domain } from './format.js';

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
const minute = 60_000;

const docs: Site = {
  id: '01928c5e-7a1b-7c3d-9e4f-000000000001',
  name: 'docs',
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now - 48 * 60 * minute,
};

function domain(
  hostname: string,
  status: Domain['status'],
  extra: Partial<Domain> = {},
): Domain {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${hostname.length.toString().padStart(12, '0')}`,
    hostname,
    status,
    routingRecord: {
      type: 'CNAME',
      name: hostname,
      value: 'docs.sites.example.test',
    },
    verificationRecord: {
      type: 'TXT',
      name: `_wardlock.${hostname}`,
      value: `wardlock-verify=SYNTHETICtoken${hostname.length}`,
    },
    createdAt: now - 10 * minute,
    lastCheckedAt: now - 3 * minute,
    nextCheckAt: now + 5 * minute,
    ...extra,
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
const sitePage = (domains: Domain[]): Record<string, Handler> => ({
  [`GET ${base}`]: () => Response.json(docs),
  [`GET ${base}/deploys`]: () => Response.json({ items: [] }),
  [`GET ${base}/policies`]: () => Response.json({ items: [] }),
  [`GET ${base}/links`]: () => Response.json({ items: [] }),
  [`GET ${base}/domains`]: () => Response.json({ items: domains }),
});

const card = () => screen.getByRole('region', { name: 'Custom domains' });
const row = (hostname: string) =>
  within(card()).getByRole('listitem', { name: hostname });

beforeEach(() => window.history.replaceState(null, '', '/sites/docs'));
afterEach(cleanup);

describe('custom domains on the site page', () => {
  it('says what a custom domain is when there is none', async () => {
    const { api } = fakeServer(sitePage([]));
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { name: 'No custom domains yet' }),
    ).toBeTruthy();
    expect(card().textContent).toContain('docs.sites.example.test');
  });

  it('shows each status in words with its last check and records', async () => {
    const { api } = fakeServer(
      sitePage([
        domain('a.example.test', 'active', {
          verifiedAt: now - 60 * minute,
        }),
        domain('p.example.test', 'pending', {
          failureReason: 'no_record',
          failureDetail: 'No CNAME or TXT record found for p.example.test.',
          failingSince: now - 10 * minute,
        }),
        domain('l.example.test', 'lapsed', {
          failureReason: 'cname_elsewhere',
          failureDetail: 'l.example.test points to elsewhere.example.test.',
        }),
      ]),
    );
    render(<App api={api} />);
    await screen.findByRole('list', { name: 'Custom domains' });

    const active = row('a.example.test');
    expect(within(active).getByText('Active').className).toContain(
      'wl-badge-success',
    );
    expect(active.textContent).toContain('Last checked 3 minutes ago');
    expect(active.textContent).toContain('Next check in 5 minutes');
    // An active domain keeps its records behind a disclosure.
    expect(within(active).getByText('DNS records').tagName).toBe('SUMMARY');

    const pending = row('p.example.test');
    expect(within(pending).getByText('Pending')).toBeTruthy();
    expect(pending.textContent).toContain('No DNS record found yet.');
    expect(pending.textContent).toContain(
      'No CNAME or TXT record found for p.example.test.',
    );
    expect(pending.textContent).toContain('Removed if still pending on');
    const cname = within(pending).getByRole('group', { name: 'CNAME record' });
    expect(
      (within(cname).getByLabelText('Name') as HTMLInputElement).value,
    ).toBe('p.example.test');
    expect(
      (within(cname).getByLabelText('Target') as HTMLInputElement).value,
    ).toBe('docs.sites.example.test');
    const txt = within(pending).getByRole('group', { name: 'TXT record' });
    expect(
      (within(txt).getByLabelText('Value') as HTMLInputElement).value,
    ).toBe('wardlock-verify=SYNTHETICtoken14');
    expect(pending.textContent).toContain('apex domain');

    const lapsed = row('l.example.test');
    expect(within(lapsed).getByText('Lapsed')).toBeTruthy();
    expect(lapsed.textContent).toContain(
      'The CNAME record points somewhere else.',
    );
  });

  it('adds a hostname and shows its records', async () => {
    const added = domain('new.example.test', 'pending', {
      lastCheckedAt: now,
      failureReason: 'dns_error',
      failureDetail: 'The lookup timed out.',
    });
    const { api, calls } = fakeServer({
      ...sitePage([]),
      [`POST ${base}/domains`]: () => Response.json(added, { status: 201 }),
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { name: 'No custom domains yet' });
    fireEvent.change(screen.getByLabelText('Hostname'), {
      target: { value: ' New.Example.test ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add domain' }));
    await screen.findByRole('listitem', { name: 'new.example.test' });
    expect(calls.find((c) => c.route === `POST ${base}/domains`)?.body).toEqual(
      { hostname: 'New.Example.test' },
    );
    expect(card().textContent).toContain('Added new.example.test.');
    expect(row('new.example.test').textContent).toContain(
      'The DNS lookup failed.',
    );
    expect((screen.getByLabelText('Hostname') as HTMLInputElement).value).toBe(
      '',
    );
  });

  it('puts domain_taken and invalid_hostname on the field', async () => {
    let answer: Response = Response.json(
      {
        code: 'domain_taken',
        message: 'This hostname is already attached to a site on this server.',
        target: 'hostname',
      },
      { status: 409 },
    );
    const { api } = fakeServer({
      ...sitePage([]),
      [`POST ${base}/domains`]: () => answer,
    });
    render(<App api={api} />);
    await screen.findByRole('heading', { name: 'No custom domains yet' });
    const field = screen.getByLabelText('Hostname');

    fireEvent.click(screen.getByRole('button', { name: 'Add domain' }));
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Enter a hostname',
    );

    fireEvent.change(field, { target: { value: 'taken.example.test' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add domain' }));
    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toContain(
        'already attached to a site on this server',
      ),
    );
    expect(field.getAttribute('aria-invalid')).toBe('true');

    answer = Response.json(
      {
        code: 'invalid_hostname',
        message: 'IP addresses cannot be custom domains.',
        target: 'hostname',
      },
      { status: 400 },
    );
    fireEvent.change(field, { target: { value: '192.0.2.1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add domain' }));
    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toBe(
        'IP addresses cannot be custom domains.',
      ),
    );
  });

  it('checks now and shows the new result', async () => {
    const before = domain('p.example.test', 'pending', {
      failureReason: 'no_record',
      failureDetail: 'No CNAME or TXT record found for p.example.test.',
    });
    const after = domain('p.example.test', 'active', {
      lastCheckedAt: now,
      verifiedAt: now,
    });
    const { api, calls } = fakeServer({
      ...sitePage([before]),
      [`POST ${base}/domains/p.example.test/verify`]: () =>
        Response.json(after),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Check p.example.test now' }),
    );
    await waitFor(() =>
      expect(within(row('p.example.test')).getByText('Active')).toBeTruthy(),
    );
    expect(row('p.example.test').textContent).toContain(
      'Checked: the record checks out.',
    );
    expect(row('p.example.test').textContent).not.toContain(
      'No DNS record found yet.',
    );
    expect(
      calls.filter((c) => c.route.endsWith('/verify')).map((c) => c.route),
    ).toEqual([`POST ${base}/domains/p.example.test/verify`]);
  });

  it('says when a check was refused for being too soon', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('p.example.test', 'pending')]),
      [`POST ${base}/domains/p.example.test/verify`]: () =>
        Response.json(
          { code: 'rate_limited', message: 'Checked moments ago.' },
          { status: 429, headers: { 'Retry-After': '7' } },
        ),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Check p.example.test now' }),
    );
    await waitFor(() =>
      expect(row('p.example.test').textContent).toContain(
        'Checked moments ago. Try again in 7 seconds.',
      ),
    );
  });

  it('removes a domain after a confirmation about the DNS record', async () => {
    const { api, calls } = fakeServer({
      ...sitePage([domain('p.example.test', 'pending')]),
      [`DELETE ${base}/domains/p.example.test`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove p.example.test' }),
    );
    const dialog = screen.getByRole('dialog', {
      name: 'Remove p.example.test?',
    });
    expect(dialog.textContent).toContain('delete the CNAME record');
    expect(dialog.textContent).toContain('_wardlock.p.example.test');
    expect(calls.some((c) => c.route.startsWith('DELETE'))).toBe(false);
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Remove domain' }),
    );
    await screen.findByRole('heading', { name: 'No custom domains yet' });
    expect(card().textContent).toContain(
      'Removed p.example.test. Remember to delete its DNS record.',
    );
  });

  it('shows no domains for a foreign site, only Site not found', async () => {
    // Another owner's site answers 404 like a missing one.
    const { api, calls } = fakeServer({});
    render(<App api={api} />);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Site not found' }),
    ).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Custom domains' })).toBeNull();
    expect(calls.some((c) => c.route.startsWith('POST'))).toBe(false);
  });

  it('offers a retry when the list cannot load', async () => {
    let fail = true;
    const { api } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/domains`]: () =>
        fail
          ? Response.json(
              { code: 'internal', message: 'Internal error.' },
              { status: 500 },
            )
          : Response.json({ items: [] }),
    });
    render(<App api={api} />);
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Could not load the custom domains.',
    );
    fail = false;
    fireEvent.click(within(card()).getByRole('button', { name: 'Try again' }));
    await screen.findByRole('heading', { name: 'No custom domains yet' });
  });
});
