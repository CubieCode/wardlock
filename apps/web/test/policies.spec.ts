// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The policies editor (D3.4), against a real server: the owner protects a
// site with each method from the dashboard (share links, a password, an
// email code to an allowlisted address, sign-in with an allowlisted GitHub
// organisation) and a visitor with no cookies gets in that way; then a
// public path policy under the protected default shows how the longest
// matching path wins, and deleting it hands its paths back to the default.
// The password is never on the page once it is saved. The password and
// addresses are synthetic.
import { createHash } from 'node:crypto';
import {
  expect,
  test,
  type APIRequestContext,
  type Browser,
  type Locator,
  type Page,
  type TestInfo,
} from '@playwright/test';
import { firstOwnerState, mailbox, schemes } from './mail.js';
import { createSite } from './publish.js';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN;
if (!origin) throw new Error('Run through pnpm browser:test');
const port = new URL(origin).port;
const headers = { Origin: origin };
const password = 'harbour-lights-7';
// Signed in as the first owner, as the share-links spec is: email-code
// sends are limited per IP, and the other specs use them.
test.use({ storageState: firstOwnerState });

/** Fails if the password is in the page's markup, an input or the title. */
async function expectNoPassword(page: Page) {
  expect(await page.content()).not.toContain(password);
  expect(await page.title()).not.toContain(password);
  const values = await page
    .locator('input, textarea')
    .evaluateAll((els) => els.map((el) => (el as HTMLInputElement).value));
  expect(values).not.toContain(password);
}

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

/** Deploys a home page and a public guide under /docs/, and makes it live. */
async function publishSite(request: APIRequestContext, site: string) {
  const files = {
    'index.html': '<!doctype html><title>Harbour plans</title><p>Harbour plans',
    'docs/index.html': '<!doctype html><title>Guide</title><p>Public guide',
  };
  const bodies = Object.entries(files).map(([path, text]) => {
    const body = Buffer.from(text);
    const sha256 = createHash('sha256').update(body).digest('hex');
    return { path, body, sha256 };
  });
  const base = `${origin}/api/v0/sites/${site}/deploys`;
  const created = await request.post(base, {
    data: {
      message: 'Harbour plans',
      files: bodies.map(({ path, body, sha256 }) => ({
        path,
        sha256,
        size: body.length,
      })),
    },
    headers,
  });
  expect(created.status()).toBe(201);
  const { deploy, missing } = (await created.json()) as {
    deploy: { id: string };
    missing: string[];
  };
  for (const hash of missing) {
    const put = await request.put(`${base}/${deploy.id}/blobs/${hash}`, {
      data: bodies.find((b) => b.sha256 === hash)!.body,
      headers: { ...headers, 'Content-Type': 'application/octet-stream' },
    });
    expect(put.status()).toBe(204);
  }
  const activated = await request.post(`${base}/${deploy.id}/activate`, {
    headers,
  });
  expect(activated.status()).toBe(200);
}

/** A page in a fresh context: a visitor with no cookies. */
async function asVisitor(
  browser: Browser,
  owner: Page,
  scheme: string,
  run: (page: Page) => Promise<void>,
) {
  const context = await browser.newContext({ colorScheme: scheme as 'dark' });
  try {
    const page = await context.newPage();
    await page.setViewportSize(owner.viewportSize()!);
    await run(page);
  } finally {
    await context.close();
  }
}

for (const scheme of schemes) {
  test(`policies: each method and path precedence (${scheme})`, async ({
    page,
    browser,
  }, info) => {
    await page.emulateMedia({ colorScheme: scheme });
    const shot = (step: string) =>
      check(page, info, `policies-${step}-${scheme}`);
    const site = `pol-${info.project.name}-${scheme}`;
    const siteURL = `http://${site}.wardlock.localhost:${port}`;
    const card = page.getByRole('region', { name: 'Access policies' });
    const rows = card
      .getByRole('list', { name: 'Access policies' })
      .getByRole('listitem');
    const summary = page.getByRole('region', { name: 'Address and access' });
    const editDefault = async () => {
      await card.getByRole('button', { name: 'Edit the site default' }).click();
      const dialog = page.getByRole('dialog', {
        name: `Edit the site default for ${site}`,
      });
      await expect(dialog).toBeVisible();
      return dialog;
    };
    const save = async (dialog: Locator) => {
      await dialog.getByRole('button', { name: 'Save policy' }).click();
      await expect(dialog).toBeHidden();
      await expect(card.getByText(/^Saved the site default\./)).toBeVisible();
    };
    const method = (dialog: Locator, name: string) =>
      dialog.getByRole('checkbox', { name, exact: true });

    await test.step('the owner sees the public default', async () => {
      await createSite(page.request, site);
      await publishSite(page.request, site);
      await page.goto(`${origin}/sites/${site}`);
      await expect(rows).toHaveCount(1);
      await expect(rows.first()).toContainText('Site default');
      await expect(rows.first()).toContainText('Public');
      await expect(card).toContainText('longest path that matches it');
      await shot('public');
    });

    await test.step('share links and a password protect the site', async () => {
      const dialog = await editDefault();
      await dialog.getByRole('radio', { name: 'Protected' }).check();
      await expect(method(dialog, 'Share links')).toBeChecked();
      await method(dialog, 'Password').check();
      await dialog
        .getByRole('textbox', { name: 'Visitor password' })
        .fill(password);
      await page.screenshot({
        path: info.outputPath(`policies-password-${scheme}.png`),
        fullPage: true,
        mask: [dialog.getByLabel('Visitor password')],
      });
      await save(dialog);
      await expect(rows.first()).toContainText('Protected');
      await expect(rows.first()).toContainText(
        'Ways in: share links and password.',
      );
      await expect(summary).toContainText('Only visitors with access');
      await expectNoPassword(page);
      await shot('protected');

      // Editing again shows that a password is set, never the password.
      const again = await editDefault();
      await expect(
        again.getByRole('textbox', { name: 'New visitor password' }),
      ).toHaveValue('');
      await expect(again).toContainText('A password is set.');
      await expectNoPassword(page);
      await again.getByRole('button', { name: 'Cancel' }).click();
    });

    await test.step('a visitor gets in with the password', async () => {
      await asVisitor(browser, page, scheme, async (v) => {
        const res = await v.goto(`${siteURL}/`);
        expect(res?.status()).toBe(401);
        const form = v.getByRole('region', { name: 'Password' });
        await expect(form.getByRole('textbox')).toBeVisible();
        // Password attempts are limited to 10 per client across all sites,
        // and site-access.spec spends most of them: sign in once per run.
        if (info.project.name !== 'desktop' || scheme !== 'light') return;
        await form.getByRole('textbox', { name: 'Password' }).fill(password);
        await form.getByRole('button', { name: 'Continue' }).click();
        await expect(v.getByText('Harbour plans')).toBeVisible();
      });
    });

    let link = '';
    await test.step('a visitor gets in with a share link', async () => {
      const res = await page.request.post(
        `${origin}/api/v0/sites/${site}/links`,
        { data: { label: 'Policies test' }, headers },
      );
      expect(res.status()).toBe(201);
      link = ((await res.json()) as { url: string }).url;
      await asVisitor(browser, page, scheme, async (v) => {
        await v.goto(link);
        await expect(v.getByText('Harbour plans')).toBeVisible();
      });
    });

    await test.step('switching share links off stops the link', async () => {
      const dialog = await editDefault();
      await method(dialog, 'Share links').uncheck();
      await save(dialog);
      await expect(rows.first()).toContainText('Ways in: password.');
      await asVisitor(browser, page, scheme, async (v) => {
        const res = await v.goto(link);
        expect(res?.status()).toBe(401);
      });
    });

    const visitorEmail = `policy-${info.project.name}-${scheme}@example.test`;
    await test.step('email codes let in an allowlisted address', async () => {
      const dialog = await editDefault();
      await method(dialog, 'Email code').check();
      await dialog.getByRole('button', { name: 'Save policy' }).click();
      await expect(dialog.getByRole('alert')).toHaveText(
        'Email codes need at least one address or domain.',
      );
      await dialog
        .getByRole('textbox', { name: 'Allowlist' })
        .fill(visitorEmail);
      await shot('email');
      await save(dialog);
      await expect(rows.first()).toContainText(
        'Ways in: password and email code.',
      );
      await expect(rows.first()).toContainText(`Allowlist: ${visitorEmail}.`);

      const inbox = mailbox(visitorEmail);
      await asVisitor(browser, page, scheme, async (v) => {
        await v.goto(`${siteURL}/`);
        const form = v.getByRole('region', { name: 'Email code' });
        await form
          .getByRole('textbox', { name: 'Email address' })
          .fill(visitorEmail);
        await form.getByRole('button', { name: 'Send code' }).click();
        await form
          .getByRole('textbox', { name: 'Code' })
          .fill(await inbox.code());
        await form.getByRole('button', { name: 'Continue' }).click();
        await expect(v.getByText('Harbour plans')).toBeVisible();
      });
    });

    await test.step('sign-in lists the providers for a GitHub organisation', async () => {
      const dialog = await editDefault();
      await method(
        dialog,
        'Sign in with GitHub, Google or single sign-on',
      ).check();
      await dialog
        .getByRole('textbox', { name: 'GitHub organisations' })
        .fill('example-org');
      await shot('oauth');
      await save(dialog);
      await expect(rows.first()).toContainText(
        'Ways in: password, email code and GitHub, Google or single sign-on.',
      );
      await expect(rows.first()).toContainText('GitHub: example-org');
      await asVisitor(browser, page, scheme, async (v) => {
        const res = await v.goto(`${siteURL}/`);
        expect(res?.status()).toBe(401);
        const accounts = v.getByRole('region', { name: 'Your account' });
        await expect(
          accounts.getByRole('button', { name: 'Continue with GitHub' }),
        ).toBeVisible();
      });
      await expectNoPassword(page);
    });

    await test.step('a public /docs/ wins over the protected default', async () => {
      await card.getByRole('button', { name: 'Add path policy' }).click();
      const dialog = page.getByRole('dialog', {
        name: `Add a path policy to ${site}`,
      });
      await dialog.getByRole('textbox', { name: 'Folder' }).fill('/docs/');
      await dialog.getByRole('radio', { name: 'Public' }).check();
      await dialog.getByRole('button', { name: 'Add policy' }).click();
      await expect(dialog).toBeHidden();
      await expect(
        card.getByText('Saved the policy for /docs/.'),
      ).toBeVisible();
      await expect(rows).toHaveCount(2);
      await expect(rows.nth(1)).toContainText(
        'Covers /docs/ and everything under it.',
      );

      const checker = card.getByRole('textbox', { name: 'Check a path' });
      await checker.fill('/docs/index.html');
      await expect(card.getByRole('status').last()).toHaveText(
        '/docs/index.html follows the policy for /docs/: public.',
      );
      await shot('paths');
      await checker.fill('/docs/../index.html');
      await expect(card.getByRole('status').last()).toHaveText(
        '/index.html follows the site default: protected (password, email code and GitHub, Google or single sign-on).',
      );

      await asVisitor(browser, page, scheme, async (v) => {
        await v.goto(`${siteURL}/docs/`);
        await expect(v.getByText('Public guide')).toBeVisible();
        const home = await v.goto(`${siteURL}/`);
        expect(home?.status()).toBe(401);
      });
    });

    await test.step('deleting /docs/ hands it back to the default', async () => {
      await card
        .getByRole('button', { name: 'Delete the policy for /docs/' })
        .click();
      const confirm = page.getByRole('dialog', {
        name: 'Delete the policy for /docs/?',
      });
      await expect(confirm).toContainText(
        'will follow the site default instead, so it becomes protected',
      );
      await shot('delete');
      await confirm.getByRole('button', { name: 'Delete policy' }).click();
      await expect(confirm).toBeHidden();
      await expect(rows).toHaveCount(1);
      await asVisitor(browser, page, scheme, async (v) => {
        const res = await v.goto(`${siteURL}/docs/`);
        expect(res?.status()).toBe(401);
      });
      await expectNoPassword(page);
    });
  });
}
