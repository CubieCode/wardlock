// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { NotificationsPage } from './notifications-page.js';

const allOn = {
  domainVerified: true,
  domainFailing: true,
  linkUsedUp: true,
  linksExpiring: true,
  tokenExpiring: true,
  previewExpiring: true,
};

type Handler = (body: unknown) => Response | Promise<Response>;

function server(get: Handler, patch?: Handler) {
  const patches: unknown[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      if (request.method === 'GET') return get(undefined);
      const body: unknown = await request.json();
      patches.push(body);
      return patch ? patch(body) : Response.json(allOn);
    },
  });
  return { api, patches };
}

const box = (name: string) =>
  screen.findByRole<HTMLInputElement>('checkbox', { name });

afterEach(cleanup);

describe('NotificationsPage', () => {
  it('says it is loading, then lists every setting as stored', async () => {
    const { api } = server(() =>
      Response.json({ ...allOn, linksExpiring: false }),
    );
    render(<NotificationsPage api={api} />);
    expect(
      screen.getByRole('heading', { level: 1, name: 'Notifications' }),
    ).toBeTruthy();
    expect(screen.getByText('Loading your settings…')).toBeTruthy();
    expect((await box('A domain is verified')).checked).toBe(true);
    expect((await box('A domain is failing')).checked).toBe(true);
    expect((await box('A share link is used up')).checked).toBe(true);
    expect((await box('Share links expire soon')).checked).toBe(false);
    expect((await box('An API token expires soon')).checked).toBe(true);
    expect((await box('A preview expires soon')).checked).toBe(true);
    expect(screen.queryByText(/Loading/)).toBeNull();
  });

  it('shows the withheld-file email as on and not changeable', async () => {
    const { api } = server(() => Response.json(allOn));
    render(<NotificationsPage api={api} />);
    const withheld = await box('A file is withheld');
    expect(withheld.checked).toBe(true);
    expect(withheld.disabled).toBe(true);
    expect(screen.getByText(/cannot be turned off/)).toBeTruthy();
  });

  it('saves a change on its own and confirms it', async () => {
    const { api, patches } = server(
      () => Response.json(allOn),
      () => Response.json({ ...allOn, tokenExpiring: false }),
    );
    render(<NotificationsPage api={api} />);
    const token = await box('An API token expires soon');
    fireEvent.click(token);
    expect(token.checked).toBe(false);
    expect(await screen.findByText('Email turned off')).toBeTruthy();
    expect(patches).toEqual([{ tokenExpiring: false }]);
    expect(token.checked).toBe(false);

    fireEvent.click(token);
    expect(await screen.findByText('Email turned on')).toBeTruthy();
    expect(patches).toEqual([
      { tokenExpiring: false },
      { tokenExpiring: true },
    ]);
  });

  it('puts the box back and says why when the server refuses', async () => {
    const { api } = server(
      () => Response.json(allOn),
      () => new Response(null, { status: 500 }),
    );
    render(<NotificationsPage api={api} />);
    const used = await box('A share link is used up');
    fireEvent.click(used);
    expect(
      await screen.findByText(/“A share link is used up” was not changed/),
    ).toBeTruthy();
    await waitFor(() => expect(used.checked).toBe(true));
    expect(screen.queryByText('Email turned off')).toBeNull();
  });

  it('puts the box back when the server cannot be reached', async () => {
    const { api } = server(
      () => Response.json(allOn),
      () => Promise.reject(new TypeError('offline')),
    );
    render(<NotificationsPage api={api} />);
    const failing = await box('A domain is failing');
    fireEvent.click(failing);
    expect(await screen.findByText(/Could not reach the server/)).toBeTruthy();
    await waitFor(() => expect(failing.checked).toBe(true));
  });

  it('clears the problem once a later change saves', async () => {
    let refuse = true;
    const { api } = server(
      () => Response.json(allOn),
      () =>
        refuse ? new Response(null, { status: 500 }) : Response.json(allOn),
    );
    render(<NotificationsPage api={api} />);
    const preview = await box('A preview expires soon');
    fireEvent.click(preview);
    await screen.findByText(/was not changed/);
    refuse = false;
    fireEvent.click(preview);
    await screen.findByText('Email turned off');
    expect(screen.queryByText(/was not changed/)).toBeNull();
  });

  it('says so when the settings cannot be loaded', async () => {
    const { api } = server(() => new Response(null, { status: 500 }));
    render(<NotificationsPage api={api} />);
    expect(
      await screen.findByText(/Could not load your settings/),
    ).toBeTruthy();
    expect(screen.queryByRole('checkbox')).toBeNull();
  });

  it('says so when the server cannot be reached for the settings', async () => {
    const { api } = server(() => Promise.reject(new TypeError('offline')));
    render(<NotificationsPage api={api} />);
    expect(
      await screen.findByText(/Could not load your settings/),
    ).toBeTruthy();
  });
});
