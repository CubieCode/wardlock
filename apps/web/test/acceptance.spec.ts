// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test } from '@playwright/test';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;

for (const scheme of ['light', 'dark'] as const) {
  test(`dashboard connects to the server (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.goto(origin);
    await expect(
      page.getByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeVisible();
    await expect(page.getByText('Connected')).toBeVisible();
    const overflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
    await page.screenshot({
      path: info.outputPath(`dashboard-${scheme}.png`),
      fullPage: true,
    });
  });
}

test('hello site is served by host routing', async ({ page }) => {
  await page.goto(`http://hello.wardlock.localhost:${port}/`);
  await expect(
    page.getByRole('heading', { name: 'Hello from Wardlock' }),
  ).toBeVisible();
});
