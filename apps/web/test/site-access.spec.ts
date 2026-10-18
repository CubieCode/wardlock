// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// A visitor's way in, in a real browser against a real server: the
// password form on the login page (A2.3) and the "Click to view" button of
// a use-limited share link (A2.2). Both pages send Referrer-Policy:
// no-referrer, so the browser posts them with Origin: null; this proves the
// edge's same-origin check still lets them through. The password is
// synthetic.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { accessOwnerFor, mailbox, schemes } from './mail.js';
import { createSite, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;
const headers = { Origin: origin };
const password = 'harbour-lights-7';

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
  test(`site access: password form and click to view (${scheme})`, async ({
    page,
    browser,
  }, info) => {
    const email = accessOwnerFor(info.project.name, scheme);
    const inbox = mailbox(email);
    const site = `a-${info.project.name}-${scheme}`;
    const siteURL = `http://${site}.wardlock.localhost:${port}`;

    await test.step('the owner protects a site with a password', async () => {
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
      await createSite(page.request, site);
      await publish(page.request, site, 'Harbour plans');
      const res = await page.request.put(
        `${origin}/api/v0/sites/${site}/policies`,
        {
          data: {
            path: '/',
            visibility: 'protected',
            methods: ['links', 'password'],
            password,
          },
          headers,
        },
      );
      expect(res.status()).toBe(200);
      expect(await res.text()).not.toContain(password);
    });

    // A visitor with no cookies, in their own browser context.
    const visitor = await browser.newContext({ colorScheme: scheme });
    try {
      const v = await visitor.newPage();
      await v.setViewportSize(page.viewportSize()!);

      await test.step('a wrong password shows the error', async () => {
        const res = await v.goto(`${siteURL}/`);
        expect(res?.status()).toBe(401);
        await expect(
          v.getByRole('heading', { level: 1, name: 'This page is private' }),
        ).toBeVisible();
        await check(v, info, `access-login-${scheme}`);
        await v.getByRole('textbox', { name: 'Password' }).fill('not-the-one');
        await v.getByRole('button', { name: 'Continue' }).click();
        await expect(v.getByRole('alert')).toHaveText(
          'That password is not right. Check it and try again.',
        );
        await check(v, info, `access-login-error-${scheme}`);
      });

      await test.step('the right password opens the site', async () => {
        await v.getByRole('textbox', { name: 'Password' }).fill(password);
        await v.getByRole('button', { name: 'Continue' }).click();
        await expect(v.getByText('Harbour plans')).toBeVisible();
        expect(new URL(v.url()).pathname).toBe('/');
        const cookies = await visitor.cookies(siteURL);
        expect(cookies.map((c) => c.name)).toContain('__Host-wardlock_site');
        // The session keeps working on the next navigation.
        const again = await v.goto(`${siteURL}/`);
        expect(again?.status()).toBe(200);
      });
    } finally {
      await visitor.close();
    }

    let link = '';
    await test.step('the owner creates a one-time link', async () => {
      const res = await page.request.post(
        `${origin}/api/v0/sites/${site}/links`,
        { data: { label: 'Browser test', maxUses: 1 }, headers },
      );
      expect(res.status()).toBe(201);
      link = ((await res.json()) as { url: string }).url;
    });

    const guest = await browser.newContext({ colorScheme: scheme });
    try {
      const g = await guest.newPage();
      await g.setViewportSize(page.viewportSize()!);
      await test.step('"Click to view" spends the use and shows the page', async () => {
        await g.goto(link);
        await expect(
          g.getByRole('heading', {
            level: 1,
            name: 'Someone shared a page with you',
          }),
        ).toBeVisible();
        await check(g, info, `access-view-${scheme}`);
        await g.getByRole('button', { name: 'Click to view' }).click();
        await expect(g.getByText('Harbour plans')).toBeVisible();
        // The token never stays in the address bar.
        expect(new URL(g.url()).searchParams.has('t')).toBe(false);
      });
    } finally {
      await guest.close();
    }
  });
}
