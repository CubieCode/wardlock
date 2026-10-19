// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: [
      'apps/web/src/**/*.test.{ts,tsx}',
      'packages/*/src/**/*.test.{ts,tsx}',
      'scripts/**/*.test.mjs',
      'docs-site/src/**/*.test.mjs',
    ],
    environment: 'node',
  },
});
