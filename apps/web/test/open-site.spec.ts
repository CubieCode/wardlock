// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Open site from the dashboard (D3.6), against a real server: a protected
// site opens in a new tab through a fresh 10-minute share link that then
// shows in the link list; a public site opens at its address. The token
// is never on the dashboard page, so screenshots are safe.
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
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`open site: protected through a share link, public directly (${scheme})`, async ({
    page,
    context,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `open-site-${step}-${scheme}`);
    const site = `o-${info.project.name}-${scheme}`;
    const open = page.getByRole('button', { name: 'Open site' });

    await test.step('a protected site opens in a new tab through a link', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Launch notes');
      await protect(page.request, site);
      await page.goto(`${origin}/sites/${site}`);
      await expect(
        page.getByRole('heading', { name: 'No share links yet' }),
      ).toBeVisible();
      await shot('protected');

      const [tab] = await Promise.all([
        context.waitForEvent('page'),
        open.click(),
      ]);
      await expect(tab).toHaveURL(
        new RegExp(`^http://${site}\\.wardlock\\.localhost:`),
      );
      await expect(tab.getByText('Launch notes')).toBeVisible();
      // The dashboard cannot reach into the site's tab.
      expect(await tab.evaluate(() => window.opener)).toBeNull();
      await tab.close();

      const row = page
        .getByRole('list', { name: 'Share links' })
        .getByRole('listitem');
      await expect(row).toHaveCount(1);
      await expect(row).toContainText('Opened from dashboard');
      await expect(row).toContainText('Expires in 10 minutes');
      await expect(row).toContainText('Unlimited uses');
      await expect(page.getByRole('alert')).toHaveCount(0);
      await shot('protected-opened');
    });

    await test.step('a public site opens at its address', async () => {
      const res = await page.request.put(
        `${origin}/api/v0/sites/${site}/policies`,
        {
          data: { path: '/', visibility: 'public', methods: [] },
          headers: { Origin: origin },
        },
      );
      expect(res.status()).toBe(200);
      await page.reload();
      await expect(open).toBeVisible();
      const [tab] = await Promise.all([
        context.waitForEvent('page'),
        open.click(),
      ]);
      await expect(tab).toHaveURL(
        new RegExp(`^http://${site}\\.wardlock\\.localhost:\\d+/$`),
      );
      await expect(tab.getByText('Launch notes')).toBeVisible();
      await tab.close();
      // No second link was made.
      await expect(
        page.getByRole('list', { name: 'Share links' }).getByRole('listitem'),
      ).toHaveCount(1);
    });
  });
}
