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
      // Creates the first owner and invites one per project and theme.
      name: 'setup',
      testMatch: 'setup.spec.ts',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'desktop',
      dependencies: ['setup'],
      testIgnore: 'setup.spec.ts',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'mobile',
      dependencies: ['setup'],
      testIgnore: 'setup.spec.ts',
      use: { ...devices['Pixel 7'], viewport: { width: 390, height: 844 } },
    },
  ],
});
