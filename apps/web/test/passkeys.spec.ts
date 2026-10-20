// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { mailbox, passkeyOwnerFor, schemes } from './mail.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');

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

/** Follows a sidebar link, opening the small-screen menu first if needed. */
async function goTo(page: Page, name: string) {
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) await menu.click();
  await page
    .getByRole('navigation', { name: 'Main' })
    .getByRole('link', { name })
    .click();
}

async function signOut(page: Page) {
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) await menu.click();
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByText('You have signed out.')).toBeVisible();
}

for (const scheme of schemes) {
  test(`add a passkey, sign in with it and remove it (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const email = passkeyOwnerFor(info.project.name, scheme);
    const inbox = mailbox(email);
    const shot = (step: string) =>
      check(page, info, `passkeys-${step}-${scheme}`);

    // A platform authenticator that holds discoverable credentials and
    // verifies the user without a prompt (Chrome DevTools WebAuthn domain).
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('WebAuthn.enable');
    const { authenticatorId } = await cdp.send(
      'WebAuthn.addVirtualAuthenticator',
      {
        options: {
          protocol: 'ctap2',
          transport: 'internal',
          hasResidentKey: true,
          hasUserVerification: true,
          isUserVerified: true,
          automaticPresenceSimulation: true,
        },
      },
    );
    const stored = async () =>
      (await cdp.send('WebAuthn.getCredentials', { authenticatorId }))
        .credentials;

    await test.step('an owner with no passkeys adds one', async () => {
      await page.goto(await inbox.signInLink());
      await page.getByRole('button', { name: 'Continue' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Overview' }),
      ).toBeVisible();
      await goTo(page, 'Passkeys');
      await expect(
        page.getByRole('heading', { level: 1, name: 'Passkeys' }),
      ).toBeVisible();
      expect(page.url()).toBe(`${origin}/passkeys`);
      await expect(page.getByText(/You have no passkeys yet/)).toBeVisible();
      await shot('empty');

      await page.getByRole('button', { name: 'Add a passkey' }).click();
      await expect(page.getByText(/Passkey added as “/)).toBeVisible();
      await expect(page.getByText(/Not used yet/)).toBeVisible();
      const creds = await stored();
      expect(creds).toHaveLength(1);
      expect(creds[0]!.isResidentCredential).toBe(true);
      expect(creds[0]!.rpId).toBe(new URL(origin).hostname);
      await shot('added');
    });

    await test.step('the passkey signs the owner in', async () => {
      await signOut(page);
      await expect(
        page.getByRole('button', { name: 'Sign in with a passkey' }),
      ).toBeVisible();
      await shot('sign-in');
      await page.goto(`${origin}/passkeys`);
      await expect(page).toHaveURL(
        `${origin}/sign-in?next=${encodeURIComponent('/passkeys')}`,
      );
      await page
        .getByRole('button', { name: 'Sign in with a passkey' })
        .click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Passkeys' }),
      ).toBeVisible();
      expect(page.url()).toBe(`${origin}/passkeys`);
      await expect(page.getByText(/Last used/)).toBeVisible();
      expect((await stored())[0]!.signCount).toBeGreaterThan(0);
    });

    await test.step('a removed passkey no longer signs in', async () => {
      const remove = page.getByRole('button', { name: /^Remove / });
      await remove.click();
      await expect(page.getByText(/Remove the passkey “/)).toBeVisible();
      await expect(page.getByRole('button', { name: 'Cancel' })).toBeFocused();
      await shot('confirm-remove');
      await page.getByRole('button', { name: 'Remove passkey' }).click();
      await expect(page.getByText(/removed\.$/)).toBeVisible();
      await expect(page.getByText(/You have no passkeys yet/)).toBeVisible();
      await shot('removed');

      // The authenticator keeps its copy; the server refuses it.
      expect(await stored()).toHaveLength(1);
      await signOut(page);
      await page
        .getByRole('button', { name: 'Sign in with a passkey' })
        .click();
      await expect(
        page.getByText(/That passkey was not accepted/),
      ).toBeVisible();
      await expect(page).toHaveURL(/\/sign-in(\?|$)/);
      await shot('refused');
    });

    await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId });
  });
}
