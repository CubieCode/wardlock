// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { foreignSite, mailbox, schemes, sitesOwnerFor } from './mail.js';
import { createSite, protect, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;

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

/** What a visitor sees at the site's own address. */
async function served(page: Page, site: string) {
  const visitor = await page.context().newPage();
  try {
    const res = await visitor.goto(
      `http://${site}.wardlock.localhost:${port}/`,
    );
    return { status: res?.status(), text: await visitor.textContent('body') };
  } finally {
    await visitor.close();
  }
}

for (const scheme of schemes) {
  test(`sites: list, empty state, rollback, teardown and not found (${scheme})`, async ({
    page,
    context,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], {
      origin,
    });
    const inbox = mailbox(sitesOwnerFor(info.project.name, scheme));
    const shot = (step: string) => check(page, info, `sites-${step}-${scheme}`);
    const name = `s-${info.project.name}-${scheme}`;
    const locked = `p-${info.project.name}-${scheme}`;

    await test.step('a new owner sees the empty state', async () => {
      // Sign in by email code rather than the invitation link: link
      // redemptions are limited per IP, and the other specs use them.
      const email = sitesOwnerFor(info.project.name, scheme);
      await inbox.signInLink(); // skip the invitation
      await page.goto(`${origin}/sign-in`);
      await page.getByRole('textbox', { name: 'Email address' }).fill(email);
      await page.getByRole('button', { name: 'Send code' }).click();
      await page
        .getByRole('textbox', { name: 'Sign-in code' })
        .fill(await inbox.code());
      await page.getByRole('button', { name: 'Sign in' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Overview' }),
      ).toBeVisible();
      const menu = page.getByRole('button', { name: 'Menu' });
      if (await menu.isVisible()) await menu.click();
      await page
        .getByRole('navigation', { name: 'Main' })
        .getByRole('link', { name: 'Sites' })
        .click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Sites' }),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { level: 2, name: 'No sites yet' }),
      ).toBeVisible();
      // The other owner's site never appears.
      await expect(page.getByText(foreignSite)).toHaveCount(0);
      await shot('empty');
    });

    let first = '';
    let second = '';
    await test.step('the list shows each site with its policy and deploy', async () => {
      await createSite(page.request, name);
      first = await publish(page.request, name, 'First version');
      second = await publish(page.request, name, 'Second version');
      await createSite(page.request, locked);
      await protect(page.request, locked);
      await page.reload();
      const list = page.getByRole('list', { name: 'Sites' });
      const rows = list.getByRole('listitem');
      await expect(rows).toHaveCount(2);
      await expect(rows.nth(0)).toContainText(locked);
      await expect(rows.nth(0)).toContainText('Protected');
      await expect(rows.nth(0)).toContainText('Not deployed yet');
      await expect(rows.nth(1)).toContainText(`${name}.wardlock.localhost`);
      await expect(rows.nth(1)).toContainText('Public');
      await expect(rows.nth(1)).toContainText(
        /Deployed (just now|now|\d+ minutes? ago)/,
      );
      await shot('list');
    });

    await test.step('the detail page copies the address', async () => {
      await page.getByRole('link', { name, exact: true }).click();
      await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
      await expect(
        page.getByRole('textbox', { name: 'Site address' }),
      ).toHaveValue(`http://${name}.wardlock.localhost:${port}/`);
      await page.getByRole('button', { name: 'Copy site address' }).click();
      await expect(page.getByText('Site address copied.')).toBeVisible();
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
        `http://${name}.wardlock.localhost:${port}/`,
      );
      const history = page.getByRole('list', { name: 'Deploy history' });
      await expect(history.getByRole('listitem')).toHaveCount(2);
      await expect(history.getByRole('listitem').nth(0)).toContainText('Live');
      await expect(history.getByRole('listitem').nth(0)).toContainText(
        'Second version',
      );
      await shot('detail');
      expect((await served(page, name)).text).toContain('Second version');
    });

    await test.step('rolling back asks first, then makes the earlier deploy live', async () => {
      await page
        .getByRole('button', { name: `Roll back to deploy ${first.slice(-8)}` })
        .click();
      const dialog = page.getByRole('dialog', { name: `Roll back ${name}?` });
      await expect(dialog).toContainText('Live now');
      await expect(dialog).toContainText('Second version');
      await expect(dialog).toContainText('Goes live');
      await expect(dialog).toContainText('First version');
      // Focus starts on Cancel, the safe choice; Escape closes.
      await expect(
        dialog.getByRole('button', { name: 'Cancel' }),
      ).toBeFocused();
      await shot('rollback-dialog');
      await page.keyboard.press('Escape');
      await expect(dialog).toBeHidden();
      expect((await served(page, name)).text).toContain('Second version');

      await page
        .getByRole('button', { name: `Roll back to deploy ${first.slice(-8)}` })
        .click();
      await dialog
        .getByRole('button', { name: 'Roll back to this deploy' })
        .click();
      await expect(
        page.getByText(`Rolled back. Deploy ${first.slice(-8)} is live.`),
      ).toBeVisible();
      const history = page.getByRole('list', { name: 'Deploy history' });
      await expect(history.getByRole('listitem').nth(1)).toContainText('Live');
      await expect(
        page.getByRole('button', {
          name: `Roll back to deploy ${second.slice(-8)}`,
        }),
      ).toBeVisible();
      await shot('rolled-back');
      expect((await served(page, name)).text).toContain('First version');
    });

    await test.step('tearing down needs the site name, then frees it', async () => {
      await page.getByRole('button', { name: 'Tear down site' }).click();
      const dialog = page.getByRole('dialog', { name: `Tear down ${name}?` });
      await expect(dialog).toContainText('This cannot be undone.');
      const confirm = dialog.getByRole('button', { name: 'Tear down site' });
      const field = dialog.getByRole('textbox', {
        name: `Type ${name} to confirm`,
      });
      await expect(field).toBeFocused();
      await expect(confirm).toBeDisabled();
      await field.fill(name.slice(0, -1));
      await expect(confirm).toBeDisabled();
      await field.fill(name);
      await shot('teardown-dialog');
      await confirm.click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Sites' }),
      ).toBeVisible();
      expect(page.url()).toBe(`${origin}/sites`);
      await expect(
        page.getByText(`${name} was torn down. Its name is free to use again.`),
      ).toBeVisible();
      await expect(
        page.getByRole('list', { name: 'Sites' }).getByRole('listitem'),
      ).toHaveCount(1);
      await shot('torn-down');
      expect((await served(page, name)).status).toBe(404);
    });

    await test.step('a foreign site looks exactly like a missing one', async () => {
      const seen: string[] = [];
      for (const site of [foreignSite, 'no-such-site']) {
        await page.goto(`${origin}/sites/${site}`);
        await expect(
          page.getByRole('heading', { level: 1, name: 'Site not found' }),
        ).toBeVisible();
        seen.push((await page.getByRole('main').textContent()) ?? '');
      }
      expect(seen[0]).toBe(seen[1]);
      await shot('not-found');
    });
  });
}
