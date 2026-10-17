// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Package boundaries (docs/MODULES.md). Go boundaries live in .golangci.yml.
const agplPackages = ['@wardlock/ui', '@wardlock/web'];

export default tseslint.config(
  {
    ignores: [
      '**/dist/**',
      '**/node_modules/**',
      '**/.turbo/**',
      '.local/**',
      '.agents/skills/**',
      '.claude/skills/**',
      'coverage/**',
      'test-results/**',
      'playwright-report/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  { languageOptions: { globals: { ...globals.node, ...globals.browser } } },
  {
    files: ['apps/web/**/*.{ts,tsx}', 'packages/ui/**/*.{ts,tsx}'],
    ...reactHooks.configs.flat.recommended,
  },
  {
    // MIT packages must never import AGPL code.
    files: ['packages/apiclient-ts/**/*.ts', 'packages/api-spec/**'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: [...agplPackages, '**/apps/**', '**/packages/ui/**'],
              message: 'MIT packages must not import AGPL code.',
            },
          ],
        },
      ],
    },
  },
  {
    files: ['packages/ui/src/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['@wardlock/*', '!@wardlock/ui', '**/apps/**'],
              message:
                'The design system is domain-free: no API client or app code.',
            },
          ],
        },
      ],
    },
  },
  {
    // The dashboard reaches the server only through @wardlock/apiclient-ts.
    files: ['apps/web/src/**/*.{ts,tsx}'],
    ignores: ['apps/web/src/**/*.test.{ts,tsx}'],
    rules: {
      'no-restricted-globals': [
        'error',
        ...['fetch', 'XMLHttpRequest', 'EventSource', 'WebSocket'].map(
          (name) => ({
            name,
            message: 'Call the server through @wardlock/apiclient-ts.',
          }),
        ),
      ],
      'no-restricted-properties': [
        'error',
        ...['window', 'globalThis', 'self'].map((object) => ({
          object,
          property: 'fetch',
          message: 'Call the server through @wardlock/apiclient-ts.',
        })),
      ],
      'no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: 'openapi-fetch',
              message: 'Use @wardlock/apiclient-ts.',
            },
          ],
          patterns: [
            {
              group: ['**/packages/**', '**/apps/server/**'],
              message: 'Import workspace packages by name.',
            },
          ],
        },
      ],
    },
  },
  prettier,
);
