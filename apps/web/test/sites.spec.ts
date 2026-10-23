// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { foreignSite, mailbox, schemes, sitesOwnerFor } from './mail.js';
import { createSite, protect, publish } from './publish.js';
import { siteTab } from './site-page.js';

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

/** What a visitor sees at the site's own address, or a path under it. */
async function served(page: Page, site: string, path = '/') {
  const visitor = await page.context().newPage();
  try {
    const res = await visitor.goto(
      `http://${site}.wardlock.localhost:${port}${path}`,
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
      second = await publish(page.request, name, 'Second version', {
        immutableAssets: true,
      });
      await createSite(page.request, locked);
      await protect(page.request, locked);
      await page.reload();
      // No groups yet, so one section: the deployed site first (sorted by
      // last deploy), then the one with nothing live.
      const list = page.getByRole('list', { name: 'Sites in No group' });
      const rows = list.getByRole('listitem');
      await expect(rows).toHaveCount(2);
      await expect(rows.nth(0)).toContainText(`${name}.wardlock.localhost`);
      await expect(rows.nth(0)).toContainText('Public');
      await expect(rows.nth(0)).toContainText('Second version');
      await expect(rows.nth(0)).toContainText(/just now|now|\d+ minutes? ago/);
      await expect(rows.nth(1)).toContainText(locked);
      await expect(rows.nth(1)).toContainText('Protected');
      await expect(rows.nth(1)).toContainText('Not deployed yet');
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
      await shot('overview');
      await siteTab(page, 'Deploys');
      await expect(page).toHaveURL(`${origin}/sites/${name}/deploys`);
      const history = page.getByRole('list', { name: 'Deploy history' });
      await expect(history.getByRole('listitem')).toHaveCount(2);
      await expect(history.getByRole('listitem').nth(0)).toContainText('Live');
      await expect(history.getByRole('listitem').nth(0)).toContainText(
        'Second version',
      );
      // Only the deploy made with the long-term caching option says so.
      await expect(history.getByRole('listitem').nth(0)).toContainText(
        'Immutable assets',
      );
      await expect(history.getByRole('listitem').nth(1)).not.toContainText(
        'Immutable assets',
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

    await test.step('a path deploy names its prefix and keeps the rest live', async () => {
      await publish(page.request, name, 'Mockup', { path: '/mockup-x/' });
      await page.reload();
      const rows = page
        .getByRole('list', { name: 'Deploy history' })
        .getByRole('listitem');
      await expect(rows).toHaveCount(3);
      await expect(rows.nth(0)).toContainText('Live');
      await expect(rows.nth(0)).toContainText('Path /mockup-x/');
      await expect(rows.nth(1)).not.toContainText('Path');
      await shot('path-deploy');
      expect((await served(page, name)).text).toContain('First version');
      expect((await served(page, name, '/mockup-x/')).text).toContain('Mockup');
    });

    await test.step('tearing down needs the site name, then frees it', async () => {
      await siteTab(page, 'Settings');
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
        page
          .getByRole('list', { name: 'Sites in No group' })
          .getByRole('listitem'),
      ).toHaveCount(1);
      await shot('torn-down');
      expect((await served(page, name)).status).toBe(404);
    });

    await test.step('a foreign site looks exactly like a missing one', async () => {
      const seen: string[] = [];
      for (const site of [
        foreignSite,
        'no-such-site',
        `${foreignSite}/settings`,
        'no-such-site/links',
      ]) {
        await page.goto(`${origin}/sites/${site}`);
        await expect(
          page.getByRole('heading', { level: 1, name: 'Site not found' }),
        ).toBeVisible();
        seen.push((await page.getByRole('main').textContent()) ?? '');
      }
      expect(new Set(seen).size).toBe(1);
      // Policies (D3.4): no editor on the page, and the API reads, sets and
      // deletes a foreign site's policies exactly as a missing site's.
      await expect(
        page.getByRole('region', { name: 'Access policies' }),
      ).toHaveCount(0);
      const policyAnswers: string[] = [];
      for (const site of [foreignSite, 'no-such-site']) {
        const base = `${origin}/api/v0/sites/${site}/policies`;
        const headers = { Origin: origin };
        for (const res of [
          await page.request.get(base),
          await page.request.put(base, {
            data: { path: '/', visibility: 'public' },
            headers,
          }),
          await page.request.delete(
            `${base}/01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e40`,
            { headers },
          ),
        ])
          policyAnswers.push(`${res.status()} ${await res.text()}`);
      }
      expect(policyAnswers.slice(0, 3)).toEqual(policyAnswers.slice(3));
      expect(policyAnswers.every((a) => a.startsWith('404 '))).toBe(true);
      await shot('not-found');
      // Custom domains (D3.5): no Domains section, and the same 404 for a
      // foreign site's domains as for a missing site's.
      await expect(
        page.getByRole('region', { name: 'Custom domains' }),
      ).toHaveCount(0);
      const answers: string[] = [];
      for (const s of [foreignSite, 'no-such-site'])
        for (const [method, path] of [
          ['GET', 'domains'],
          ['POST', 'domains'],
          ['POST', 'domains/www.example.test/verify'],
          ['DELETE', 'domains/www.example.test'],
        ] as const) {
          const res = await page.request.fetch(
            `${origin}/api/v0/sites/${s}/${path}`,
            {
              method,
              headers: { Origin: origin },
              data:
                method === 'POST'
                  ? { hostname: 'www.example.test' }
                  : undefined,
            },
          );
          answers.push(`${method} ${path} ${res.status()} ${await res.text()}`);
        }
      expect(answers.slice(0, 4)).toEqual(answers.slice(4));
      expect(answers.every((a) => / 404 /.test(a))).toBe(true);
    });
  });
}
