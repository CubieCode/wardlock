// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// A visitor's way in, in a real browser against a real server: the
// password form on the login page (A2.3), the "Click to view" button of a
// use-limited share link (A2.2), the email-code forms (A2.4), with the
// code read from the dev:// mail sink, and the OAuth buttons (A2.5) up to
// the provider, which the browser never reaches. These pages send Referrer-Policy:
// no-referrer, so the browser posts them with Origin: null; this proves the
// edge's same-origin check still lets them through. The password and
// addresses are synthetic.
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

    const visitorEmail = `visitor-${info.project.name}-${scheme}@example.test`;
    const visitorInbox = mailbox(visitorEmail);
    await test.step('the owner enables email codes for one address', async () => {
      const res = await page.request.put(
        `${origin}/api/v0/sites/${site}/policies`,
        {
          data: {
            path: '/',
            visibility: 'protected',
            methods: ['links', 'password', 'email'],
            allowlist: { emails: [visitorEmail], domains: [] },
          },
          headers,
        },
      );
      expect(res.status()).toBe(200);
    });

    const reader = await browser.newContext({ colorScheme: scheme });
    try {
      const r = await reader.newPage();
      await r.setViewportSize(page.viewportSize()!);
      const emailForm = r.getByRole('region', { name: 'Email code' });

      await test.step('an address that is not allowed gets the same answer', async () => {
        await r.goto(`${siteURL}/`);
        await check(r, info, `access-email-${scheme}`);
        await emailForm
          .getByRole('textbox', { name: 'Email address' })
          .fill('stranger@example.test');
        await emailForm.getByRole('button', { name: 'Send code' }).click();
        await expect(emailForm).toContainText(
          "If stranger@example.test may open this page, we've sent a 6-digit code to it.",
        );
        await emailForm
          .getByRole('link', { name: 'Use another address' })
          .click();
      });

      await test.step('a wrong code shows the error', async () => {
        await emailForm
          .getByRole('textbox', { name: 'Email address' })
          .fill(visitorEmail);
        await emailForm.getByRole('button', { name: 'Send code' }).click();
        await expect(emailForm).toContainText(
          `If ${visitorEmail} may open this page, we've sent a 6-digit code to it.`,
        );
        await check(r, info, `access-email-sent-${scheme}`);
        const code = await visitorInbox.code();
        await emailForm
          .getByRole('textbox', { name: 'Code' })
          .fill(code === '000000' ? '000001' : '000000');
        await emailForm.getByRole('button', { name: 'Continue' }).click();
        await expect(emailForm.getByRole('alert')).toHaveText(
          'That code is not right, or it has expired. Check it, or send a new code.',
        );
        await check(r, info, `access-email-error-${scheme}`);

        await test.step('the right code opens the site', async () => {
          await emailForm.getByRole('textbox', { name: 'Code' }).fill(code);
          await emailForm.getByRole('button', { name: 'Continue' }).click();
          await expect(r.getByText('Harbour plans')).toBeVisible();
          const cookies = await reader.cookies(siteURL);
          expect(cookies.map((c) => c.name)).toContain('__Host-wardlock_site');
        });
      });
    } finally {
      await reader.close();
    }

    await test.step('the owner enables OAuth for a GitHub organisation', async () => {
      const res = await page.request.put(
        `${origin}/api/v0/sites/${site}/policies`,
        {
          data: {
            path: '/',
            visibility: 'protected',
            methods: ['links', 'password', 'email', 'oauth'],
            allowlist: {
              emails: [visitorEmail],
              domains: [],
              githubOrgs: ['example-org'],
            },
          },
          headers,
        },
      );
      expect(res.status()).toBe(200);
    });

    // The provider is never reached: everything off localhost goes to a
    // proxy that is not there.
    const member = await browser.newContext({
      colorScheme: scheme,
      proxy: {
        server: 'http://127.0.0.1:9',
        bypass: 'localhost,.localhost,127.0.0.1',
      },
    });
    try {
      const m = await member.newPage();
      await m.setViewportSize(page.viewportSize()!);
      const accounts = m.getByRole('region', { name: 'Your account' });

      await test.step('the login page lists the providers', async () => {
        const res = await m.goto(`${siteURL}/`);
        expect(res?.status()).toBe(401);
        await expect(
          accounts.getByRole('button', { name: 'Continue with GitHub' }),
        ).toBeVisible();
        await expect(
          accounts.getByRole('button', { name: 'Continue with Google' }),
        ).toBeVisible();
        await check(m, info, `access-oauth-${scheme}`);
      });

      await test.step('a provider button goes to the provider with PKCE', async () => {
        const toProvider = m.waitForRequest((r) =>
          r.url().startsWith('https://github.com/'),
        );
        await accounts
          .getByRole('button', { name: 'Continue with GitHub' })
          .click({ noWaitAfter: true });
        const authorise = new URL((await toProvider).url());
        expect(authorise.origin + authorise.pathname).toBe(
          'https://github.com/login/oauth/authorize',
        );
        const q = authorise.searchParams;
        expect(q.get('redirect_uri')).toBe(
          `http://auth.wardlock.localhost:${port}/callback/github`,
        );
        expect(q.get('code_challenge_method')).toBe('S256');
        expect(q.get('scope')).toBe('user:email read:org');
        expect(q.get('state')).toBeTruthy();
        const names = (await member.cookies(siteURL)).map((c) => c.name);
        expect(names).toContain('__Host-wardlock_oauth');
      });

      // A fresh tab: the first is still failing to reach the provider.
      const e = await member.newPage();
      await e.setViewportSize(page.viewportSize()!);
      await test.step('a stale sign-in shows the auth origin error page', async () => {
        const res = await e.goto(
          `http://auth.wardlock.localhost:${port}/callback/github?state=stale&code=stale`,
        );
        expect(res?.status()).toBe(400);
        expect(res?.headers()['set-cookie']).toBeUndefined();
        await expect(
          e.getByRole('heading', {
            level: 1,
            name: 'Sign-in did not complete',
          }),
        ).toBeVisible();
        await check(e, info, `access-oauth-auth-error-${scheme}`);
      });

      await test.step('a stale code at the site shows the login page again', async () => {
        const res = await e.goto(`${siteURL}/.wardlock/callback?code=stale`);
        expect(res?.status()).toBe(400);
        await expect(e.getByRole('alert')).toHaveText(
          'That sign-in has expired or was already used. Sign in again.',
        );
        await check(e, info, `access-oauth-callback-error-${scheme}`);
        const names = (await member.cookies(siteURL)).map((c) => c.name);
        expect(names).not.toContain('__Host-wardlock_oauth');
        expect(names).not.toContain('__Host-wardlock_site');
      });
    } finally {
      await member.close();
    }
  });
}
