// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// `pnpm browser:test`: starts the built wardlockd (pnpm build) on a free port
// with a throwaway data directory, runs the Playwright specs in
// apps/web/test at desktop and mobile widths, then stops the server. The specs
// get the server's one-time setup URL and its dev:// mail directory, so they
// can sign in as synthetic owners; neither is printed.
// Install the browser once: pnpm --filter @wardlock/web exec playwright install chromium
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

/** @type {number} */
const port = await new Promise((resolve) => {
  const server = createServer().listen(0, '127.0.0.1', () => {
    const { port } = /** @type {import('node:net').AddressInfo} */ (
      server.address()
    );
    server.close(() => resolve(port));
  });
});
const dataDir = mkdtempSync(join(tmpdir(), 'wardlock-acceptance-'));
const origin = `http://wardlock.localhost:${port}`;
// dev:// writes mail under the working directory's .local/mail.
const mailDir = join(dataDir, '.local', 'mail');
const wardlockd = resolve('.local/bin/wardlockd');
// The operator controls spec (A2.13) runs `wardlockd admin` with the same
// settings against the same data directory.
const secret = randomBytes(32).toString('base64url');
const serverEnv = {
  WARDLOCK_HTTP_ADDR: `127.0.0.1:${port}`,
  WARDLOCK_BASE_DOMAIN: 'wardlock.localhost',
  WARDLOCK_DATA_DIR: dataDir,
  WARDLOCK_SECRET: secret,
};
const server = spawn(wardlockd, ['serve'], {
  cwd: dataDir,
  stdio: ['ignore', 'inherit', 'pipe'],
  env: {
    ...process.env,
    WARDLOCK_HTTP_ADDR: `127.0.0.1:${port}`,
    WARDLOCK_BASE_DOMAIN: 'wardlock.localhost',
    WARDLOCK_DASHBOARD_HOST: '',
    WARDLOCK_DATA_DIR: dataDir,
    WARDLOCK_DEV_DASHBOARD_URL: '',
    WARDLOCK_LOG_LEVEL: 'info',
    WARDLOCK_LOG_FORMAT: 'json',
    WARDLOCK_SMTP_URL: 'dev://',
    WARDLOCK_SMTP_FROM: '',
    // A fresh server key per run; nothing persists past the throwaway data
    // directory.
    WARDLOCK_SECRET: secret,
    // Abuse reports (A2.13) go to a synthetic address in the mail sink.
    WARDLOCK_ABUSE_EMAIL: 'abuse@example.test',
    // OAuth providers (A2.5) with synthetic clients, so login pages list
    // them; the browser test stops at the provider and never reaches it.
    WARDLOCK_GITHUB_CLIENT_ID: 'acceptance-github-client',
    WARDLOCK_GITHUB_CLIENT_SECRET: randomBytes(16).toString('hex'),
    WARDLOCK_GOOGLE_CLIENT_ID: 'acceptance-google-client',
    WARDLOCK_GOOGLE_CLIENT_SECRET: randomBytes(16).toString('hex'),
    // Country lookup (V5.13) from a synthetic database that places
    // 127.0.0.0/24, where every spec's request comes from, in NZ; never real
    // geo data. The server is drawn in NZ too.
    WARDLOCK_COUNTRY_DB: resolve(
      'apps/server/internal/country/countrytest/testdata/acceptance.mmdb',
    ),
    WARDLOCK_COUNTRY_DB_ATTRIBUTION:
      'Synthetic country data for tests (https://example.test/countries)',
    WARDLOCK_SERVER_COUNTRY: 'NZ',
    WARDLOCK_OIDC_ISSUER: '',
    WARDLOCK_OIDC_CLIENT_ID: '',
    WARDLOCK_OIDC_CLIENT_SECRET: '',
  },
});

// Keep the setup URL for the specs; pass warnings and errors through.
let setupURL = '';
createInterface({ input: server.stderr }).on('line', (line) => {
  let entry;
  try {
    entry = JSON.parse(line);
  } catch {
    process.stderr.write(`${line}\n`);
    return;
  }
  if (entry.setup_url) setupURL = entry.setup_url;
  else if (entry.level !== 'INFO' && entry.level !== 'DEBUG')
    process.stderr.write(`${line}\n`);
});

let status;
try {
  for (let i = 0; ; i++) {
    const ok = await fetch(`http://127.0.0.1:${port}/healthz`).then(
      (r) => r.ok,
      () => false,
    );
    if (ok && setupURL) break;
    if (i > 50) throw new Error('wardlockd did not start');
    await new Promise((r) => setTimeout(r, 100));
  }
  // Asynchronous, so this process keeps draining wardlockd's piped log
  // while the specs run; a blocked pipe would stall the server.
  const playwright = spawn(
    'pnpm',
    [
      '--filter',
      '@wardlock/web',
      'exec',
      'playwright',
      'test',
      '-c',
      'test/playwright.config.ts',
      // Extra arguments pick specs or tests: `pnpm browser:test sites-list`.
      ...process.argv.slice(2),
    ],
    {
      stdio: 'inherit',
      env: {
        ...process.env,
        WARDLOCK_ACCEPTANCE_ORIGIN: origin,
        WARDLOCK_ACCEPTANCE_SETUP_URL: setupURL,
        WARDLOCK_ACCEPTANCE_MAIL_DIR: mailDir,
        WARDLOCK_ACCEPTANCE_WARDLOCKD: wardlockd,
        WARDLOCK_ACCEPTANCE_ADMIN_ENV: JSON.stringify(serverEnv),
      },
    },
  );
  status = await new Promise((resolve) => {
    playwright.on('error', () => resolve(1));
    playwright.on('exit', (code) => resolve(code ?? 1));
  });
} finally {
  server.kill('SIGTERM');
  rmSync(dataDir, { recursive: true, force: true });
}
process.exit(status);
