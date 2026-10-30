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
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { App } from '../app.js';
import type { Site } from '../sites/format.js';
import { runEffects } from '../test/effects.js';
import { refreshInterval } from './domains.js';
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
  unavailable: false,
  tagIds: [],
  pinned: false,
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

beforeEach(() => window.history.replaceState(null, '', '/sites/docs/domains'));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

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
          { code: 'checked_recently', message: 'Checked moments ago.' },
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

  it('shows the server’s error for a 429 that is not the cooldown', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('p.example.test', 'pending')]),
      [`POST ${base}/domains/p.example.test/verify`]: () =>
        Response.json(
          {
            code: 'rate_limited',
            message: 'Too many attempts. Try again later.',
          },
          { status: 429, headers: { 'Retry-After': '600' } },
        ),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Check p.example.test now' }),
    );
    await waitFor(() =>
      expect(row('p.example.test').textContent).toContain(
        'Could not check the domain: Too many attempts. Try again later.',
      ),
    );
    expect(row('p.example.test').textContent).not.toContain(
      'Checked moments ago',
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

  it('keeps a persistent, empty status region for results', async () => {
    const { api } = fakeServer(sitePage([domain('p.example.test', 'pending')]));
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    const regions = within(card())
      .getAllByRole('status')
      .filter((el) => el.classList.contains('domain-live'));
    // One for the card, one for the row; both mounted before anything happens.
    expect(regions).toHaveLength(2);
    expect(regions.every((el) => el.textContent === '')).toBe(true);
  });

  it('reads the list again while a domain is pending, and redraws times', async () => {
    const ticks: (() => void)[] = [];
    const realInterval = window.setInterval;
    vi.spyOn(window, 'setInterval').mockImplementation(((
      handler: () => void,
      delay?: number,
    ) => {
      if (delay === refreshInterval) {
        ticks.push(handler);
        return 0;
      }
      return realInterval(handler, delay);
    }) as typeof window.setInterval);
    let current = domain('p.example.test', 'pending', {
      lastCheckedAt: now - 3 * minute,
    });
    const { api, calls } = fakeServer({
      ...sitePage([]),
      [`GET ${base}/domains`]: () => Response.json({ items: [current] }),
    });
    const app = <App api={api} />;
    const { rerender } = render(app);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    // The refresh timer starts in an effect after the list is shown.
    runEffects(rerender, app);
    expect(row('p.example.test').textContent).toContain(
      'Last checked 3 minutes ago',
    );
    const lists = () =>
      calls.filter((c) => c.route === `GET ${base}/domains`).length;
    const before = lists();

    // Time passes: the tick redraws relative times without new data.
    const later = now + 2 * minute;
    vi.spyOn(Date, 'now').mockReturnValue(later);
    current = domain('p.example.test', 'active', {
      lastCheckedAt: later,
      verifiedAt: later,
    });
    await act(async () => ticks.at(-1)!());
    await waitFor(() =>
      expect(within(row('p.example.test')).getByText('Active')).toBeTruthy(),
    );
    expect(lists()).toBe(before + 1);
    expect(row('p.example.test').textContent).toContain(
      'Last checked just now',
    );

    // Nothing pending any more: ticks only redraw, they do not fetch.
    await act(async () => ticks.at(-1)!());
    expect(lists()).toBe(before + 1);
  });

  it('does not read the list while the page is hidden', async () => {
    const ticks: (() => void)[] = [];
    const realInterval = window.setInterval;
    vi.spyOn(window, 'setInterval').mockImplementation(((
      handler: () => void,
      delay?: number,
    ) => {
      if (delay === refreshInterval) {
        ticks.push(handler);
        return 0;
      }
      return realInterval(handler, delay);
    }) as typeof window.setInterval);
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    const { api, calls } = fakeServer(
      sitePage([domain('p.example.test', 'pending')]),
    );
    const app = <App api={api} />;
    const { rerender } = render(app);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    // The refresh timer starts in an effect after the list is shown.
    runEffects(rerender, app);
    const lists = () =>
      calls.filter((c) => c.route === `GET ${base}/domains`).length;
    const before = lists();
    await act(async () => ticks.at(-1)!());
    expect(lists()).toBe(before);
    // Coming back reads it straight away.
    hidden.mockReturnValue(false);
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await waitFor(() => expect(lists()).toBe(before + 1));
  });

  it('keeps focus on Check now while the check runs', async () => {
    // The verify answer is held until the test releases it.
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => (release = resolve));
    const { api, calls } = fakeServer({
      ...sitePage([domain('l.example.test', 'lapsed')]),
      [`POST ${base}/domains/l.example.test/verify`]: (() =>
        held.then(() =>
          Response.json(domain('l.example.test', 'lapsed')),
        )) as unknown as Handler,
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'l.example.test' });
    const button = screen.getByRole('button', {
      name: 'Check l.example.test now',
    });
    button.focus();
    fireEvent.click(button);
    await waitFor(() => expect(button.textContent).toBe('Checking…'));
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(button.hasAttribute('disabled')).toBe(false);
    expect(document.activeElement).toBe(button);
    // A second press while checking sends nothing more.
    fireEvent.click(button);
    release();
    await waitFor(() => expect(button.textContent).toBe('Check now'));
    expect(document.activeElement).toBe(button);
    expect(calls.filter((c) => c.route.endsWith('/verify'))).toHaveLength(1);
  });

  it('says a lapsed domain still does not check out', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('l.example.test', 'lapsed')]),
      [`POST ${base}/domains/l.example.test/verify`]: () =>
        Response.json(domain('l.example.test', 'lapsed')),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'l.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Check l.example.test now' }),
    );
    await waitFor(() =>
      expect(row('l.example.test').textContent).toContain(
        'Checked: the record still does not check out.',
      ),
    );
    expect(row('l.example.test').textContent).not.toContain('yet.');
  });

  it('explains a domain dropped by Check now and moves focus to it', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('p.example.test', 'pending')]),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    const button = screen.getByRole('button', {
      name: 'Check p.example.test now',
    });
    button.focus();
    fireEvent.click(button);
    await screen.findByRole('heading', { name: 'No custom domains yet' });
    const notice = within(card()).getByText(
      'p.example.test was no longer claimed and has been removed from the list.',
    );
    const region = notice.closest('[role="status"]')!;
    await waitFor(() => expect(document.activeElement).toBe(region));
  });

  it('moves focus to the result after removing a domain', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('p.example.test', 'pending')]),
      [`DELETE ${base}/domains/p.example.test`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'p.example.test' });
    const opener = screen.getByRole('button', {
      name: 'Remove p.example.test',
    });
    opener.focus();
    fireEvent.click(opener);
    fireEvent.click(screen.getByRole('button', { name: 'Remove domain' }));
    const notice = await within(card()).findByText(
      'Removed p.example.test. Remember to delete its DNS record.',
    );
    const region = notice.closest('[role="status"]')!;
    await waitFor(() => expect(document.activeElement).toBe(region));
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

describe('the primary hostname', () => {
  const picker = () => screen.getByRole('region', { name: 'Primary hostname' });
  const choice = (label: string) =>
    within(picker()).getByRole('radio', { name: label }) as HTMLInputElement;
  const save = () =>
    within(picker()).getByRole('button', { name: 'Save primary hostname' });
  const redirecting = () =>
    within(
      within(picker()).getByRole('list', { name: 'Redirecting hostnames' }),
    )
      .getAllByRole('listitem')
      .map((item) => item.textContent);

  it('offers only served hostnames, with none chosen by default', async () => {
    const { api } = fakeServer(
      sitePage([
        domain('a.example.test', 'active'),
        domain('p.example.test', 'pending'),
        domain('l.example.test', 'lapsed'),
      ]),
    );
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    expect(picker().textContent).toContain(
      'No primary hostname: every hostname serves the site.',
    );
    expect(choice('None').checked).toBe(true);
    expect(choice('docs.sites.example.test').disabled).toBe(false);
    expect(choice('a.example.test').disabled).toBe(false);
    // Hostnames that are not served cannot be chosen, and say why.
    expect(choice('p.example.test').disabled).toBe(true);
    expect(choice('l.example.test').disabled).toBe(true);
    expect(picker().textContent).toContain(
      'Pending: it can be the primary once its DNS record checks out.',
    );
    // Nothing to save until the choice changes.
    expect((save() as HTMLButtonElement).disabled).toBe(true);
  });

  it('chooses a custom domain and shows which hostnames redirect to it', async () => {
    let current = docs;
    const { api, calls } = fakeServer({
      ...sitePage([
        domain('a.example.test', 'active'),
        domain('b.example.test', 'active'),
        domain('p.example.test', 'pending'),
      ]),
      [`GET ${base}`]: () => Response.json(current),
      [`PUT ${base}/primary-host`]: () => {
        current = { ...docs, primaryHost: 'a.example.test' };
        return Response.json(current);
      },
    });
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    fireEvent.click(choice('a.example.test'));
    fireEvent.click(save());
    expect(
      await within(picker()).findByText(
        'Saved. Every other hostname now redirects to a.example.test.',
      ),
    ).toBeTruthy();
    expect(
      calls.find((c) => c.route === `PUT ${base}/primary-host`)?.body,
    ).toEqual({ host: 'a.example.test' });
    // The site's own address and the other active domain redirect; the
    // pending one is not served at all.
    expect(redirecting()).toEqual([
      'docs.sites.example.test',
      'b.example.test',
    ]);
    expect(within(row('a.example.test')).getByText('Primary')).toBeTruthy();
    expect(row('b.example.test').textContent).toContain(
      'Redirects to the primary hostname, a.example.test.',
    );
    expect(row('p.example.test').textContent).not.toContain('Redirects to');
    expect(choice('b.example.test').parentElement!.textContent).toContain(
      'Redirects to a.example.test now.',
    );
    await waitFor(() =>
      expect((save() as HTMLButtonElement).disabled).toBe(true),
    );
  });

  it('chooses the site’s own address', async () => {
    const { api, calls } = fakeServer({
      ...sitePage([domain('a.example.test', 'active')]),
      [`PUT ${base}/primary-host`]: () =>
        Response.json({ ...docs, primaryHost: docs.hostname }),
    });
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    fireEvent.click(choice('docs.sites.example.test'));
    fireEvent.click(save());
    await within(picker()).findByText(
      'Saved. Every other hostname now redirects to docs.sites.example.test.',
    );
    expect(
      calls.find((c) => c.route === `PUT ${base}/primary-host`)?.body,
    ).toEqual({ host: 'docs.sites.example.test' });
    expect(redirecting()).toEqual(['a.example.test']);
  });

  it('turns the primary off', async () => {
    const chosen = { ...docs, primaryHost: 'a.example.test' };
    const { api, calls } = fakeServer({
      ...sitePage([domain('a.example.test', 'active', { primary: true })]),
      [`GET ${base}`]: () => Response.json(chosen),
      [`DELETE ${base}/primary-host`]: () => Response.json(docs),
    });
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    expect(choice('a.example.test').checked).toBe(true);
    expect(redirecting()).toEqual(['docs.sites.example.test']);
    fireEvent.click(choice('None'));
    fireEvent.click(save());
    expect(
      await within(picker()).findByText(
        'Saved. Every hostname serves the site, with no redirects.',
      ),
    ).toBeTruthy();
    expect(calls.some((c) => c.route === `DELETE ${base}/primary-host`)).toBe(
      true,
    );
    expect(picker().textContent).toContain('No primary hostname');
    expect(within(row('a.example.test')).queryByText('Primary')).toBeNull();
  });

  it('says nothing redirects while the primary domain has lapsed', async () => {
    // A lapsed primary is not in effect: the site has no primaryHost.
    const { api } = fakeServer(
      sitePage([
        domain('a.example.test', 'active'),
        domain('l.example.test', 'lapsed', { primary: true }),
      ]),
    );
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    expect(picker().textContent).toContain(
      'l.example.test is the primary hostname, but it has lapsed, so nothing redirects',
    );
    expect(
      within(picker()).queryByRole('list', { name: 'Redirecting hostnames' }),
    ).toBeNull();
    // Still the owner's choice, so it stays checked and the badge stays.
    expect(choice('l.example.test').checked).toBe(true);
    expect(within(row('l.example.test')).getByText('Primary')).toBeTruthy();
    expect(row('a.example.test').textContent).not.toContain('Redirects to');
    // Another served hostname can take over.
    expect(choice('a.example.test').disabled).toBe(false);
  });

  it('forgets the choice when the primary domain is removed', async () => {
    const chosen = { ...docs, primaryHost: 'a.example.test' };
    const { api } = fakeServer({
      ...sitePage([domain('a.example.test', 'active', { primary: true })]),
      [`GET ${base}`]: () => Response.json(chosen),
      [`DELETE ${base}/domains/a.example.test`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<App api={api} />);
    await screen.findByRole('listitem', { name: 'a.example.test' });
    fireEvent.click(
      screen.getByRole('button', { name: 'Remove a.example.test' }),
    );
    fireEvent.click(
      within(
        await screen.findByRole('dialog', { name: 'Remove a.example.test?' }),
      ).getByRole('button', { name: 'Remove domain' }),
    );
    await screen.findByRole('heading', { name: 'No custom domains yet' });
    await waitFor(() =>
      expect(picker().textContent).toContain('No primary hostname'),
    );
    expect(choice('None').checked).toBe(true);
  });

  it('explains a refusal from the server', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('a.example.test', 'active')]),
      [`PUT ${base}/primary-host`]: () =>
        Response.json(
          { code: 'domain_not_active', message: 'Not active.' },
          { status: 409 },
        ),
    });
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    fireEvent.click(choice('a.example.test'));
    fireEvent.click(save());
    expect(
      await within(picker()).findByText(
        'a.example.test is not active, so it cannot be the primary yet. Choose it once its DNS record checks out.',
      ),
    ).toBeTruthy();
    expect(picker().textContent).toContain('No primary hostname');
  });

  it('shows the primary but offers no change on a taken-down site', async () => {
    const { api } = fakeServer({
      ...sitePage([domain('a.example.test', 'active', { primary: true })]),
      [`GET ${base}`]: () =>
        Response.json({
          ...docs,
          unavailable: true,
          primaryHost: 'a.example.test',
        }),
    });
    render(<App api={api} />);
    await screen.findByRole('region', { name: 'Primary hostname' });
    expect(picker().textContent).toContain(
      'a.example.test is the primary hostname.',
    );
    expect(within(picker()).queryByRole('radio')).toBeNull();
    expect(within(picker()).queryByRole('button')).toBeNull();
  });
});
