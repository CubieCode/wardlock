// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { defineConfig, devices } from '@playwright/test';

// Run through `pnpm browser:test`, which starts a real wardlockd and sets
// WARDLOCK_ACCEPTANCE_ORIGIN (scripts/browser-acceptance.mjs).
export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: '../../../.local/test-results',
  forbidOnly: !!process.env.CI,
  reporter: 'list',
  use: { trace: 'retain-on-failure' },
  projects: [
    {
      name: 'desktop',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'mobile',
      use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 } },
    },
  ],
});
