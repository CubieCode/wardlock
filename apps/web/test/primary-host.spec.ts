// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Choosing a site's primary hostname on the Domains tab (D3.7), against a
// real server. The local server cannot pass a DNS check, so a custom domain
// stays pending: it is offered but cannot be chosen, and the site's own
// address is the hostname chosen; the server's answer is read back.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
test.use({ storageState: firstOwnerState });

/** No sideways scroll, then a full-page screenshot. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`primary hostname: choose, see it in effect, turn it off (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `primary-${step}-${scheme}`);
    const site = `ph-${info.project.name}-${scheme}`;
    const own = `${site}.wardlock.localhost`;
    // Under .test (RFC 6761), so no real DNS name is ever involved.
    const custom = `www.${site}.example.test`;
    const picker = page.getByRole('region', { name: 'Primary hostname' });
    const save = picker.getByRole('button', { name: 'Save primary hostname' });
    const primaryHost = async () => {
      const res = await page.request.get(`${origin}/api/v0/sites/${site}`);
      expect(res.status()).toBe(200);
      return ((await res.json()) as { primaryHost?: string }).primaryHost;
    };

    await test.step('with no primary, only served hostnames can be chosen', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Field guide');
      const added = await page.request.post(
        `${origin}/api/v0/sites/${site}/domains`,
        { data: { hostname: custom }, headers: { Origin: origin } },
      );
      expect(added.status()).toBe(201);
      await page.goto(`${origin}/sites/${site}`);
      await siteTab(page, 'Domains');
      await expect(picker).toContainText(
        'No primary hostname: every hostname serves the site.',
      );
      await expect(picker.getByRole('radio', { name: 'None' })).toBeChecked();
      await expect(picker.getByRole('radio', { name: custom })).toBeDisabled();
      await expect(save).toBeDisabled();
      await picker.scrollIntoViewIfNeeded();
      await shot('none');
    });

    await test.step('choosing the site’s own address saves it', async () => {
      await picker.getByRole('radio', { name: own }).check();
      await save.click();
      await expect(
        picker.getByText(`Saved. ${own} is the primary hostname.`),
      ).toBeVisible();
      await expect(picker).toContainText(`${own} is the primary hostname.`);
      await expect(save).toBeDisabled();
      expect(await primaryHost()).toBe(own);
      await shot('chosen');
    });

    await test.step('the overview marks it, and it survives a reload', async () => {
      await siteTab(page, 'Overview');
      const addresses = page.getByRole('list', { name: 'Addresses' });
      await expect(
        addresses.getByRole('listitem').filter({ hasText: own }),
      ).toContainText('Primary');
      await siteTab(page, 'Domains');
      await page.reload();
      await expect(picker.getByRole('radio', { name: own })).toBeChecked();
    });

    await test.step('choosing None turns redirects off', async () => {
      await picker.getByRole('radio', { name: 'None' }).check();
      await save.click();
      await expect(
        picker.getByText(
          'Saved. Every hostname serves the site, with no redirects.',
        ),
      ).toBeVisible();
      expect(await primaryHost()).toBeUndefined();
    });
  });
}
