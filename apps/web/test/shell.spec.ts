// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { barPlaces, jumpPages, railPlaces } from './places.js';
import { createSite } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');

// The first owner's saved session: email-code sends are limited per IP, and
// the other specs already spend most of that limit signing in.
test.use({ storageState: firstOwnerState });

async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    animations: 'disabled',
  });
}

for (const scheme of schemes) {
  test(`shell: rail, navigator, jump to and theme (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) => check(page, info, `shell-${step}-${scheme}`);
    const mobile = info.project.name === 'mobile';
    const name = `shell-${info.project.name}-${scheme}`;
    await createSite(page.request, `${name}-alpha`);
    await createSite(page.request, `${name}-beta`);
    const main = page.getByRole('navigation', { name: 'Main' });

    await test.step('the rail lists only built screens', async () => {
      await page.goto(`${origin}/sites`);
      await expect(
        page.getByRole('heading', { level: 1, name: 'Sites' }),
      ).toBeVisible();
      await expect(
        main.getByRole('link', { name: /^(?!Wardlock$)/ }),
      ).toHaveText(mobile ? barPlaces : railPlaces);
      await expect(main.getByRole('link', { name: 'Sites' })).toHaveAttribute(
        'aria-current',
        'page',
      );
      if (mobile) {
        // Five items at most: the bar's places, Jump to and Account.
        expect(await main.locator('.wl-rail-item').count()).toBeLessThanOrEqual(
          5,
        );
      }
    });

    await test.step('the navigator shows All sites with its count, in a drawer on narrow screens', async () => {
      if (mobile) {
        // No navigator column: Browse sites opens it in a drawer.
        await expect(page.getByRole('complementary')).toHaveCount(0);
        await shot('bottom-bar');
        await page.getByRole('button', { name: 'Browse sites' }).click();
        const drawer = page.getByRole('dialog', { name: 'Browse sites' });
        await expect(
          drawer.getByRole('link', { name: /^All sites \d+$/ }),
        ).toHaveAttribute('aria-current', 'page');
        await shot('drawer');
        await drawer.getByRole('button', { name: 'Close' }).click();
        await expect(drawer).toBeHidden();
      } else {
        const navigator = page.getByRole('complementary', {
          name: 'Sites navigator',
        });
        const all = navigator.getByRole('link', { name: /^All sites \d+$/ });
        await expect(all).toHaveAttribute('aria-current', 'page');
        await main.getByRole('link', { name: 'Overview' }).hover();
        await shot('rail');
      }
    });

    await test.step('Ctrl+K searches sites and opens the choice', async () => {
      await page.keyboard.press('Control+k');
      const input = page.getByRole('combobox', { name: 'Jump to' });
      await expect(input).toBeFocused();
      await input.fill(name);
      const options = page
        .getByRole('listbox', { name: 'Jump to' })
        .getByRole('option');
      await expect(options.first()).toHaveText(new RegExp(`^${name}-alpha`));
      await expect(options.nth(1)).toHaveText(new RegExp(`^${name}-beta`));
      await page.keyboard.press('ArrowDown');
      await expect(options.nth(1)).toHaveAttribute('aria-selected', 'true');
      await shot('palette');
      await page.keyboard.press('Enter');
      await expect(page).toHaveURL(`${origin}/sites/${name}-beta`);
      await expect(input).toHaveCount(0);
    });

    await test.step('Jump to opens the palette and Escape returns focus', async () => {
      const jump = main.getByRole('button', { name: 'Jump to' });
      await jump.click();
      await expect(
        page.getByRole('combobox', { name: 'Jump to' }),
      ).toBeFocused();
      await expect(
        page.getByRole('group', { name: 'Pages' }).getByRole('option'),
      ).toHaveText(jumpPages);
      await page.keyboard.press('Escape');
      await expect(page.getByRole('combobox')).toHaveCount(0);
      await expect(jump).toBeFocused();
    });

    await test.step('the theme setting persists across reloads', async () => {
      const other = scheme === 'light' ? 'Dark' : 'Light';
      const html = page.locator('html');
      const account = main.getByRole('button', { name: 'Account' });
      await account.click();
      const menu = page.getByRole('dialog', { name: 'Account' });
      await expect(menu.getByRole('radio', { name: 'System' })).toBeChecked();
      await expect(html).not.toHaveAttribute('data-theme', /.*/);
      await menu.getByRole('radio', { name: other }).check();
      await expect(html).toHaveAttribute('data-theme', other.toLowerCase());
      await shot('theme');

      // public/theme.js applies it before the app's own script runs.
      await page.route('**/assets/*.js', (route) => route.abort());
      await page.reload();
      await expect(html).toHaveAttribute('data-theme', other.toLowerCase());
      await page.unroute('**/assets/*.js');

      await page.reload();
      await expect(html).toHaveAttribute('data-theme', other.toLowerCase());
      await account.click();
      await expect(menu.getByRole('radio', { name: other })).toBeChecked();
      await menu.getByRole('radio', { name: 'System' }).check();
      await expect(html).not.toHaveAttribute('data-theme', /.*/);
      await page.reload();
      await expect(
        page.getByRole('heading', { level: 1, name: `${name}-beta` }),
      ).toBeVisible();
      await expect(html).not.toHaveAttribute('data-theme', /.*/);
    });
  });
}
