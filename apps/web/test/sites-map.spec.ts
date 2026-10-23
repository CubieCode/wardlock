// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The sites map (V5.9) in a real browser against a real server: the List/Map
// switch keeps the filters, filters fade the sites they leave out, a focused
// dot shows its tooltip, a hub filters to its group, the text list holds
// every site, and a site named map keeps its own address. All sites, groups
// and names are synthetic.
import { execFileSync } from 'node:child_process';
import {
  expect,
  test,
  type APIRequestContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
const wardlockd = process.env.WARDLOCK_ACCEPTANCE_WARDLOCKD;
const adminEnv = process.env.WARDLOCK_ACCEPTANCE_ADMIN_ENV;
if (!origin || !wardlockd || !adminEnv)
  throw new Error('Run through pnpm browser:test');
const headers = { Origin: origin };

// The first owner's saved session: email-code sends are limited per IP. Each
// project and theme files its own sites under its own prefix and groups, and
// the map is searched by that prefix, so other runs' sites are only faded.
test.use({ storageState: firstOwnerState });

/** Runs `wardlockd admin <args>` against the test server's data. */
function admin(...args: string[]) {
  return execFileSync(wardlockd!, ['admin', ...args], {
    env: { ...process.env, ...(JSON.parse(adminEnv!) as object) },
    encoding: 'utf8',
  });
}

async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    animations: 'disabled',
  });
}

async function post<T>(request: APIRequestContext, path: string, data: object) {
  const res = await request.post(`${origin}${path}`, { data, headers });
  expect(res.ok(), `${path}: ${res.status()}`).toBe(true);
  return (await res.json()) as T;
}

interface Named {
  id: string;
  name: string;
}

for (const scheme of schemes) {
  test(`sites map: switch, fading, tooltip, hubs and the text list (${scheme})`, async ({
    page,
  }, info) => {
    test.setTimeout(180_000);
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `sites-map-${step}-${scheme}`);
    const prefix = `mp-${info.project.name}-${scheme}`;
    const name = (i: number) => `${prefix}-${String(i).padStart(2, '0')}`;
    const names = Array.from({ length: 60 }, (_, i) => name(i));
    const label = `${info.project.name} ${scheme}`;
    const map = page.getByRole('group', { name: /^Map of / });
    const dot = (i: number) =>
      map.getByRole('link', { name: new RegExp(`^${name(i)}: `) });

    let north!: Named;
    await test.step('60 synthetic sites in three groups', async () => {
      const { request } = page;
      const group = (title: string) =>
        post<Named>(request, '/api/v0/groups', { name: `${title} ${label}` });
      north = await group('North');
      const south = await group('South');
      const east = await group('East');
      for (let i = 0; i < names.length; i += 10)
        await Promise.all(
          names.slice(i, i + 10).map((site) => createSite(request, site)),
        );
      await Promise.all(
        names
          .filter((_, i) => i % 3 === 0)
          .map((site) => protect(request, site)),
      );
      const organise = (sites: string[], change: object) =>
        post(request, '/api/v0/organise', { sites, ...change });
      await organise(names.slice(0, 20), { groupId: north.id });
      await organise(names.slice(20, 40), { groupId: south.id });
      await organise(names.slice(40, 50), { groupId: east.id });
      await organise([name(1), name(2)], { pinned: true });
      expect(admin('takedown-site', name(5))).toContain(`Took down ${name(5)}`);
    });

    try {
      await test.step('the list’s Map switch keeps the search', async () => {
        await page.goto(`${origin}/sites?q=${prefix}&sort=name`);
        await page
          .getByRole('navigation', { name: 'Show sites as' })
          .getByRole('link', { name: 'Map' })
          .click();
        await expect(
          page.getByRole('heading', { level: 1, name: 'Sites map' }),
        ).toBeVisible();
        expect(new URL(page.url()).pathname).toBe('/sites');
        expect(new URL(page.url()).searchParams.get('view')).toBe('map');
        expect(new URL(page.url()).searchParams.get('q')).toBe(prefix);
        expect(new URL(page.url()).searchParams.get('sort')).toBe('name');
        await expect(
          page.getByText(/^60 of [\d,]+ sites match$/),
        ).toBeVisible();
        await expect(
          map.getByRole('link', {
            name: `North ${label}: 20 sites, 20 matching`,
          }),
        ).toBeVisible();
        await expect(dot(0)).toHaveAttribute(
          'aria-label',
          `${name(0)}: Protected, 0 requests in 7 days`,
        );
        await expect(dot(0)).toHaveAttribute('data-access', 'protected');
        await expect(dot(4)).toHaveAttribute('data-access', 'public');
        await expect(dot(5)).toHaveAttribute('data-access', 'down');
        // Pinned sites carry their names.
        await expect(dot(1)).toHaveText(name(1));
        await expect(dot(4)).toHaveText('');
        await shot('search');
      });

      await test.step('a narrower search fades the rest instead of hiding it', async () => {
        await page
          .getByRole('searchbox', { name: 'Search sites' })
          .fill(`${prefix} is:protected`);
        await expect(
          page.getByText(/^20 of [\d,]+ sites match$/),
        ).toBeVisible();
        await expect(
          page.getByRole('group', { name: 'Filters' }),
        ).toContainText('Protected');
        await expect(dot(1)).toHaveAttribute(
          'aria-label',
          `${name(1)}: Public, 0 requests in 7 days, pinned, outside the filters`,
        );
        await expect(dot(1)).toHaveClass(/map-dim/);
        await expect(dot(3)).not.toHaveClass(/map-dim/);
        await expect(dot(1)).toBeAttached();
        await expect(page.getByRole('list', { name: 'Legend' })).toContainText(
          'Faded: outside the filters',
        );
        await shot('faded');
      });

      await test.step('a focused dot shows its tooltip; Escape hides it', async () => {
        await dot(3).focus();
        const tip = page.locator('.map-tip');
        await expect(tip).toBeVisible();
        await expect(tip).toContainText(name(3));
        await expect(tip).toContainText('Protected');
        await expect(tip).toContainText('0 requests in 7 days');
        await shot('tooltip');
        await page.keyboard.press('Escape');
        await expect(tip).toHaveCount(0);
        // Tab reaches the next link and shows its tooltip.
        await page.keyboard.press('Tab');
        await expect(tip).toBeVisible();
      });

      await test.step('the whole map, with the text list open', async () => {
        await page.goto(`${origin}/sites?view=map`);
        await expect(map).toBeVisible();
        await expect(dot(5)).toHaveAttribute('data-access', 'down');
        await shot('default');
        await page.locator('summary', { hasText: 'Sites by group' }).click();
        const list = page.getByRole('list', {
          name: `Sites in North ${label}`,
        });
        await expect(list.getByRole('listitem')).toHaveCount(20);
        await expect(
          list.getByRole('listitem').filter({ hasText: name(0) }),
        ).toContainText('Protected');
        await page
          .getByRole('list', { name: `Sites in North ${label}` })
          .scrollIntoViewIfNeeded();
        await shot('text-list');
      });

      await test.step('a hub filters the map to its group; List keeps it', async () => {
        // From the keyboard: a pinned site's label can lie over the hub.
        await map
          .getByRole('link', { name: `North ${label}: 20 sites` })
          .press('Enter');
        await expect(page).toHaveURL(`${origin}/sites?g=${north.id}&view=map`);
        await expect(
          page.getByRole('group', { name: 'Filters' }),
        ).toContainText(`Group: North ${label}`);
        await expect(dot(0)).not.toHaveClass(/map-dim/);
        await expect(dot(20)).toHaveClass(/map-dim/);
        await page
          .getByRole('navigation', { name: 'Show sites as' })
          .getByRole('link', { name: 'List' })
          .click();
        await expect(page).toHaveURL(`${origin}/sites?g=${north.id}`);
        await expect(
          page.getByRole('heading', { level: 1, name: `North ${label}` }),
        ).toBeVisible();
      });
    } finally {
      admin('restore-site', name(5));
    }
  });

  test(`a site named map keeps /sites/map as its Overview (${scheme})`, async ({
    page,
  }) => {
    await page.emulateMedia({ colorScheme: scheme });
    // Every project and theme shares the one site named map.
    const res = await page.request.post(`${origin}/api/v0/sites`, {
      data: { name: 'map' },
      headers,
    });
    expect([201, 409], `create map: ${res.status()}`).toContain(res.status());
    expect((await page.request.get(`${origin}/api/v0/sites/map`)).ok()).toBe(
      true,
    );
    await page.goto(`${origin}/sites/map`);
    await expect(
      page.getByRole('heading', { level: 1, name: 'map' }),
    ).toBeVisible();
    await expect(
      page
        .getByRole('navigation', { name: 'Site sections' })
        .getByRole('link', { name: 'Overview' }),
    ).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('group', { name: /^Map of / })).toHaveCount(0);
  });
}
