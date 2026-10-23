// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Operator controls (A2.13) in a real browser against a real server: a
// visitor reports a protected site from its login page, the operator takes
// it down with `wardlockd admin`, visitors then get the unavailable page
// (which links to the report form) and the owner's dashboard says the site
// is taken down; restoring brings it back. The report, the site and every
// address are synthetic.
import { execFileSync } from 'node:child_process';
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, messageWith, schemes } from './mail.js';
import { createSite, protect, publish } from './publish.js';
import { siteTab } from './site-page.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
const wardlockd = process.env.WARDLOCK_ACCEPTANCE_WARDLOCKD;
const adminEnv = process.env.WARDLOCK_ACCEPTANCE_ADMIN_ENV;
if (!origin || !wardlockd || !adminEnv)
  throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;
// Signed in as the first owner, whose session setup saved: email-code sends
// are limited per IP, and the other specs use them.
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
    fullPage: true,
  });
}

for (const scheme of schemes) {
  test(`operator controls: report, take down and restore (${scheme})`, async ({
    page,
    browser,
  }, info) => {
    const site = `op-${info.project.name}-${scheme}`;
    const siteURL = `http://${site}.wardlock.localhost:${port}`;

    await test.step('the owner publishes a protected site', async () => {
      await createSite(page.request, site);
      await publish(page.request, site, 'Harbour plans');
      await protect(page.request, site);
    });

    const visitor = await browser.newContext({ colorScheme: scheme });
    try {
      const v = await visitor.newPage();
      await v.setViewportSize(page.viewportSize()!);

      await test.step('a visitor reports the site from its login page', async () => {
        await v.goto(`${siteURL}/plans/`);
        await v.getByRole('link', { name: 'Report this site' }).click();
        await expect(
          v.getByRole('heading', { level: 1, name: 'Report this site' }),
        ).toBeVisible();
        // On Wardlock's dashboard origin, out of the reported site's reach.
        expect(new URL(v.url()).origin).toBe(origin);
        await expect(v.getByRole('textbox', { name: 'Page' })).toHaveValue(
          `${site}.wardlock.localhost/plans/`,
        );
        await check(v, info, `report-form-${scheme}`);
        await v.getByRole('button', { name: 'Send report' }).click();
        // The browser asks for a reason first; nothing is sent.
        await expect(
          v.getByRole('heading', { level: 1, name: 'Report this site' }),
        ).toBeVisible();
        await v.getByRole('radio', { name: 'Phishing or a scam' }).check();
        await v
          .getByRole('textbox', { name: 'Details (optional)' })
          .fill('A fake bank sign-in page.');
        await v
          .getByRole('textbox', { name: 'Your email (optional)' })
          .fill(`reporter-${info.project.name}-${scheme}@example.test`);
        await v.getByRole('button', { name: 'Send report' }).click();
        await expect(
          v.getByRole('heading', { level: 1, name: 'Thanks for your report' }),
        ).toBeVisible();
        await check(v, info, `report-sent-${scheme}`);
        const report = await messageWith(
          'abuse@example.test',
          `Abuse report: ${site}.wardlock.localhost`,
        );
        expect(report).toContain('A fake bank sign-in page.');
      });

      await test.step('the operator takes the site down', async () => {
        expect(admin('takedown-site', site)).toContain(`Took down ${site}`);
        await expect
          .poll(async () => (await v.goto(`${siteURL}/`))?.status(), {
            timeout: 10_000,
          })
          .toBe(403);
        await expect(
          v.getByRole('heading', {
            level: 1,
            name: 'This site is unavailable',
          }),
        ).toBeVisible();
        await expect(v.getByText('Harbour plans')).toHaveCount(0);
        await check(v, info, `unavailable-${scheme}`);
        await v.getByRole('link', { name: 'Report this site' }).click();
        await expect(
          v.getByRole('heading', { level: 1, name: 'Report this site' }),
        ).toBeVisible();
      });
    } finally {
      await visitor.close();
    }

    await test.step('the owner sees the site is taken down', async () => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.goto(`${origin}/sites/${site}`);
      await expect(
        page.getByRole('heading', { level: 1, name: site }),
      ).toBeVisible();
      await expect(page.getByText('Taken down', { exact: true })).toBeVisible();
      await expect(
        page.getByText(
          'The operator of this server has taken this site down.',
          {
            exact: true,
          },
        ),
      ).toBeVisible();
      await expect(page.getByRole('button', { name: 'Open site' })).toHaveCount(
        0,
      );
      await check(page, info, `dashboard-taken-down-${scheme}`);
      // Nothing to roll back to.
      await siteTab(page, 'Deploys');
      await expect(
        page.getByRole('list', { name: 'Deploy history' }),
      ).toBeVisible();
      await expect(
        page.getByRole('button', { name: /^Roll back/ }),
      ).toHaveCount(0);
      await check(page, info, `dashboard-taken-down-deploys-${scheme}`);
      // Its domains are frozen: no field to add one.
      await siteTab(page, 'Domains');
      await expect(
        page.getByText(/its domains stay as they are/),
      ).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Add domain' }),
      ).toHaveCount(0);
      // And it cannot be torn down.
      await siteTab(page, 'Settings');
      await expect(
        page.getByText('Ask the operator of this server about it.', {
          exact: false,
        }),
      ).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Tear down site' }),
      ).toHaveCount(0);
      await check(page, info, `dashboard-taken-down-settings-${scheme}`);
    });

    await test.step('restoring serves the site again', async () => {
      admin('restore-site', site);
      await page.goto(`${origin}/sites/${site}`);
      await expect(
        page.getByRole('button', { name: 'Open site' }),
      ).toBeVisible();
      // The server notices within a few seconds; the site is protected again.
      await expect
        .poll(
          async () =>
            (
              await page.request.get(`${siteURL}/`, { maxRedirects: 0 })
            ).status(),
          { timeout: 10_000 },
        )
        .toBe(401);
    });
  });
}
