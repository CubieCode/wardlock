// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from './app.js';

afterEach(cleanup);

const client = (respond: () => Promise<Response>) =>
  createApiClient({ baseUrl: 'http://wardlock.test', fetch: respond });

describe('App', () => {
  it('shows the connected server version', async () => {
    render(
      <App
        api={client(async () =>
          Response.json({ status: 'ok', version: '0.0.0-test' }),
        )}
      />,
    );
    expect(await screen.findByText('Connected')).toBeTruthy();
    expect(screen.getByText('0.0.0-test')).toBeTruthy();
  });

  it('explains how to recover when the server is unreachable', async () => {
    render(
      <App
        api={client(async () => Promise.reject(new TypeError('offline')))}
      />,
    );
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Could not reach the server',
    );
  });
});
