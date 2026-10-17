// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// `pnpm browser:test`: starts the built wardlockd (pnpm build) on a free port
// with a throwaway data directory, runs the Playwright specs in
// apps/web/test at desktop and mobile widths, then stops the server.
// Install the browser once: pnpm --filter @wardlock/web exec playwright install chromium
import { spawn, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

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
const server = spawn('.local/bin/wardlockd', ['serve'], {
  stdio: 'inherit',
  env: {
    ...process.env,
    WARDLOCK_HTTP_ADDR: `127.0.0.1:${port}`,
    WARDLOCK_BASE_DOMAIN: 'wardlock.localhost',
    WARDLOCK_DASHBOARD_HOST: '',
    WARDLOCK_DATA_DIR: dataDir,
    WARDLOCK_DEV_DASHBOARD_URL: '',
    WARDLOCK_LOG_LEVEL: 'warn',
    // A fresh server key per run; nothing persists past the throwaway data
    // directory.
    WARDLOCK_SECRET: randomBytes(32).toString('base64url'),
  },
});

let status;
try {
  for (let i = 0; ; i++) {
    const ok = await fetch(`http://127.0.0.1:${port}/healthz`).then(
      (r) => r.ok,
      () => false,
    );
    if (ok) break;
    if (i > 50) throw new Error('wardlockd did not start');
    await new Promise((r) => setTimeout(r, 100));
  }
  status =
    spawnSync(
      'pnpm',
      [
        '--filter',
        '@wardlock/web',
        'exec',
        'playwright',
        'test',
        '-c',
        'test/playwright.config.ts',
      ],
      {
        stdio: 'inherit',
        env: { ...process.env, WARDLOCK_ACCEPTANCE_ORIGIN: origin },
      },
    ).status ?? 1;
} finally {
  server.kill('SIGTERM');
  rmSync(dataDir, { recursive: true, force: true });
}
process.exit(status);
