// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Share links in the dashboard (D3.3), against a real server: create a link
// and see its URL once, copy it, open it as a visitor, read its access log,
// then rotate and revoke it through their confirmations. The token must
// never be on the page after its dialog closes, in the title or in a
// screenshot; screenshots are taken only while no token is shown.
import {
  expect,
  test,
  type APIRequestContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
// Signed in as the first owner, whose session setup saved: email-code sends
// are limited per IP, and the other specs use them.
test.use({ storageState: firstOwnerState });

const tokenShape = /wl_[a-z2-7]{10}_[A-Za-z0-9_-]{43}/;

/** Fails if any token is in the page's markup or title. */
async function expectNoToken(page: Page) {
  expect(await page.content()).not.toMatch(tokenShape);
  expect(await page.title()).not.toMatch(/wl_/);
}

/** No sideways scroll, no token, then a full-page screenshot. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  await expectNoToken(page);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

/** What a client with no site cookie (curl, an AI fetcher) gets at `url`. */
async function visit(request: APIRequestContext, url: string) {
  const res = await request.get(url, { maxRedirects: 0 });
  return { status: res.status(), text: await res.text() };
}

for (const scheme of schemes) {
  test(`share links: create, copy, access log, rotate and revoke (${scheme})`, async ({
    page,
    context,
    browser,
    request,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], {
      origin,
    });
    const shot = (step: string) => check(page, info, `links-${step}-${scheme}`);
    const site = `l-${info.project.name}-${scheme}`;
    const label = 'Review copy';
    const row = () =>
      page.getByRole('list', { name: 'Share links' }).getByRole('listitem');

    await test.step('the owner opens a protected site', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Quarterly plan');
      await protect(page.request, site);
      await page.goto(`${origin}/sites/${site}`);
      await shot('overview');
      await siteTab(page, 'Links');
      await expect(
        page.getByRole('heading', { name: 'No share links yet' }),
      ).toBeVisible();
      await shot('empty');
    });

    let url = '';
    await test.step('creating a link shows its URL once, with Copy', async () => {
      await page.getByRole('button', { name: 'Create link' }).click();
      const dialog = page.getByRole('dialog', {
        name: `Create a share link for ${site}`,
      });
      await dialog.getByRole('textbox', { name: 'Label' }).fill(label);
      await dialog
        .getByRole('combobox', { name: 'Expires' })
        .selectOption({ label: 'In 7 days' });
      await shot('create');
      await dialog.getByRole('button', { name: 'Create link' }).click();

      const reveal = page.getByRole('dialog', { name: 'Copy your share link' });
      await expect(reveal).toBeVisible();
      url = await reveal
        .getByRole('textbox', { name: 'Share link' })
        .inputValue();
      expect(url).toMatch(tokenShape);
      expect(url.startsWith(`http://${site}.wardlock.localhost:`)).toBe(true);
      expect(await page.title()).not.toMatch(/wl_/);
      // The one screenshot with a token on the page: the field is masked,
      // so the token is never drawn into the image.
      await page.screenshot({
        path: info.outputPath(`links-reveal-${scheme}.png`),
        fullPage: true,
        mask: [reveal.getByRole('textbox', { name: 'Share link' })],
      });
      await reveal.getByRole('button', { name: 'Copy share link' }).click();
      await expect(reveal.getByText('Share link copied.')).toBeVisible();
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
        url,
      );
      await reveal.getByRole('button', { name: 'Done' }).click();
      await expect(reveal).toBeHidden();

      await expect(row()).toHaveCount(1);
      await expect(row()).toContainText(label);
      await expect(row()).toContainText('Expires in 7 days');
      await expect(row()).toContainText('Not opened yet');
      await shot('list');
    });

    await test.step('a visitor opens it and the access log shows the visit', async () => {
      const guest = await browser.newContext({ colorScheme: scheme });
      try {
        const g = await guest.newPage();
        const res = await g.goto(url);
        expect(res?.status()).toBe(200);
        await expect(g.getByText('Quarterly plan')).toBeVisible();
      } finally {
        await guest.close();
      }

      await row()
        .getByRole('link', { name: `Access log of “${label}”` })
        .click();
      await expect(
        page.getByRole('heading', { level: 2, name: label }),
      ).toBeVisible();
      // Entries are written in batches about once a second.
      const table = page.getByRole('table', { name: /Visits through link/ });
      await expect(async () => {
        await page.reload();
        await expect(table).toBeVisible({ timeout: 1000 });
      }).toPass();
      const visitRow = table.getByRole('row').nth(1);
      await expect(visitRow).toContainText('/');
      await expect(visitRow).toContainText('Opened · 200');
      await shot('access-log');
      await page.getByRole('link', { name: 'All share links' }).click();
      await expect(row()).toContainText('Last opened');
    });

    let rotated = '';
    await test.step('rotating asks first, then shows the new URL once', async () => {
      await row()
        .getByRole('button', { name: `Rotate “${label}”` })
        .click();
      const confirm = page.getByRole('dialog', { name: `Rotate “${label}”?` });
      await expect(confirm).toContainText('current link stops working');
      await shot('rotate-confirm');
      await confirm.getByRole('button', { name: 'Cancel' }).click();
      await expect(confirm).toBeHidden();
      expect((await visit(request, url)).status).toBe(200);

      await row()
        .getByRole('button', { name: `Rotate “${label}”` })
        .click();
      await page
        .getByRole('dialog', { name: `Rotate “${label}”?` })
        .getByRole('button', { name: 'Rotate link' })
        .click();
      const reveal = page.getByRole('dialog', { name: 'Copy the new link' });
      await expect(reveal).toBeVisible();
      rotated = await reveal
        .getByRole('textbox', { name: 'Share link' })
        .inputValue();
      expect(rotated).toMatch(tokenShape);
      expect(rotated).not.toBe(url);
      await reveal.getByRole('button', { name: 'Done' }).click();
      await expect(reveal).toBeHidden();
      await expectNoToken(page);
      expect((await visit(request, url)).status).toBe(401);
      const fresh = await visit(request, rotated);
      expect(fresh.status).toBe(200);
      expect(fresh.text).toContain('Quarterly plan');
    });

    await test.step('revoking asks first, then the link stops working', async () => {
      await row()
        .getByRole('button', { name: `Revoke “${label}”` })
        .click();
      const confirm = page.getByRole('dialog', { name: `Revoke “${label}”?` });
      await expect(confirm).toContainText('loses access');
      await shot('revoke-confirm');
      await confirm.getByRole('button', { name: 'Revoke link' }).click();
      await expect(
        page.getByText(`Revoked “${label}”. It no longer works.`),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: 'No share links yet' }),
      ).toBeVisible();
      expect((await visit(request, rotated)).status).toBe(401);
      await shot('revoked');
    });
  });
}
