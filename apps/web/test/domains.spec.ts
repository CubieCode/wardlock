// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Custom domains on the site page (D3.5), against a real server: add a
// hostname and copy its records exactly as shown, have "Check now" refused
// straight after the add's own check, see domain_taken, then remove it
// through its confirmation. The local server cannot pass a DNS check, so the
// statuses a check can return (each failure reason, active, lapsed) come
// from answers to "Check now" built on the real domain. Foreign sites are
// covered by sites.spec.ts, whose owner is not the first owner.
import {
  expect,
  test,
  type Locator,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { firstOwnerState, schemes } from './mail.js';
import { createSite, publish } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
// Signed in as the first owner, whose session setup saved: email-code sends
// and invitations are limited, and the other specs use them.
test.use({ storageState: firstOwnerState });

const minute = 60_000;

/** No sideways scroll, then a full-page screenshot. */
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

/** Copies a field with its own Copy button and reads the clipboard back. */
async function copied(page: Page, scope: Locator, label: string) {
  const shown = await scope.getByLabel(label, { exact: true }).inputValue();
  await scope
    .getByRole('button', { name: `Copy ${label.toLowerCase()}` })
    .click();
  await expect(scope.getByText(`${label} copied.`)).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(shown);
  return shown;
}

for (const scheme of schemes) {
  test(`custom domains: add, copy, statuses, check now, taken and remove (${scheme})`, async ({
    page,
    context,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], {
      origin,
    });
    const shot = (step: string) =>
      check(page, info, `domains-${step}-${scheme}`);
    const site = `d-${info.project.name}-${scheme}`;
    const other = `e-${info.project.name}-${scheme}`;
    // Under .test (RFC 6761), so no real DNS name is ever involved.
    const hostname = `www.${site}.example.test`;
    const api = `${origin}/api/v0/sites/${site}/domains`;
    const card = page.getByRole('region', { name: 'Custom domains' });
    const row = card.getByRole('listitem', { name: hostname });
    const checkNow = row.getByRole('button', { name: `Check ${hostname} now` });

    await test.step('the owner opens a site with no custom domains', async () => {
      await createSite(page.request, site);
      await createSite(page.request, other);
      await publish(page.request, site, 'Field guide');
      await page.goto(`${origin}/sites/${site}/domains`);
      await expect(
        card.getByRole('heading', { name: 'No custom domains yet' }),
      ).toBeVisible();
      await shot('empty');
    });

    await test.step('adding a hostname shows its records, copyable as shown', async () => {
      await card.getByRole('textbox', { name: 'Hostname' }).fill(hostname);
      await card.getByRole('button', { name: 'Add domain' }).click();
      await expect(row).toBeVisible();
      await expect(card.getByText(`Added ${hostname}.`)).toBeVisible();
      await expect(row.getByText('Pending', { exact: true })).toBeVisible();
      // The add checked DNS at once; the local server never passes it.
      await expect(row.getByText(/^Last checked/)).toBeVisible();
      await expect(row.locator('.domain-failure')).toBeVisible();

      const cname = row.getByRole('group', { name: 'CNAME record' });
      expect(await copied(page, cname, 'Name')).toBe(hostname);
      expect(await copied(page, cname, 'Target')).toBe(
        `${site}.wardlock.localhost`,
      );
      await row.getByText('Apex domain or CDN proxy? Use a TXT record').click();
      const txt = row.getByRole('group', { name: 'TXT record' });
      expect(await copied(page, txt, 'Name')).toBe(`_wardlock.${hostname}`);
      expect(await copied(page, txt, 'Value')).toMatch(/^wardlock-verify=\S+$/);
      await shot('pending');
    });

    await test.step('"Check now" straight after the add is refused politely', async () => {
      await checkNow.click();
      await expect(
        row.getByText(/^Checked moments ago\. Try again in \d+ seconds?\.$/),
      ).toBeVisible();
    });

    await test.step('each check result shows its status and reason', async () => {
      const res = await page.request.get(`${api}/${hostname}`);
      expect(res.status()).toBe(200);
      const real = (await res.json()) as Record<string, unknown>;
      const now = Date.now();
      const answers: [Record<string, unknown>, string, string][] = [
        [
          {
            status: 'pending',
            failureReason: 'cname_elsewhere',
            failureDetail: `${hostname} points to elsewhere.example.test, not ${site}.wardlock.localhost.`,
          },
          'Pending',
          'The CNAME record points somewhere else.',
        ],
        [
          {
            status: 'pending',
            failureReason: 'txt_mismatch',
            failureDetail: `_wardlock.${hostname} holds another token.`,
          },
          'Pending',
          'The TXT record holds a different value.',
        ],
        [
          {
            status: 'pending',
            failureReason: 'dns_error',
            failureDetail: 'The DNS lookup timed out.',
          },
          'Pending',
          'The DNS lookup failed.',
        ],
        [
          {
            status: 'pending',
            failureReason: 'no_record',
            failureDetail: `No CNAME or TXT record found for ${hostname}.`,
          },
          'Pending',
          'No DNS record found yet.',
        ],
        [{ status: 'active', verifiedAt: now }, 'Active', ''],
        [
          {
            status: 'lapsed',
            failingSince: now - 72 * 60 * minute,
            failureReason: 'cname_elsewhere',
            failureDetail: `${hostname} points to elsewhere.example.test, not ${site}.wardlock.localhost.`,
          },
          'Lapsed',
          'The CNAME record points somewhere else.',
        ],
      ];
      let answer = answers[0]![0];
      await page.route(`${api}/${hostname}/verify`, (route) =>
        route.fulfill({
          json: {
            ...real,
            failingSince: undefined,
            failureReason: undefined,
            failureDetail: undefined,
            lastCheckedAt: Date.now(),
            nextCheckAt: Date.now() + 5 * minute,
            ...answer,
          },
        }),
      );
      for (const [next, word, reason] of answers) {
        answer = next;
        await checkNow.click();
        await expect(row.getByText(word, { exact: true })).toBeVisible();
        await expect(row.getByText('Last checked just now')).toBeVisible();
        if (reason) {
          await expect(row.getByText(reason)).toBeVisible();
          await expect(
            row.getByText(next.failureDetail as string),
          ).toBeVisible();
        } else {
          await expect(row.locator('.domain-failure')).toHaveCount(0);
          await expect(
            row.getByText('Checked: the record checks out.'),
          ).toBeVisible();
          // Records stay one click away once the domain is live.
          await row.getByText('DNS records', { exact: true }).click();
          await expect(
            row.getByRole('group', { name: 'CNAME record' }),
          ).toBeVisible();
        }
        if (next.failureReason === 'no_record' || word !== 'Pending')
          await shot(word.toLowerCase());
      }
      await page.unroute(`${api}/${hostname}/verify`);
    });

    await test.step('a 429 that is not the cooldown shows the server’s error', async () => {
      // A session never meets the token limiter, so the refusal it sends
      // to a bearer request is answered here.
      await page.route(`${api}/${hostname}/verify`, (route) =>
        route.fulfill({
          status: 429,
          headers: { 'Retry-After': '600' },
          json: {
            code: 'rate_limited',
            message: 'Too many failed attempts. Try again later.',
          },
        }),
      );
      await checkNow.click();
      const notice = row.getByText(
        'Could not check the domain: Too many failed attempts. Try again later.',
      );
      await expect(notice).toBeVisible();
      await expect(row.getByText(/Checked moments ago/)).toHaveCount(0);
      // The message wraps inside the row rather than overflowing it.
      const [box, rowBox] = await Promise.all([
        notice.boundingBox(),
        row.boundingBox(),
      ]);
      expect(box!.x).toBeGreaterThanOrEqual(rowBox!.x);
      expect(box!.x + box!.width).toBeLessThanOrEqual(
        rowBox!.x + rowBox!.width,
      );
      expect(
        await notice.evaluate((el) => el.scrollWidth - el.clientWidth),
      ).toBeLessThanOrEqual(0);
      await shot('refused');
      await page.unroute(`${api}/${hostname}/verify`);
    });

    await test.step('a hostname another site holds is domain_taken', async () => {
      await page.goto(`${origin}/sites/${other}/domains`);
      await expect(
        card.getByRole('heading', { name: 'No custom domains yet' }),
      ).toBeVisible();
      const field = card.getByRole('textbox', { name: 'Hostname' });
      await field.fill(hostname.toUpperCase());
      await card.getByRole('button', { name: 'Add domain' }).click();
      await expect(
        card.getByText(
          'This hostname is already attached to a site on this server.',
          { exact: false },
        ),
      ).toBeVisible();
      await expect(field).toHaveAttribute('aria-invalid', 'true');
      await expect(
        card.getByRole('list', { name: 'Custom domains' }),
      ).toHaveCount(0);
      await shot('taken');
    });

    await test.step('removing asks first and reminds about the DNS record', async () => {
      await page.goto(`${origin}/sites/${site}/domains`);
      await row.getByRole('button', { name: `Remove ${hostname}` }).click();
      const dialog = page.getByRole('dialog', { name: `Remove ${hostname}?` });
      await expect(dialog).toBeVisible();
      await expect(dialog.getByText(/delete the CNAME record/)).toBeVisible();
      await shot('remove');
      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(row).toBeVisible();

      await row.getByRole('button', { name: `Remove ${hostname}` }).click();
      await dialog.getByRole('button', { name: 'Remove domain' }).click();
      await expect(dialog).toBeHidden();
      await expect(
        card.getByText(
          `Removed ${hostname}. Remember to delete its DNS record.`,
        ),
      ).toBeVisible();
      await expect(
        card.getByRole('heading', { name: 'No custom domains yet' }),
      ).toBeVisible();
      expect((await page.request.get(`${api}/${hostname}`)).status()).toBe(404);
      await shot('removed');
    });
  });
}
