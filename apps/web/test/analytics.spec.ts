// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The analytics page (V5.12), against a real server: an unvisited site
// shows the empty state; then a group of two synthetic sites gets public,
// refused and share-link requests, and the page counts them by day, way
// in, tag, site, link, path and browser, each with a table view and
// keyboard tooltips. No token may be on the page or in a screenshot.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
test.use({ storageState: firstOwnerState });

const headers = { Origin: origin };
const tokenShape = /wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43}/;

/** No sideways scroll, no token, then a full-page screenshot. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  expect(await page.content()).not.toMatch(tokenShape);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`analytics: empty, then requests by scope (${scheme})`, async ({
    page,
    request,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `analytics-${step}-${scheme}`);
    const prefix = `an-${info.project.name}-${scheme}`;
    const open = `${prefix}-open`;
    const shut = `${prefix}-shut`;
    const quiet = `${prefix}-quiet`;
    const label = 'Board pack';
    const api = page.request;
    const main = page.getByRole('navigation', { name: 'Main' });

    // Two sites in a new group, one of them tagged, and one with no requests.
    const group = await api.post(`${origin}/api/v0/groups`, {
      data: { name: prefix },
      headers,
    });
    expect(group.status()).toBe(201);
    const { id: groupId } = (await group.json()) as { id: string };
    const tag = await api.post(`${origin}/api/v0/tags`, {
      data: { name: prefix, colour: 3 },
      headers,
    });
    expect(tag.status()).toBe(201);
    const { id: tagId } = (await tag.json()) as { id: string };
    for (const site of [open, shut, quiet]) {
      await createSite(api, site);
      await publish(api, site, 'Quarterly notes');
    }
    await protect(api, shut);
    for (const [site, tagIds] of [
      [open, [tagId]],
      [shut, []],
    ] as const) {
      const res = await api.put(`${origin}/api/v0/sites/${site}/organisation`, {
        data: { groupId, tagIds },
        headers,
      });
      expect(res.status()).toBe(200);
    }

    await test.step('the rail leads to Analytics', async () => {
      await page.goto(`${origin}/`);
      await main.getByRole('link', { name: 'Analytics' }).click();
      await expect(page).toHaveURL(`${origin}/analytics`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Analytics' }),
      ).toBeVisible();
      await expect(page).toHaveTitle('Analytics · Wardlock');
      await expect(
        main.getByRole('link', { name: 'Analytics' }),
      ).toHaveAttribute('aria-current', 'page');
    });

    await test.step('a site with no requests shows the empty state', async () => {
      await page.goto(`${origin}/analytics?site=${quiet}`);
      await expect(
        page.getByRole('heading', { name: 'No requests yet' }),
      ).toBeVisible();
      await expect(page.getByText(`Requests to ${quiet} over`)).toBeVisible();
      await shot('empty');
    });

    await test.step('requests to the group are counted', async () => {
      const site = await api.get(`${origin}/api/v0/sites/${open}`);
      const { url: openUrl } = (await site.json()) as { url: string };
      for (let i = 0; i < 3; i++)
        expect((await request.get(openUrl)).status()).toBe(200);
      const created = await api.post(`${origin}/api/v0/sites/${shut}/links`, {
        data: { label },
        headers,
      });
      expect(created.status()).toBe(201);
      const { url } = (await created.json()) as { url: string };
      const bare = new URL(url);
      bare.search = '';
      expect((await request.get(bare.href, { maxRedirects: 0 })).status()).toBe(
        401,
      );
      expect((await request.get(url, { maxRedirects: 0 })).status()).toBe(200);

      // The rollup is written with the access log, about once a second.
      await page.goto(`${origin}/analytics?g=${groupId}`);
      const allowed = page
        .getByText('Allowed requests', { exact: true })
        .locator('xpath=..');
      await expect(async () => {
        await page.reload();
        await expect(allowed.locator('dd').first()).toHaveText('4', {
          timeout: 1000,
        });
      }).toPass();
      await expect(
        page.getByText(
          `Requests to the ${prefix} group over the last 30 days.`,
        ),
      ).toBeVisible();
      const refused = page
        .getByText('Refused requests', { exact: true })
        .locator('xpath=..');
      await expect(refused.locator('dd')).toHaveText(['1', '20% of requests']);

      const via = page.getByRole('list', { name: 'How visitors got in' });
      await expect(via.locator('.wl-bar-name')).toHaveText([
        'Public',
        'Share link',
      ]);
      await expect(
        page.getByText('1 request refused, not counted here.'),
      ).toBeVisible();
      await expect(
        page
          .getByRole('list', { name: 'Top share links' })
          .getByRole('link', { name: label }),
      ).toBeVisible();
      await expect(
        page
          .getByRole('list', { name: 'By tag' })
          .getByRole('link', { name: prefix }),
      ).toBeVisible();
      await expect(page.getByRole('region', { name: 'By group' })).toHaveCount(
        0,
      );
      await expect(page.getByText(/visits?\b/i)).toHaveCount(0);
      await shot('group');
    });

    await test.step('tooltips answer the keyboard', async () => {
      const plot = page.getByRole('group', {
        name: 'Requests per day, last 30 days',
      });
      await plot.locator('[tabindex="0"]').focus();
      const tip = plot.getByRole('tooltip');
      await expect(tip).toBeVisible();
      await expect(tip).toContainText('Allowed 4');
      await expect(tip).toContainText('Refused 1');
      await page.keyboard.press('Escape');
      await expect(tip).toBeHidden();

      const browsers = page.getByRole('list', { name: 'Browsers and tools' });
      await browsers.getByRole('listitem').first().focus();
      await expect(browsers.getByRole('tooltip')).toBeVisible();
    });

    await test.step('each chart opens as a table', async () => {
      const perDay = page.getByRole('region', { name: 'Requests per day' });
      await perDay.getByText('View as table').click();
      const table = perDay.getByRole('table', {
        name: 'Requests per day, last 30 days',
      });
      await expect(table).toBeVisible();
      await expect(table.getByRole('row').last()).toContainText('4');
      const sites = page.getByRole('region', { name: 'Top sites' });
      await sites.getByText('View as table').click();
      await expect(
        sites.getByRole('table', { name: 'Top sites' }).getByRole('row'),
      ).toHaveText([
        /Site\s*Allowed\s*Refused/,
        new RegExp(`${open}`),
        new RegExp(`${shut}`),
      ]);
      await shot('tables');
    });

    await test.step('the range and scope change from the toolbar', async () => {
      await page.locator('label', { hasText: '7 days' }).click();
      await expect(page).toHaveURL(`${origin}/analytics?g=${groupId}&days=7`);
      await expect(
        page.getByText(`Requests to the ${prefix} group over the last 7 days.`),
      ).toBeVisible();
      await page
        .getByRole('combobox', { name: 'Sites' })
        .selectOption(`t:${tagId}`);
      await expect(page).toHaveURL(`${origin}/analytics?t=${tagId}&days=7`);
      await expect(
        page.getByText(
          `Requests to sites tagged ${prefix} over the last 7 days.`,
        ),
      ).toBeVisible();
      await page
        .getByRole('list', { name: 'Top sites' })
        .getByRole('link', { name: open })
        .click();
      await expect(page).toHaveURL(`${origin}/analytics?site=${open}&days=7`);
      await expect(
        page
          .getByText('Allowed requests', { exact: true })
          .locator('xpath=..')
          .locator('dd')
          .first(),
      ).toHaveText('3');
    });
  });
}
