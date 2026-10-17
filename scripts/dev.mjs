// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// `pnpm dev`: builds workspace packages, starts the dashboard's Vite dev
// server, builds and starts wardlockd with dashboard pages proxied to Vite.
// Ports and data directory come from .env (scripts/worktree-setup.sh).
import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';

if (existsSync('.env')) process.loadEnvFile('.env');
const env = { ...process.env };
const webPort = env.WARDLOCK_WEB_DEV_PORT || '5173';
env.WARDLOCK_DEV_DASHBOARD_URL = `http://127.0.0.1:${webPort}`;
env.WARDLOCK_DATA_DIR ||= '.local/data';
env.WARDLOCK_LOG_FORMAT ||= 'text';

/** @param {string} cmd @param {string[]} args */
const step = (cmd, args) => {
  const { status } = spawnSync(cmd, args, { stdio: 'inherit', env });
  if (status !== 0) process.exit(status ?? 1);
};
step('pnpm', ['exec', 'turbo', 'build', '--filter=./packages/*']);
step('go', [
  'build',
  '-o',
  '.local/bin/wardlockd-dev',
  './apps/server/cmd/wardlockd',
]);

const children = [
  spawn('pnpm', ['--filter', '@wardlock/web', 'dev'], {
    stdio: 'inherit',
    env,
  }),
  spawn('.local/bin/wardlockd-dev', ['serve'], { stdio: 'inherit', env }),
];
const host =
  env.WARDLOCK_DASHBOARD_HOST ||
  env.WARDLOCK_BASE_DOMAIN ||
  'wardlock.localhost';
const port = (env.WARDLOCK_HTTP_ADDR || '127.0.0.1:8080').split(':').at(-1);
console.log(
  `\nDashboard: http://${host}:${port}  Hello site: http://hello.${env.WARDLOCK_BASE_DOMAIN || 'wardlock.localhost'}:${port}\n`,
);

let stopping = false;
/** @param {number} code */
const stop = (code) => {
  if (stopping) return;
  stopping = true;
  for (const child of children) child.kill('SIGTERM');
  process.exitCode = code;
};
for (const child of children) child.on('exit', (code) => stop(code ?? 0));
process.on('SIGINT', () => stop(0));
process.on('SIGTERM', () => stop(0));
