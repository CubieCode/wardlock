// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { goToPlace } from './places.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');

// The first owner's saved session: email-code sends are limited per IP, and
// the other specs already spend most of that limit signing in. Every run
// shares the owner's settings, so each one changes a box of its own.
test.use({ storageState: firstOwnerState });

const boxes: Record<string, string> = {
  'desktop-light': 'Share links expire soon',
  'desktop-dark': 'A preview expires soon',
  'mobile-light': 'An API token expires soon',
  'mobile-dark': 'A domain is verified',
};

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
    animations: 'disabled',
  });
}

for (const scheme of schemes) {
  test(`an owner chooses which emails they get (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto(`${origin}/`);
    await goToPlace(page, info, 'Notifications');
    await expect(
      page.getByRole('heading', { level: 1, name: 'Notifications' }),
    ).toBeVisible();
    expect(page.url()).toBe(`${origin}/notifications`);

    const box = page.getByRole('checkbox', {
      name: boxes[`${info.project.name}-${scheme}`]!,
    });
    await expect(box).toBeChecked();
    const withheld = page.getByRole('checkbox', { name: 'A file is withheld' });
    await expect(withheld).toBeChecked();
    await expect(withheld).toBeDisabled();
    await check(page, info, `notifications-${scheme}`);

    await box.click();
    await expect(page.getByText('Email turned off')).toBeVisible();
    await page.reload();
    await expect(box).not.toBeChecked();

    await box.click();
    await expect(page.getByText('Email turned on')).toBeVisible();
    await page.reload();
    await expect(box).toBeChecked();
  });
}
