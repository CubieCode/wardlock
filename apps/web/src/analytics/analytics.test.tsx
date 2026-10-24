// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient, type components } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../app.js';
import { readQuery, requestsPerDay, type Analytics } from './data.js';

type SiteSummary = components['schemas']['SiteSummary'];

const me = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

const groupId = '01928c5e-7a1b-7c3d-9e4f-00000000a001';
const tagId = '01928c5e-7a1b-7c3d-9e4f-00000000b001';
const otherTagId = '01928c5e-7a1b-7c3d-9e4f-00000000b002';
const groups = {
  items: [{ id: groupId, name: 'Client work', createdAt: 1767225600000 }],
};
const tags = {
  items: [
    { id: tagId, name: 'docs', colour: 3, createdAt: 1767225600000 },
    { id: otherTagId, name: 'drafts', colour: 5, createdAt: 1767225600000 },
  ],
};

function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-${name.padEnd(12, '0').slice(0, 12)}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: 1767225600000,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'protected',
    methods: ['links'],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [],
    quiet: false,
    ...extra,
  };
}

const hour = 3600_000;
const to = Date.UTC(2026, 9, 23, 0) + hour;

/** A report over `days` with a request in its last hour and one a day ago. */
function report(days: number, extra: Partial<Analytics> = {}): Analytics {
  return {
    days: days as Analytics['days'],
    from: to - days * 24 * hour,
    to,
    total: { allowed: 42, refused: 3 },
    hours: [
      { hour: to - 25 * hour, allowed: 2, refused: 0 },
      { hour: to - hour, allowed: 40, refused: 3 },
    ],
    via: [
      { via: 'link', allowed: 30, refused: 0 },
      { via: 'none', allowed: 0, refused: 3 },
      { via: 'public', allowed: 12, refused: 0 },
    ],
    sites: [
      { site: 'docs', allowed: 30, refused: 3 },
      { site: 'drafts', allowed: 12, refused: 0 },
    ],
    groups: [
      { groupId, allowed: 30, refused: 3 },
      { allowed: 12, refused: 0 },
    ],
    tags: [
      { tagId, allowed: 42, refused: 3 },
      { tagId: otherTagId, allowed: 12, refused: 0 },
    ],
    links: [
      {
        site: 'docs',
        publicId: 'q4v7m2xkpa',
        label: 'Client review',
        allowed: 30,
        refused: 0,
      },
      { site: 'docs', publicId: 'b2c3d4e5f6', allowed: 1, refused: 0 },
    ],
    paths: [
      { site: 'docs', path: '/', allowed: 25, refused: 1 },
      { site: 'drafts', path: '/notes/', allowed: 12, refused: 0 },
    ],
    otherPaths: { allowed: 5, refused: 2 },
    browsers: [
      { browser: 'chrome', allowed: 28, refused: 0 },
      { browser: 'tool', allowed: 14, refused: 3 },
    ],
    ...extra,
  };
}

const empty = (days: number) =>
  report(days, {
    total: { allowed: 0, refused: 0 },
    hours: [],
    via: [],
    sites: [],
    groups: [],
    tags: [],
    links: [],
    paths: [],
    otherPaths: { allowed: 0, refused: 0 },
    browsers: [],
  });

type Handler = (request: Request) => Response | Promise<Response>;

const notFound = () =>
  Response.json({ code: 'not_found', message: 'Not found.' }, { status: 404 });

function fakeServer(routes: Record<string, Handler> = {}) {
  const calls: URL[] = [];
  const all: Record<string, Handler> = {
    'GET /api/v0/me': () => Response.json(me),
    'GET /api/v0/groups': () => Response.json(groups),
    'GET /api/v0/tags': () => Response.json(tags),
    'GET /api/v0/analytics': (request) =>
      Response.json(
        report(Number(new URL(request.url).searchParams.get('days') ?? 7)),
      ),
    'GET /api/v0/site-summaries': (request) => {
      const query = new URL(request.url).searchParams;
      const sites = [
        summary('docs', { groupId, tagIds: [tagId], workingLinks: 2 }),
        summary('docs-old', { quiet: true, tagIds: [tagId] }),
        summary('drafts', { workingLinks: 1, quiet: true }),
        // Taken down: not quiet, as in the navigator's Quiet view.
        summary('gone', { quiet: true, unavailable: true }),
      ];
      return Response.json({
        items: sites.filter(
          (site) =>
            (!query.get('group') || site.groupId === query.get('group')) &&
            (!query.get('tag') || site.tagIds.includes(query.get('tag')!)) &&
            site.name.includes(query.get('q') ?? ''),
        ),
      });
    },
    ...routes,
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      calls.push(url);
      const handler = all[`${request.method} ${url.pathname}`];
      return handler ? handler(request) : notFound();
    },
  });
  const analyticsCalls = () =>
    calls
      .filter((url) => url.pathname === '/api/v0/analytics')
      .map((url) => Object.fromEntries(url.searchParams));
  const summaryCalls = () =>
    calls.filter((url) => url.pathname === '/api/v0/site-summaries').length;
  return { api, analyticsCalls, summaryCalls };
}

const at = (path: string) => window.history.replaceState(null, '', path);

async function open(path: string, routes: Record<string, Handler> = {}) {
  at(path);
  const server = fakeServer(routes);
  render(<App api={server.api} />);
  await screen.findByRole('heading', { level: 1, name: 'Analytics' });
  return server;
}

/** The figure under a totals label, such as "Refused requests". */
function figure(name: string) {
  const term = screen.getByText(name, { selector: 'dt' });
  return [...term.parentElement!.querySelectorAll('dd')].map(
    (dd) => dd.textContent,
  );
}

const card = (name: string) => screen.getByRole('region', { name });
const cardTitles = () =>
  screen
    .getAllByRole('region')
    .map((region) => region.getAttribute('aria-label'));

afterEach(cleanup);

describe('scope', () => {
  it('counts all sites over 30 days by default, with every breakdown', async () => {
    const server = await open('/analytics');
    await screen.findByText(
      'Requests to all your sites over the last 30 days.',
    );
    expect(server.analyticsCalls()).toEqual([{ days: '30' }]);
    expect(figure('Allowed requests')).toEqual([
      '42',
      '30 days · all your sites',
    ]);
    expect(figure('Refused requests')).toEqual(['3', '6.7% of requests']);
    expect(figure('Working links')).toEqual(['3', 'Share links that work now']);
    expect(figure('Quiet sites')).toEqual(['2', 'No requests in 30 days']);
    expect(cardTitles()).toEqual([
      'Totals',
      'Requests per day',
      'How visitors got in',
      'By group',
      'By tag',
      'Top sites',
      'Top share links',
      'Top pages',
      'Browsers and tools',
    ]);
    expect(document.title).toBe('Analytics · Wardlock');
  });

  it('narrows to a group, keeping its name and leaving out By group', async () => {
    const server = await open(`/analytics?g=${groupId}`);
    await screen.findByText(
      'Requests to the Client work group over the last 30 days.',
    );
    expect(server.analyticsCalls()).toEqual([{ days: '30', group: groupId }]);
    expect(figure('Working links')[0]).toBe('2');
    expect(figure('Quiet sites')[0]).toBe('0');
    expect(cardTitles()).not.toContain('By group');
    expect(cardTitles()).toContain('By tag');
    const picker = screen.getByRole('combobox', { name: 'Showing' });
    expect((picker as HTMLSelectElement).value).toBe(`g:${groupId}`);
  });

  it('narrows to a tag and says a site counts under each of its tags', async () => {
    const server = await open(`/analytics?t=${tagId}&days=7`);
    await screen.findByText(
      'Requests to sites tagged docs over the last 7 days.',
    );
    expect(server.analyticsCalls()).toEqual([{ days: '7', tag: tagId }]);
    expect(figure('Quiet sites')[0]).toBe('1');
    expect(
      within(card('By tag')).getByText(
        'A site counts under each of its tags, so these can add up to more than the total.',
      ),
    ).toBeTruthy();
    // Tag bars take each tag's colour; no share, since tags overlap.
    const docs = within(card('By tag')).getByRole('link', { name: 'docs' });
    expect(docs.getAttribute('href')).toBe(`/analytics?t=${tagId}&days=7`);
    const row = docs.closest('li')!;
    expect(
      (row.querySelector('.wl-bar-fill') as HTMLElement).style.background,
    ).toBe('var(--color-tag-3)');
    expect(row.querySelector('.wl-bar-share')).toBeNull();
    expect(cardTitles()).toContain('By group');
  });

  it('narrows to one site, without per-site breakdowns', async () => {
    const server = await open('/analytics?site=docs&days=90');
    await screen.findByText('Requests to docs over the last 90 days.');
    expect(server.analyticsCalls()).toEqual([{ days: '90', site: 'docs' }]);
    // Search for "docs" also finds docs-old; only the site itself counts.
    expect(figure('Quiet sites')[0]).toBe('0');
    expect(figure('Working links')[0]).toBe('2');
    for (const name of ['By group', 'By tag', 'Top sites'])
      expect(cardTitles()).not.toContain(name);
    const picker = screen.getByRole('combobox', { name: 'Showing' });
    expect((picker as HTMLSelectElement).value).toBe('s:docs');
  });

  it('moves between scopes with the picker and bars, keeping the range', async () => {
    const server = await open('/analytics?days=7');
    await screen.findByText(/over the last 7 days/);
    fireEvent.change(screen.getByRole('combobox', { name: 'Showing' }), {
      target: { value: `t:${tagId}` },
    });
    await screen.findByText(
      'Requests to sites tagged docs over the last 7 days.',
    );
    expect(window.location.search).toBe(`?t=${tagId}&days=7`);
    fireEvent.click(
      within(card('Top sites')).getByRole('link', { name: 'drafts' }),
    );
    await screen.findByText('Requests to drafts over the last 7 days.');
    expect(server.analyticsCalls().at(-1)).toEqual({
      days: '7',
      site: 'drafts',
    });
  });

  it('says so when the site, group or tag is missing or not the owner’s', async () => {
    await open(`/analytics?g=${groupId}&days=7`, {
      'GET /api/v0/analytics': notFound,
    });
    await screen.findByRole('heading', { name: 'Nothing to count here' });
    expect(
      screen.getByRole('link', { name: 'Show all sites' }).getAttribute('href'),
    ).toBe('/analytics?days=7');
  });
});

describe('range', () => {
  it('reads 7, 30 or 90 days and falls back to 30', () => {
    expect(readQuery(new URLSearchParams('days=7')).days).toBe(7);
    expect(readQuery(new URLSearchParams('days=90')).days).toBe(90);
    expect(readQuery(new URLSearchParams('days=12')).days).toBe(30);
    expect(readQuery(new URLSearchParams('site=a&g=b')).scope).toEqual({
      kind: 'site',
      name: 'a',
    });
  });

  it('switches the range and reads the report again', async () => {
    const server = await open('/analytics');
    await screen.findByText(/over the last 30 days/);
    fireEvent.click(screen.getByRole('radio', { name: '90 days' }));
    await screen.findByText(/over the last 90 days/);
    expect(window.location.search).toBe('?days=90');
    expect(server.analyticsCalls().at(-1)).toEqual({ days: '90' });
    fireEvent.click(screen.getByRole('radio', { name: '30 days' }));
    await screen.findByText(/over the last 30 days/);
    expect(window.location.search).toBe('');
    // Site totals do not depend on the range: read once.
    expect(server.summaryCalls()).toBe(1);
  });

  it('keeps the newest range when an earlier answer arrives later', async () => {
    const answers = new Map<string, (response: Response) => void>();
    const server = await open('/analytics', {
      'GET /api/v0/analytics': (request) =>
        new Promise((resolve) =>
          answers.set(new URL(request.url).searchParams.get('days')!, resolve),
        ),
    });
    await waitFor(() => expect(answers.has('30')).toBe(true));
    fireEvent.click(screen.getByRole('radio', { name: '7 days' }));
    await waitFor(() => expect(answers.has('7')).toBe(true));
    answers.get('7')!(Response.json(report(7)));
    await screen.findByText(/over the last 7 days/);
    answers.get('30')!(Response.json(report(30)));
    await waitFor(() => expect(server.analyticsCalls()).toHaveLength(2));
    // Give the late answer a chance to land; the page stays on 7 days.
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.getByText(/over the last 7 days/)).toBeTruthy();
    expect(figure('Allowed requests')[1]).toBe('7 days · all your sites');
  });

  it('draws one bar per local day of the range', () => {
    const points = requestsPerDay(report(7));
    // 7 × 24 UTC hours span 7 or 8 local days, depending on the timezone.
    expect(points.length).toBeGreaterThanOrEqual(7);
    expect(points.length).toBeLessThanOrEqual(8);
    const sum = (i: 0 | 1) => points.reduce((n, p) => n + p.values[i], 0);
    expect([sum(0), sum(1)]).toEqual([42, 3]);
    expect(points.at(-1)!.values).toEqual([40, 3]);
  });
});

describe('states', () => {
  it('shows an empty state when the range has no requests', async () => {
    await open('/analytics', {
      'GET /api/v0/analytics': () => Response.json(empty(30)),
    });
    await screen.findByRole('heading', { name: 'No requests yet' });
    expect(figure('Refused requests')).toEqual(['0', 'No requests']);
    expect(
      screen.queryByRole('region', { name: 'Requests per day' }),
    ).toBeNull();
  });

  it('shows loading, then an error with a retry that reads again', async () => {
    let fail = true;
    const server = await open('/analytics', {
      'GET /api/v0/analytics': () =>
        fail
          ? Response.json(
              { code: 'internal', message: 'Something broke.' },
              { status: 500 },
            )
          : Response.json(report(30)),
    });
    await screen.findByText('Could not load analytics.');
    // The picker still names every group and tag, and stays usable.
    const picker = screen.getByRole('combobox', {
      name: 'Showing',
    }) as HTMLSelectElement;
    expect(picker.disabled).toBe(false);
    expect([...picker.options].map((o) => o.textContent)).toEqual([
      'All sites',
      'Client work',
      'docs',
      'drafts',
    ]);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(screen.getByRole('status').textContent).toBe('Counting requests…');
    await screen.findByText(/over the last 30 days/);
    expect(server.analyticsCalls()).toHaveLength(2);
    // Only the failed read is repeated.
    expect(server.summaryCalls()).toBe(1);
  });

  it('says plainly when the site totals cannot be read', async () => {
    await open('/analytics', {
      'GET /api/v0/site-summaries': () =>
        Response.json({ code: 'internal', message: 'No.' }, { status: 500 }),
    });
    await screen.findByText('Could not load analytics.');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('shows the current scope in the picker while a new one loads', async () => {
    let answer: (response: Response) => void = () => {};
    await open(`/analytics?t=${tagId}`, {
      'GET /api/v0/analytics': (request) =>
        new URL(request.url).searchParams.get('group')
          ? new Promise((resolve) => (answer = resolve))
          : Response.json(report(30)),
    });
    await screen.findByText(
      'Requests to sites tagged docs over the last 30 days.',
    );
    const picker = () =>
      screen.getByRole('combobox', { name: 'Showing' }) as HTMLSelectElement;
    fireEvent.change(picker(), { target: { value: `g:${groupId}` } });
    await screen.findByText('Counting requests…');
    expect(picker().value).toBe(`g:${groupId}`);
    expect(picker().selectedOptions[0]!.textContent).toBe('Client work');
    answer(Response.json(report(30)));
    await screen.findByText(
      'Requests to the Client work group over the last 30 days.',
    );
  });
});

describe('charts', () => {
  it('counts ways in from allowed requests, and refused ones apart, with no reason', async () => {
    await open('/analytics');
    const via = await screen.findByRole('region', {
      name: 'How visitors got in',
    });
    const list = within(via).getByRole('list', { name: 'How visitors got in' });
    expect(
      within(list)
        .getAllByRole('listitem')
        .map((li) => li.querySelector('.wl-bar-name')!.textContent),
    ).toEqual(['Share link', 'Public']);
    expect(
      within(via).getByText('3 requests refused, not counted here.'),
    ).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/visits?\b/i);
  });

  it('shows each bar’s numbers on keyboard focus and hides them on Escape', async () => {
    await open('/analytics');
    const browsers = await screen.findByRole('region', {
      name: 'Browsers and tools',
    });
    const chrome = within(browsers).getAllByRole('listitem')[0]!;
    fireEvent.focus(chrome);
    const tip = within(chrome).getByRole('tooltip');
    expect(tip.hidden).toBe(false);
    expect(tip.textContent).toBe('Chrome28 · 62%');
    fireEvent.keyDown(chrome, { key: 'Escape' });
    expect(tip.hidden).toBe(true);
  });

  it('gives every chart a table view', async () => {
    await open('/analytics');
    await screen.findByRole('region', { name: 'Top pages' });
    const tables = screen.getAllByRole('table', { hidden: true });
    expect(tables.map((t) => t.querySelector('caption')!.textContent)).toEqual([
      'Requests per day, last 30 days',
      'How visitors got in',
      'By group',
      'By tag',
      'Top sites',
      'Top share links',
      'Top pages',
      'Browsers and tools',
    ]);
    const pages = within(card('Top pages')).getByRole('table', {
      hidden: true,
    });
    const rows = within(pages)
      .getAllByRole('row', { hidden: true })
      .map((row) => row.textContent);
    expect(rows).toEqual([
      'PathSiteAllowedRefused',
      '/docs251',
      '/notes/drafts120',
    ]);
    expect(
      within(card('Top pages')).getByText(
        '7 requests for less common paths are not listed.',
      ),
    ).toBeTruthy();
    const links = within(card('Top share links'));
    expect(
      links.getByRole('link', { name: 'Client review' }).getAttribute('href'),
    ).toBe('/sites/docs/links/q4v7m2xkpa');
    expect(links.getByRole('link', { name: 'Link b2c3d4e5f6' })).toBeTruthy();
    const perDay = within(card('Requests per day')).getByRole('table', {
      hidden: true,
    });
    await waitFor(() =>
      expect(
        within(perDay).getAllByRole('row', { hidden: true }).length,
      ).toBeGreaterThan(30),
    );
  });

  it('names ungrouped sites and links each group to its own scope', async () => {
    await open('/analytics');
    const byGroup = await screen.findByRole('region', { name: 'By group' });
    expect(
      within(byGroup)
        .getByRole('link', { name: 'Client work' })
        .getAttribute('href'),
    ).toBe(`/analytics?g=${groupId}`);
    expect(
      within(byGroup).getByText('No group', { selector: '.wl-bar-name' }),
    ).toBeTruthy();
  });
});

describe('countries', () => {
  const attribution =
    'IP Geolocation by DB-IP (https://db-ip.com), licensed under CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)';
  const known = [
    'NZ',
    'AU',
    'US',
    'GB',
    'DE',
    'JP',
    'CA',
    'FR',
    'NL',
    'SG',
  ].map((country, i) => ({ country, allowed: 20 - i, refused: 0 }));
  const withCountries = (extra: Partial<Analytics> = {}) => ({
    'GET /api/v0/analytics': () =>
      Response.json(
        report(30, {
          countries: [...known, { country: 'unknown', allowed: 0, refused: 4 }],
          countryLookup: { attribution, serverCountry: 'NZ' },
          ...extra,
        }),
      ),
  });

  it('shows the map, the busiest countries, the rest folded in and Unknown', async () => {
    await open('/analytics', withCountries());
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    const list = within(geo).getByRole('list', { name: 'Requests by country' });
    const rows = within(list)
      .getAllByRole('listitem')
      .map((item) => item.firstChild?.textContent);
    expect(rows).toEqual([
      'New Zealand',
      'Australia',
      'United States',
      'United Kingdom',
      'Germany',
      'Japan',
      'Canada',
      'France',
      '2 more countries',
      'Unknown',
    ]);
    expect(
      within(geo).getByText(/^Unknown: The country database has no country/),
    ).toBeTruthy();
    // The map loads on demand, with a focusable bubble per country.
    const bubble = await within(geo).findByRole('img', {
      name: 'New Zealand: 20 requests · 12.6%',
    });
    expect(bubble.getAttribute('tabindex')).toBe('0');
    fireEvent.focus(bubble);
    expect(within(geo).getByRole('tooltip').textContent).toBe(
      'New Zealand20 requests · 12.6%',
    );
    fireEvent.keyDown(bubble, { key: 'Escape' });
    expect(within(geo).queryByRole('tooltip')).toBeNull();
    expect(
      within(geo).getByRole('img', { name: 'Your server, in New Zealand' }),
    ).toBeTruthy();
    expect(within(geo).getByText('Your server')).toBeTruthy();
  });

  it('lists every country and Unknown in the table view', async () => {
    await open('/analytics', withCountries());
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    const table = within(geo).getByRole('table', {
      name: 'Requests by country',
    });
    const rows = within(table).getAllByRole('row');
    expect(rows.map((row) => row.textContent)).toEqual([
      'CountryAllowedRefusedShare',
      'New Zealand20012.6%',
      'Australia19011.9%',
      'United States18011.3%',
      'United Kingdom17010.7%',
      'Germany16010.1%',
      'Japan1509.4%',
      'Canada1408.8%',
      'France1308.2%',
      'Netherlands1207.5%',
      'Singapore1106.9%',
      'Unknown042.5%',
    ]);
  });

  it('draws no hub without a server country', async () => {
    await open('/analytics', withCountries({ countryLookup: { attribution } }));
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    await within(geo).findByRole('img', { name: /^New Zealand:/ });
    expect(within(geo).queryByRole('img', { name: /Your server/ })).toBeNull();
    expect(within(geo).queryByText('Your server')).toBeNull();
    expect(geo.querySelector('.country-lines')).toBeNull();
  });

  it('names no server in the legend when its country has no point on the map', async () => {
    await open(
      '/analytics',
      // Gibraltar is too small for Natural Earth's label points.
      withCountries({ countryLookup: { attribution, serverCountry: 'GI' } }),
    );
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    await within(geo).findByRole('img', { name: /^New Zealand:/ });
    expect(within(geo).getByText('Bubble area: requests')).toBeTruthy();
    expect(within(geo).queryByRole('img', { name: /Your server/ })).toBeNull();
    expect(within(geo).queryByText('Your server')).toBeNull();
  });

  it('credits the database as given, linking only its https URLs', async () => {
    await open(
      '/analytics',
      withCountries({
        countryLookup: {
          attribution:
            'Data from Example (https://example.test/geo). Mirror: http://example.test/ and javascript:alert(1)',
        },
      }),
    );
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    const credit = geo.querySelector('.country-credit')!;
    expect(credit.textContent).toBe(
      'Data from Example (https://example.test/geo). Mirror: http://example.test/ and javascript:alert(1)',
    );
    expect(
      [...credit.querySelectorAll('a')].map((a) => a.getAttribute('href')),
    ).toEqual(['https://example.test/geo']);
  });

  it('says how many requests have no country when only Unknown or none are counted', async () => {
    await open(
      '/analytics',
      withCountries({
        countries: [{ country: 'unknown', allowed: 2, refused: 0 }],
      }),
    );
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    expect(
      within(geo).getByText(
        '43 requests logged while the country lookup was off have no country and are not counted here.',
      ),
    ).toBeTruthy();
    expect(
      within(geo)
        .getAllByRole('listitem')
        .map((item) => item.firstChild?.textContent),
    ).toEqual(['Unknown']);
  });

  it('says so, and why, when no request in the range has a country', async () => {
    await open('/analytics', withCountries({ countries: [] }));
    const geo = await screen.findByRole('region', {
      name: 'Where requests come from',
    });
    expect(
      within(geo).getByText('No requests in this range have a country.'),
    ).toBeTruthy();
    expect(
      within(geo).getByText(
        '45 requests logged while the country lookup was off have no country and are not counted here.',
      ),
    ).toBeTruthy();
    expect(within(geo).queryByRole('table')).toBeNull();
  });

  it('shows nothing about countries while the lookup is off', async () => {
    await open('/analytics');
    await screen.findByRole('region', { name: 'Requests per day' });
    expect(cardTitles()).not.toContain('Where requests come from');
    expect(document.body.textContent).not.toMatch(/countr/i);
  });
});
