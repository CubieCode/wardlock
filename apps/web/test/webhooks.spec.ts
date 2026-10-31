// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// I6.4: an owner adds a webhook on the page against a real server, sees the
// signing secret once, sends a test that fails to deliver, then turns the
// webhook off and deletes it. The URL is synthetic and resolves to nothing.
import { expect, test, type Page, type TestInfo } from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { goToPlace } from './places.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');

// The first owner's saved session: email-code sends are limited per IP.
test.use({ storageState: firstOwnerState });

const secretShape = /wlwh_[A-Za-z0-9_-]{43}/;

/** No sideways scroll, then a full-page screenshot with no secret in it. */
async function check(page: Page, info: TestInfo, name: string) {
  const overflow = await page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
  expect(await page.content()).not.toMatch(secretShape);
  await page.screenshot({
    path: info.outputPath(`${name}.png`),
    fullPage: true,
    animations: 'disabled',
  });
}

for (const scheme of schemes) {
  test(`add, test, turn off and delete a webhook (${scheme})`, async ({
    page,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `webhooks-${step}-${scheme}`);
    const name = `Deploys ${info.project.name} ${scheme}`;
    const url = `https://hooks.example.net/wardlock-${info.project.name}-${scheme}`;

    await test.step('the owner adds a webhook', async () => {
      await page.goto(`${origin}/`);
      await goToPlace(page, info, 'Webhooks');
      await expect(
        page.getByRole('heading', { level: 1, name: 'Webhooks' }),
      ).toBeVisible();
      await page.getByRole('button', { name: 'Add webhook' }).click();
      const dialog = page.getByRole('dialog', { name: 'Add a webhook' });
      await dialog
        .getByRole('textbox', { name: /Endpoint URL/ })
        .fill('ftp://hooks.example.net/wrong');
      await dialog.getByRole('textbox', { name: /Description/ }).fill(name);
      await dialog.getByRole('radio', { name: 'Only these events' }).check();
      // Other specs share this owner: an event they fire would fill the
      // history, which keeps only the newest 50.
      await dialog.getByRole('checkbox', { name: /^file\.withheld/ }).check();
      await shot('add');
      await dialog.getByRole('button', { name: 'Add webhook' }).click();
      await expect(dialog.getByText('url must use https.')).toBeVisible();
      await dialog.getByRole('textbox', { name: /Endpoint URL/ }).fill(url);
      await dialog.getByRole('button', { name: 'Add webhook' }).click();
    });

    await test.step('the signing secret is shown once', async () => {
      const dialog = page.getByRole('dialog', {
        name: 'Copy your signing secret',
      });
      const field = dialog.getByRole('textbox', { name: 'Signing secret' });
      await expect(field).toHaveValue(secretShape);
      await expect(dialog.getByText(/cannot be shown again/)).toBeVisible();
      const secret = await field.inputValue();
      await page.keyboard.press('Escape');
      await expect(field).toHaveValue(secret);
      await dialog.getByRole('button', { name: 'Done' }).click();
      await expect(dialog).toBeHidden();
      await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
      await expect(page.getByText(url)).toBeVisible();
      await expect(page).toHaveTitle(/^Webhook · /);
      await shot('new');
      await page.reload();
      await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
      expect(await page.content()).not.toMatch(secretShape);
    });

    await test.step('the list shows it', async () => {
      await page.getByRole('link', { name: 'All webhooks' }).click();
      const link = page.getByRole('link', { name });
      await expect(link).toBeVisible();
      await shot('several');
      await link.click();
      await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
    });

    await test.step('a test fails to deliver and is listed', async () => {
      await page.getByRole('button', { name: 'Send test' }).click();
      await expect(page.getByText('Test queued.')).toBeVisible();
      const row = page.getByRole('row', { name: /ping/ });
      await expect(row.getByText('Failed, retrying')).toBeVisible({
        timeout: 30_000,
      });
      await expect(
        row.getByText(
          /Host not found|Could not connect|Timed out|TLS failed|Address refused/,
        ),
      ).toBeVisible();
      await expect(page.getByText('Deliveries started failing')).toBeVisible();
      await shot('failed');
      await row.getByRole('button', { name: /^Redeliver/ }).click();
      await expect(page.getByText('Queued to be sent again.')).toBeVisible();
      await expect(page.getByRole('row', { name: /ping/ })).toHaveCount(2);
    });

    await test.step('turned off, it sends nothing until turned on', async () => {
      await page.getByRole('button', { name: 'Turn off' }).click();
      await expect(
        page.getByText('Anything still queued will be dropped.', {
          exact: false,
        }),
      ).toBeVisible();
      await expect(page.getByText('This webhook is turned off')).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Send test' }),
      ).toBeDisabled();
      await shot('off');
      await page.getByRole('button', { name: 'Turn on' }).click();
      await expect(page.getByText('New events will be sent.')).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Send test' }),
      ).toBeEnabled();
      await page.getByRole('button', { name: 'Turn off' }).click();
      await expect(page.getByText('This webhook is turned off')).toBeVisible();
    });

    await test.step('a webhook off after failing says why', async () => {
      // Three days of failures cannot be waited for, so the answer is changed.
      const at = Date.now() - 3600_000;
      const answer = page.url().replace(/^.*\/webhooks/, '**/api/v0/webhooks');
      await page.route(answer, async (route) => {
        const response = await route.fetch();
        await route.fulfill({
          response,
          json: {
            ...((await response.json()) as object),
            disabledReason: 'failing',
            disabledAt: at,
          },
        });
      });
      await page.reload();
      await expect(page.getByText('failed for 3 days')).toBeVisible();
      await expect(page.getByText('Turned off after failing')).toBeVisible();
      await shot('off-failing');
      await page.unroute(answer);
    });

    await test.step('deleting asks first', async () => {
      await page.getByRole('button', { name: 'Delete' }).click();
      const dialog = page.getByRole('dialog', { name: `Delete “${name}”?` });
      await shot('delete');
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toBeHidden();
      await page.getByRole('button', { name: 'Delete' }).click();
      await dialog.getByRole('button', { name: 'Delete webhook' }).click();
      await expect(
        page.getByRole('heading', { level: 1, name: 'Webhooks' }),
      ).toBeVisible();
      await expect(page.getByRole('link', { name })).toHaveCount(0);
      await shot('list');
    });
  });
}
