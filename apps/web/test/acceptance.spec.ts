// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { mailbox, ownerFor, schemes } from './mail.js';

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

/** Opens the small-screen menu when the sidebar is collapsed. */
async function openMenu(page: Page) {
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) await menu.click();
}

async function signInWithCode(
  page: Page,
  email: string,
  inbox: ReturnType<typeof mailbox>,
) {
  await page.getByRole('textbox', { name: 'Email address' }).fill(email);
  await page.getByRole('button', { name: 'Send code' }).click();
  await expect(
    page.getByRole('heading', { level: 1, name: 'Check your email' }),
  ).toBeVisible();
  const code = await inbox.code();
  await page.getByRole('textbox', { name: 'Sign-in code' }).fill(code);
  await page.getByRole('button', { name: 'Sign in' }).click();
  return code;
}

for (const scheme of schemes) {
  test(`sign-in, wrong code, sign-out and expiry (${scheme})`, async ({
    page,
    playwright,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const email = ownerFor(info.project.name, scheme);
    const inbox = mailbox(email);
    const shot = (step: string) => check(page, info, `${step}-${scheme}`);

    await test.step('the invitation link signs the owner in', async () => {
      await page.goto(await inbox.signInLink());
      await expect(
        page.getByRole('heading', { level: 1, name: 'Overview' }),
      ).toBeVisible();
      expect(page.url()).toBe(`${origin}/`);
      await expect(page.getByText('Connected', { exact: true })).toBeVisible();
      await shot('overview');
      await openMenu(page);
      await expect(
        page.getByRole('navigation', { name: 'Main' }).getByRole('link'),
      ).toHaveText(['Overview', 'Sites', 'Passkeys']);
      await expect(page.getByText(email, { exact: true })).toBeVisible();
      await shot('navigation');
      const menu = page.getByRole('button', { name: 'Close' });
      if (await menu.isVisible()) {
        // The open menu holds focus; Escape closes it and returns focus.
        await expect(
          page.getByRole('link', { name: 'Overview' }),
        ).toBeFocused();
        await page.keyboard.press('Escape');
        const reopen = page.getByRole('button', { name: 'Menu' });
        await expect(reopen).toBeFocused();
        await expect(reopen).toHaveAttribute('aria-expanded', 'false');
        await reopen.click();
      }
    });

    await test.step('signing out returns to sign-in', async () => {
      await page.getByRole('button', { name: 'Sign out' }).click();
      await expect(page.getByText('You have signed out.')).toBeVisible();
      expect(page.url()).toBe(`${origin}/sign-in`);
      await shot('signed-out');
      // The session is gone on the server too.
      await page.goto(origin);
      await expect(page).toHaveURL(`${origin}/sign-in`);
    });

    await test.step('a wrong code is refused, the right one signs in', async () => {
      await page.getByRole('textbox', { name: 'Email address' }).fill(email);
      await page.getByRole('button', { name: 'Send code' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Check your email' }),
      ).toBeVisible();
      const code = await inbox.code();
      await shot('code');
      const wrong = String((Number(code) + 1) % 1_000_000).padStart(6, '0');
      const field = page.getByRole('textbox', { name: 'Sign-in code' });
      await field.fill(wrong);
      await page.getByRole('button', { name: 'Sign in' }).click();
      await expect(field).toHaveAttribute('aria-invalid', 'true');
      await expect(page.getByRole('alert')).toContainText(
        'That code is not right, or it has expired.',
      );
      await field.fill('');
      await shot('wrong-code');
      await field.fill(code);
      await page.getByRole('button', { name: 'Sign in' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Overview' }),
      ).toBeVisible();
    });

    await test.step('an ended session asks to sign in again', async () => {
      await page.goto(`${origin}/no-such-page`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Page not found' }),
      ).toBeVisible();
      // End the session server-side, as expiry or revocation would, while
      // the browser still holds the cookie.
      const cookie = (await page.context().cookies(origin)).find(
        (c) => c.name === '__Host-wardlock_session',
      );
      expect(cookie).toBeTruthy();
      const other = await playwright.request.newContext();
      try {
        const res = await other.post(`${origin}/api/v0/auth/sign-out`, {
          headers: {
            Cookie: `${cookie!.name}=${cookie!.value}`,
            Origin: origin,
          },
        });
        expect(res.status()).toBe(204);
      } finally {
        await other.dispose();
      }
      expect(await page.context().cookies(origin)).toContainEqual(
        expect.objectContaining({ name: '__Host-wardlock_session' }),
      );

      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await expect(
        page.getByText('Your session has ended. Sign in again to continue.'),
      ).toBeVisible();
      expect(page.url()).toBe(
        `${origin}/sign-in?next=${encodeURIComponent('/no-such-page')}`,
      );
      await shot('expired');

      await signInWithCode(page, email, inbox);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Page not found' }),
      ).toBeVisible();
      expect(page.url()).toBe(`${origin}/no-such-page`);
    });
  });
}

test('an unknown site host is routed to the edge', async ({ page }) => {
  const res = await page.goto(`http://nothing.wardlock.localhost:${port}/`);
  expect(res?.status()).toBe(404);
  await expect(
    page.getByText('No site is published at this address.'),
  ).toBeVisible();
});
