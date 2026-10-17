// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

/**
 * Typed client for the Wardlock API. Types come from schema.gen.ts, which
 * `pnpm codegen` generates from packages/api-spec/openapi.yaml.
 */
import createClient from 'openapi-fetch';
import type { components, paths } from './schema.gen.js';

export type { components, paths };
export type Health = components['schemas']['Health'];

export interface ApiClientOptions {
  /** Server origin; defaults to the page's own origin. */
  baseUrl?: string;
  /** Replaces the global fetch (tests). */
  fetch?: typeof globalThis.fetch;
}

/** Creates a client that sends the dashboard session cookie with requests. */
export function createApiClient({
  baseUrl = '',
  fetch,
}: ApiClientOptions = {}) {
  return createClient<paths>({
    baseUrl,
    credentials: 'same-origin',
    ...(fetch ? { fetch } : {}),
  });
}

export type ApiClient = ReturnType<typeof createApiClient>;
