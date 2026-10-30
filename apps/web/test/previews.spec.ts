// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Previews on the site page (D3.7), against a real server: the empty
// state's command, a preview deployed through the API with redirect rules,
// opening it at its own address, promoting it through its confirmation,
// the deploy history marking it with its rules, and deleting it.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;
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
  test(`previews: open, promote, history and delete (${scheme})`, async ({
    page,
    context,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `previews-${step}-${scheme}`);
    const site = `pv-${info.project.name}-${scheme}`;
    const card = page.getByRole('region', { name: 'Previews' });
    const row = card.getByRole('listitem', { name: 'pr-1' });

    await test.step('a site with no previews shows the command for one', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Field guide');
      await page.goto(`${origin}/sites/${site}`);
      await siteTab(page, 'Previews');
      await expect(
        card.getByRole('heading', { name: 'No previews yet' }),
      ).toBeVisible();
      await expect(card.getByLabel('Command', { exact: true })).toHaveValue(
        `wardlock deploy dist ${site} --preview pr-42`,
      );
      await shot('empty');
    });

    await test.step('a preview deploy is listed with its address', async () => {
      await publish(page.request, site, 'Field guide redesign', {
        preview: 'pr-1',
        redirects: [{ from: '/old', to: '/' }],
      });
      await page.reload();
      await expect(row).toBeVisible();
      await expect(row).toContainText(`${site}--pr-1.wardlock.localhost`);
      await expect(row).toContainText('Field guide redesign');
      await expect(row).toContainText(/Expires in (2 weeks|14 days)/);
      await shot('listed');
    });

    await test.step('Open shows the preview at its own address', async () => {
      const [tab] = await Promise.all([
        context.waitForEvent('page'),
        row.getByRole('button', { name: 'Open preview pr-1' }).click(),
      ]);
      await expect(tab).toHaveURL(
        new RegExp(`^http://${site}--pr-1\\.wardlock\\.localhost:`),
      );
      await expect(tab.getByText('Field guide redesign')).toBeVisible();
      await tab.close();
    });

    await test.step('promoting confirms which deploy goes live', async () => {
      await row.getByRole('button', { name: 'Promote pr-1' }).click();
      const dialog = page.getByRole('dialog', { name: 'Promote pr-1?' });
      await expect(dialog).toBeVisible();
      await expect(dialog).toContainText('Live now');
      await expect(dialog).toContainText('Field guide');
      await expect(dialog).toContainText('Goes live from pr-1');
      await expect(dialog).toContainText('Field guide redesign');
      await shot('promote');
      await dialog
        .getByRole('button', { name: 'Put this deploy live' })
        .click();
      await expect(dialog).toBeHidden();
      await expect(card.getByText(/^Promoted pr-1\. Deploy/)).toBeVisible();
      await expect(row.getByText('Live on site')).toBeVisible();
      await expect(
        row.getByRole('button', { name: 'Promote pr-1' }),
      ).toHaveCount(0);
      await shot('promoted');
      // The live site serves the preview's deploy now.
      const live = await page.request.get(
        `http://${site}.wardlock.localhost:${port}/`,
      );
      expect(await live.text()).toContain('Field guide redesign');
    });

    await test.step('the history marks the preview deploy and its rules', async () => {
      await siteTab(page, 'Deploys');
      const history = page.getByRole('list', { name: 'Deploy history' });
      const newest = history.locator(':scope > li').first();
      await expect(newest).toContainText('Live');
      await expect(newest).toContainText('Preview pr-1');
      await newest.getByText('1 redirect', { exact: true }).click();
      const redirects = newest.getByRole('list', { name: /^Redirects of/ });
      await expect(redirects).toBeVisible();
      await expect(redirects).toContainText('/old');
      await expect(redirects).toContainText('301 permanent');
      await shot('history');
    });

    await test.step('deleting a preview confirms and removes it', async () => {
      await siteTab(page, 'Previews');
      await row.getByRole('button', { name: 'Delete preview pr-1' }).click();
      const dialog = page.getByRole('dialog', { name: 'Delete preview pr-1?' });
      await expect(dialog).toBeVisible();
      await dialog.getByRole('button', { name: 'Delete preview' }).click();
      await expect(dialog).toBeHidden();
      await expect(card.getByText('Deleted preview pr-1.')).toBeVisible();
      await expect(
        card.getByRole('heading', { name: 'No previews yet' }),
      ).toBeVisible();
    });
  });
}
