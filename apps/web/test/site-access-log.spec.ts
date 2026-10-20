// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// A site's access log (A2.12), against a real server: the owner opens it
// from the site page, sees it empty, then sees a refused visit and one
// through a share link, named by the link's label, and follows the link to
// its own log. No token may be on the page, in the title or in a
// screenshot.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
test.use({ storageState: firstOwnerState });

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
  expect(await page.title()).not.toMatch(/wl_/);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`site access log: empty, refused and link visits (${scheme})`, async ({
    page,
    request,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `site-log-${step}-${scheme}`);
    const site = `al-${info.project.name}-${scheme}`;
    const label = 'Board pack';

    await test.step('the owner opens an unvisited site’s access log', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Board papers');
      await protect(page.request, site);
      await page.goto(`${origin}/sites/${site}`);
      await page.getByRole('link', { name: 'View access log' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Access log' }),
      ).toBeVisible();
      await expect(page).toHaveTitle(`Access log · ${site} · Wardlock`);
      await expect(
        page.getByRole('heading', { name: 'No visits yet' }),
      ).toBeVisible();
      await shot('empty');
    });

    await test.step('a refused visit and a link visit are listed', async () => {
      const created = await page.request.post(
        `${origin}/api/v0/sites/${site}/links`,
        { data: { label }, headers: { Origin: origin } },
      );
      expect(created.status()).toBe(201);
      const { url } = (await created.json()) as { url: string };
      const bare = new URL(url);
      bare.search = '';
      // A client with no cookie: refused without the token, let in with it.
      const refused = await request.get(bare.href, { maxRedirects: 0 });
      expect(refused.status()).toBe(401);
      const opened = await request.get(url, { maxRedirects: 0 });
      expect(opened.status()).toBe(200);

      // Entries are written in batches about once a second.
      const table = page.getByRole('table', { name: `Visits to ${site}` });
      await expect(async () => {
        await page.reload();
        await expect(table.getByRole('row')).toHaveCount(3, { timeout: 1000 });
      }).toPass();
      const rows = table.getByRole('row');
      await expect(rows.nth(1)).toContainText('Opened · 200');
      await expect(rows.nth(1)).toContainText(`Share link ${label}`);
      await expect(rows.nth(2)).toContainText('Refused · 401');
      await expect(rows.nth(2)).toContainText('Not let in');
      await shot('visits');
    });

    await test.step('the link’s label leads to its own log', async () => {
      await page
        .getByRole('table', { name: `Visits to ${site}` })
        .getByRole('link', { name: label })
        .click();
      await expect(
        page.getByRole('heading', { level: 1, name: label }),
      ).toBeVisible();
      await page.getByRole('link', { name: `Back to ${site}` }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: site }),
      ).toBeVisible();
    });
  });
}
