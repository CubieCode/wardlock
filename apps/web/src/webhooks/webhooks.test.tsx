// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { refreshInterval, WebhookPage } from './webhook-page.js';
import { WebhooksPage } from './webhooks-page.js';

// Synthetic: shaped like a real signing secret, valid nowhere.
const secret = 'wlwh_TESTonlyTESTonlyTESTonlyTESTonlyTESTonlyTES';
const id = '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e30';
const deliveryId = '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e31';
const url = 'https://hooks.example.com/services/receiver-secret';
const hourAgo = () => Date.now() - 3600_000;

const webhook = (over: Record<string, unknown> = {}) => ({
  id,
  url,
  description: 'Deploys channel',
  enabled: true,
  createdAt: Date.UTC(2026, 9, 1),
  ...over,
});

const delivery = (over: Record<string, unknown> = {}) => ({
  id: deliveryId,
  eventId: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e32',
  type: 'deploy.activated',
  status: 'succeeded',
  attempts: 1,
  responseCode: 204,
  durationMs: 120,
  createdAt: hourAgo(),
  ...over,
});

type Handler = (url: URL, body: unknown) => Response | Promise<Response>;

function fakeServer(routes: Record<string, Handler>) {
  const calls: { route: string; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const target = new URL(request.url);
      const route = `${request.method} ${target.pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route, body });
      const handler = routes[route];
      if (handler) return handler(target, body);
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return {
    api,
    calls,
    bodyOf: (route: string) => calls.findLast((c) => c.route === route)?.body,
    count: (route: string) => calls.filter((c) => c.route === route).length,
  };
}

const json =
  (value: unknown, status = 200): Handler =>
  () =>
    Response.json(value, { status });

const refuse = (
  status: number,
  code: string,
  message: string,
  target?: string,
): Handler => json({ code, message, target }, status);

const fail: Handler = () =>
  Response.json({ code: 'internal', message: 'Boom.' }, { status: 500 });

const listRoute = 'GET /api/v0/webhooks';
const createRoute = 'POST /api/v0/webhooks';
const one = `/api/v0/webhooks/${id}`;
const getRoute = `GET ${one}`;
const patchRoute = `PATCH ${one}`;
const deleteRoute = `DELETE ${one}`;
const deliveriesRoute = `GET ${one}/deliveries`;
const testRoute = `POST ${one}/test`;
const redeliverRoute = `POST ${one}/deliveries/${deliveryId}/redeliver`;

/** One webhook's page, with these routes over a healthy default. */
function renderWebhook(
  routes: Record<string, Handler> = {},
  initial = webhook(),
  deliveries: unknown[] = [],
) {
  const server = fakeServer({
    [getRoute]: json(initial),
    [deliveriesRoute]: json({ items: deliveries }),
    ...routes,
  });
  render(<WebhookPage api={server.api} webhookId={id} />);
  return server;
}

const dialog = () => screen.getByRole('dialog');
const click = (name: string | RegExp) =>
  fireEvent.click(screen.getByRole('button', { name }));
const clickInDialog = (name: string) =>
  fireEvent.click(within(dialog()).getByRole('button', { name }));
const fill = (name: RegExp, value: string) =>
  fireEvent.change(screen.getByRole('textbox', { name }), {
    target: { value },
  });

async function openAdd() {
  const button = await screen.findByRole('button', { name: 'Add webhook' });
  await waitFor(() => expect(button).toHaveProperty('disabled', false));
  fireEvent.click(button);
  await screen.findByRole('heading', { name: 'Add a webhook' });
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  window.history.replaceState(null, '', '/');
});

describe('the list', () => {
  it('says so while loading, then when there are no webhooks', async () => {
    const server = fakeServer({ [listRoute]: json({ items: [] }) });
    render(<WebhooksPage api={server.api} />);
    screen.getByRole('status');
    await screen.findByRole('heading', { name: 'No webhooks yet' });
    expect(document.title).toContain('Webhooks');
  });

  it('shows each webhook with its status and what it wants', async () => {
    const server = fakeServer({
      [listRoute]: json({
        items: [
          webhook(),
          webhook({
            id: 'b',
            description: '',
            url: 'https://b.example.net/x',
            events: ['site.created', 'site.deleted'],
            sites: ['docs'],
            failingSince: hourAgo(),
          }),
          webhook({
            id: 'c',
            description: 'Off',
            enabled: false,
            disabledReason: 'owner',
          }),
          webhook({
            id: 'd',
            description: 'Broken',
            enabled: false,
            disabledReason: 'failing',
          }),
        ],
      }),
    });
    render(<WebhooksPage api={server.api} />);
    const link = await screen.findByRole('link', { name: 'Deploys channel' });
    expect(link.getAttribute('href')).toBe(`/webhooks/${id}`);
    const rows = screen.getAllByRole('row').slice(1);
    within(rows[0]!).getByText('Active');
    within(rows[0]!).getByText('All events');
    within(rows[0]!).getByText('All sites');
    // Without a description, the host names it.
    within(rows[1]!).getByRole('link', { name: 'b.example.net' });
    within(rows[1]!).getByText('Failing');
    within(rows[1]!).getByText('2 events');
    within(rows[1]!).getByText('1 site');
    within(rows[2]!).getByText('Turned off');
    within(rows[3]!).getByText('Turned off after failing');
  });

  it('says when the list could not be read, and tries again', async () => {
    let answer: Handler = fail;
    const server = fakeServer({
      [listRoute]: (u, b) => answer(u, b),
    });
    render(<WebhooksPage api={server.api} />);
    await screen.findByText(/Could not load your webhooks/);
    expect(screen.getByRole('button', { name: 'Add webhook' })).toHaveProperty(
      'disabled',
      true,
    );
    answer = json({ items: [] });
    click('Try again');
    await screen.findByRole('heading', { name: 'No webhooks yet' });
  });

  it('stops offering Add webhook at the limit of 10', async () => {
    const items = Array.from({ length: 10 }, (_, n) =>
      webhook({ id: String(n) }),
    );
    const server = fakeServer({ [listRoute]: json({ items }) });
    render(<WebhooksPage api={server.api} />);
    await screen.findByText(/the most webhooks allowed/);
    expect(screen.getByRole('button', { name: 'Add webhook' })).toHaveProperty(
      'disabled',
      true,
    );
  });
});

describe('adding a webhook', () => {
  const created = (over: Record<string, unknown> = {}) =>
    json({ webhook: webhook(over), secret }, 201);

  it('sends everything as all, shows the secret once, then opens the webhook', async () => {
    const server = fakeServer({
      [listRoute]: json({ items: [] }),
      [createRoute]: created(),
    });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    fill(/Endpoint URL/, ` ${url} `);
    fill(/Description/, 'Deploys channel');
    clickInDialog('Add webhook');

    await screen.findByRole('heading', { name: 'Copy your signing secret' });
    expect(server.bodyOf(createRoute)).toEqual({
      url,
      description: 'Deploys channel',
    });
    const field = screen.getByRole('textbox', { name: 'Signing secret' });
    expect((field as HTMLInputElement).value).toBe(secret);
    screen.getByText(/cannot be shown again/);
    screen.getByRole('button', { name: /Copy/ });
    // A stray Escape cannot lose it.
    fireEvent(dialog(), new Event('cancel', { cancelable: true }));
    screen.getByRole('textbox', { name: 'Signing secret' });

    clickInDialog('Done');
    await waitFor(() =>
      expect(
        screen.queryByRole('textbox', { name: 'Signing secret' }),
      ).toBeNull(),
    );
    expect(document.body.textContent).not.toContain(secret);
    expect(window.location.pathname).toBe(`/webhooks/${id}`);
  });

  it('sends only the chosen events and sites', async () => {
    const server = fakeServer({
      [listRoute]: json({ items: [] }),
      [createRoute]: created(),
      'GET /api/v0/sites': (target) =>
        target.searchParams.get('cursor')
          ? json({ items: [{ name: 'shop' }] })(target, null)
          : json({ items: [{ name: 'docs' }], nextCursor: 'more' })(
              target,
              null,
            ),
    });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    fill(/Endpoint URL/, url);
    fireEvent.click(screen.getByRole('radio', { name: 'Only these events' }));
    fireEvent.click(screen.getByRole('checkbox', { name: /^link\.revoked/ }));
    fireEvent.click(screen.getByRole('checkbox', { name: /^site\.created/ }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only these sites' }));
    await screen.findByRole('checkbox', { name: 'shop' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'docs' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'shop' }));
    clickInDialog('Add webhook');
    await screen.findByRole('heading', { name: 'Copy your signing secret' });
    expect(server.bodyOf(createRoute)).toEqual({
      url,
      description: '',
      events: ['site.created', 'link.revoked'],
      sites: ['docs', 'shop'],
    });
  });

  it('asks for what is missing before sending', async () => {
    const server = fakeServer({
      [listRoute]: json({ items: [] }),
      'GET /api/v0/sites': json({ items: [] }),
    });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    fireEvent.click(screen.getByRole('radio', { name: 'Only these events' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only these sites' }));
    await screen.findByText('You have no sites yet.');
    clickInDialog('Add webhook');
    await screen.findByText('Enter the address to send events to.');
    screen.getByText('Choose at least one event, or All events.');
    screen.getByText('Choose at least one site, or All sites.');
    expect(server.count(createRoute)).toBe(0);
  });

  it.each([
    ['url', /Endpoint URL/, 'url must use https.'],
    [
      'description',
      /Description/,
      'description must be at most 200 characters.',
    ],
  ])(
    'shows the server refusing %s against its field',
    async (target, name, text) => {
      const server = fakeServer({
        [listRoute]: json({ items: [] }),
        [createRoute]: refuse(400, 'invalid_request', text, target),
      });
      render(<WebhooksPage api={server.api} />);
      await openAdd();
      fill(/Endpoint URL/, 'https://x.example.com');
      clickInDialog('Add webhook');
      await screen.findByText(text);
      const field = screen.getByRole('textbox', { name });
      expect(field.getAttribute('aria-invalid')).toBe('true');
    },
  );

  it.each([
    ['events', 'Unknown event x.'],
    ['sites', 'A webhook can list at most 100 sites.'],
  ])(
    'shows the server refusing %s against that group',
    async (target, text) => {
      const server = fakeServer({
        [listRoute]: json({ items: [] }),
        [createRoute]: refuse(400, 'invalid_request', text, target),
      });
      render(<WebhooksPage api={server.api} />);
      await openAdd();
      fill(/Endpoint URL/, url);
      clickInDialog('Add webhook');
      const legend = target === 'events' ? 'Events' : 'Sites';
      const group = await screen.findByRole('group', { name: legend });
      await within(group).findByText(text);
    },
  );

  it('says a chosen site is gone, the limit, and a server failure', async () => {
    let answer: Handler = refuse(404, 'not_found', 'Not found.');
    const server = fakeServer({
      [listRoute]: json({ items: [] }),
      [createRoute]: (u, b) => answer(u, b),
    });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    fill(/Endpoint URL/, url);
    clickInDialog('Add webhook');
    await screen.findByText('One of these sites no longer exists.');
    answer = refuse(413, 'limit_exceeded', 'Delete one first.');
    clickInDialog('Add webhook');
    await screen.findByText('Delete one first.');
    answer = fail;
    clickInDialog('Add webhook');
    await screen.findByText(/Something went wrong/);
  });

  it('says when the sites could not be read', async () => {
    const server = fakeServer({
      [listRoute]: json({ items: [] }),
      'GET /api/v0/sites': fail,
    });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    fireEvent.click(screen.getByRole('radio', { name: 'Only these sites' }));
    await screen.findByText(/Could not load your sites/);
  });

  it('cancels without sending', async () => {
    const server = fakeServer({ [listRoute]: json({ items: [] }) });
    render(<WebhooksPage api={server.api} />);
    await openAdd();
    clickInDialog('Cancel');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(server.count(createRoute)).toBe(0);
  });
});

describe('one webhook', () => {
  it('shows its URL to its owner, but not in the title or heading', async () => {
    renderWebhook();
    await screen.findByRole('heading', { level: 1, name: 'Deploys channel' });
    screen.getByText(url);
    screen.getByText('Active');
    expect(document.title).not.toContain('receiver-secret');
    expect(document.title).toContain('Webhook');
  });

  it('names a webhook with no description by its host alone', async () => {
    renderWebhook({}, webhook({ description: '' }));
    await screen.findByRole('heading', {
      level: 1,
      name: 'hooks.example.com',
    });
  });

  it('lists the events and sites it asked for', async () => {
    renderWebhook(
      {},
      webhook({ events: ['site.created'], sites: ['docs', 'shop'] }),
    );
    await screen.findByText('site.created');
    screen.getByText('docs');
    screen.getByText('shop');
  });

  it('says when every site it listed was deleted', async () => {
    renderWebhook({}, webhook({ sites: [] }));
    await screen.findByText(/the sites it listed were deleted/);
  });

  it('says a missing or foreign webhook was not found', async () => {
    renderWebhook({ [getRoute]: refuse(404, 'not_found', 'Not found.') });
    await screen.findByRole('heading', { name: 'Webhook not found' });
    screen.getByRole('link', { name: 'All webhooks' });
  });

  it('says when it could not be read, and tries again', async () => {
    let answer: Handler = fail;
    renderWebhook({ [getRoute]: (u, b) => answer(u, b) });
    await screen.findByText(/Could not load this webhook/);
    answer = json(webhook());
    click('Try again');
    await screen.findByRole('heading', { level: 1, name: 'Deploys channel' });
  });

  it('warns while deliveries are failing', async () => {
    renderWebhook({}, webhook({ failingSince: hourAgo() }));
    await screen.findByText(/Deliveries started failing/);
    screen.getByText('Failing');
    expect(screen.queryByRole('button', { name: 'Turn on' })).toBeNull();
  });
});

describe('editing', () => {
  it('sends only what changed', async () => {
    const server = renderWebhook({
      [patchRoute]: json(webhook({ description: 'Renamed' })),
    });
    await screen.findByRole('button', { name: 'Edit' });
    click('Edit');
    const field = (await screen.findByRole('textbox', {
      name: /Endpoint URL/,
    })) as HTMLInputElement;
    expect(field.value).toBe(url);
    fill(/Description/, 'Renamed');
    clickInDialog('Save changes');
    await screen.findByRole('heading', { level: 1, name: 'Renamed' });
    expect(server.bodyOf(patchRoute)).toEqual({ description: 'Renamed' });
    await screen.findByText('Changes saved.');
  });

  it('widens events and sites back to all', async () => {
    const server = renderWebhook(
      { [patchRoute]: json(webhook()) },
      webhook({ events: ['site.created'], sites: ['docs'] }),
    );
    await screen.findByRole('button', { name: 'Edit' });
    click('Edit');
    fireEvent.click(await screen.findByRole('radio', { name: /^All events/ }));
    fireEvent.click(screen.getByRole('radio', { name: /^All sites/ }));
    clickInDialog('Save changes');
    await screen.findByText('Changes saved.');
    expect(server.bodyOf(patchRoute)).toEqual({
      allEvents: true,
      allSites: true,
    });
  });

  it('replaces a list, and leaves one the owner did not touch', async () => {
    const server = renderWebhook(
      {
        [patchRoute]: json(webhook()),
        'GET /api/v0/sites': json({ items: [{ name: 'docs' }] }),
      },
      webhook({ events: ['site.created'], sites: [] }),
    );
    await screen.findByRole('button', { name: 'Edit' });
    click('Edit');
    fireEvent.click(
      await screen.findByRole('checkbox', { name: /^link\.created/ }),
    );
    clickInDialog('Save changes');
    await screen.findByText('Changes saved.');
    expect(server.bodyOf(patchRoute)).toEqual({
      events: ['site.created', 'link.created'],
    });
  });

  it('shows a refusal against its field and stays open', async () => {
    renderWebhook({
      [patchRoute]: refuse(
        400,
        'invalid_request',
        'url must use https.',
        'url',
      ),
    });
    await screen.findByRole('button', { name: 'Edit' });
    click('Edit');
    fill(/Endpoint URL/, 'http://hooks.example.com');
    clickInDialog('Save changes');
    await within(dialog()).findByText('url must use https.');
  });
});

describe('turning off and on', () => {
  it('turns off, then back on', async () => {
    const off = webhook({ enabled: false, disabledReason: 'owner' });
    let current = webhook();
    const server = renderWebhook({
      [getRoute]: () => Response.json(current),
      [patchRoute]: (_, body) => {
        current = (body as { enabled: boolean }).enabled ? webhook() : off;
        return Response.json(current);
      },
    });
    await screen.findByRole('button', { name: 'Turn off' });
    click('Turn off');
    await screen.findByText(/Anything still queued will be dropped/);
    expect(server.bodyOf(patchRoute)).toEqual({ enabled: false });
    screen.getByText('Turned off');
    await screen.findByText(/This webhook is turned off/);
    expect(screen.getByRole('button', { name: 'Send test' })).toHaveProperty(
      'disabled',
      true,
    );

    click('Turn on');
    await screen.findByText(/New events will be sent/);
    expect(server.bodyOf(patchRoute)).toEqual({ enabled: true });
    screen.getByText('Active');
  });

  it('says why a webhook was turned off after failing, and turns it on', async () => {
    const at = hourAgo();
    let current = webhook({
      enabled: false,
      disabledReason: 'failing',
      disabledAt: at,
      failingSince: at - 3 * 24 * 3600_000,
    });
    const server = renderWebhook({
      [getRoute]: () => Response.json(current),
      [patchRoute]: () => {
        current = webhook();
        return Response.json(current);
      },
    });
    await screen.findByText(/deliveries failed for 3 days/);
    screen.getByText('Turned off after failing');
    click('Turn on');
    await screen.findByText(/New events will be sent/);
    expect(server.bodyOf(patchRoute)).toEqual({ enabled: true });
    expect(screen.queryByText(/deliveries failed for 3 days/)).toBeNull();
  });

  it('says when the change failed', async () => {
    renderWebhook({ [patchRoute]: fail });
    await screen.findByRole('button', { name: 'Turn off' });
    click('Turn off');
    await screen.findByText(/Something went wrong/);
    screen.getByText('Active');
  });
});

describe('deleting', () => {
  it('names the webhook and does nothing on Cancel', async () => {
    const server = renderWebhook();
    await screen.findByRole('button', { name: 'Delete' });
    click('Delete');
    await screen.findByRole('heading', { name: 'Delete “Deploys channel”?' });
    clickInDialog('Cancel');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(server.count(deleteRoute)).toBe(0);
  });

  it('never names the URL when asking', async () => {
    renderWebhook({}, webhook({ description: '' }));
    await screen.findByRole('button', { name: 'Delete' });
    click('Delete');
    await screen.findByRole('heading', {
      name: 'Delete “hooks.example.com”?',
    });
    expect(dialog().textContent).not.toContain('receiver-secret');
  });

  it.each([
    ['deletes it', 204],
    ['treats one already gone as deleted', 404],
  ])('%s and returns to the list', async (_, status) => {
    window.history.replaceState(null, '', `/webhooks/${id}`);
    const server = renderWebhook({
      [deleteRoute]: () =>
        status === 204
          ? new Response(null, { status })
          : Response.json({ code: 'not_found', message: 'No.' }, { status }),
    });
    await screen.findByRole('button', { name: 'Delete' });
    click('Delete');
    clickInDialog('Delete webhook');
    await waitFor(() => expect(window.location.pathname).toBe('/webhooks'));
    expect(server.count(deleteRoute)).toBe(1);
  });

  it('stays open and says so when it failed', async () => {
    renderWebhook({ [deleteRoute]: fail });
    await screen.findByRole('button', { name: 'Delete' });
    click('Delete');
    clickInDialog('Delete webhook');
    await within(dialog()).findByText(/Something went wrong/);
  });
});

describe('sending a test', () => {
  it('queues a ping and lists it', async () => {
    const ping = delivery({
      type: 'ping',
      status: 'pending',
      attempts: 0,
      responseCode: undefined,
      durationMs: undefined,
    });
    renderWebhook({ [testRoute]: json(ping, 202) });
    await screen.findByRole('heading', { name: 'No deliveries yet' });
    click('Send test');
    await screen.findByText('Test queued. It is listed below.');
    const row = screen.getByRole('cell', { name: 'ping' }).closest('tr')!;
    within(row).getByText('Queued');
  });

  it('says a webhook turned off elsewhere refused it, and shows it off', async () => {
    let current = webhook();
    renderWebhook({
      [getRoute]: () => Response.json(current),
      [testRoute]: () => {
        current = webhook({ enabled: false, disabledReason: 'owner' });
        return Response.json(
          { code: 'webhook_disabled', message: 'Off.' },
          { status: 409 },
        );
      },
    });
    await screen.findByRole('button', { name: 'Send test' });
    click('Send test');
    await screen.findByText(/so nothing was sent/);
    await screen.findByRole('button', { name: 'Turn on' });
  });

  it('says when it failed', async () => {
    renderWebhook({ [testRoute]: fail });
    await screen.findByRole('button', { name: 'Send test' });
    click('Send test');
    await screen.findByText(/Something went wrong/);
  });
});

describe('deliveries', () => {
  const cells = (type: string) => {
    const row = screen.getByRole('cell', { name: type }).closest('tr')!;
    return within(row);
  };

  it('shows status, response code, duration and attempts', async () => {
    renderWebhook({}, webhook(), [
      delivery(),
      delivery({
        id: 'b',
        type: 'site.created',
        status: 'failed',
        attempts: 9,
        responseCode: 502,
        error: 'status',
        durationMs: 1500,
      }),
      delivery({
        id: 'c',
        type: 'site.deleted',
        status: 'pending',
        attempts: 2,
        responseCode: undefined,
        durationMs: 10000,
        error: 'timeout',
      }),
      delivery({
        id: 'd',
        type: 'link.created',
        status: 'dropped',
        attempts: 1,
        responseCode: undefined,
        durationMs: 3,
        error: 'dns',
      }),
      delivery({
        id: 'e',
        type: 'ping',
        status: 'pending',
        attempts: 0,
        responseCode: undefined,
        durationMs: undefined,
        redeliveryOf: deliveryId,
      }),
    ]);
    await screen.findByRole('cell', { name: 'deploy.activated' });
    const good = cells('deploy.activated');
    good.getByText('Delivered');
    good.getByRole('cell', { name: '204' });
    good.getByRole('cell', { name: '120 ms' });
    good.getByRole('cell', { name: '1' });
    good.getByRole('cell', { name: '1 hour ago' });
    const failed = cells('site.created');
    failed.getByText('Failed');
    failed.getByRole('cell', { name: '502' });
    failed.getByRole('cell', { name: '1.5 s' });
    const retrying = cells('site.deleted');
    retrying.getByText('Failed, retrying');
    retrying.getByRole('cell', { name: 'Timed out' });
    const dropped = cells('link.created');
    dropped.getByText('Dropped');
    dropped.getByRole('cell', { name: 'Host not found' });
    const queued = screen.getByText('Redelivery').closest('tr')!;
    within(queued).getByText('Queued');
    expect(within(queued).getAllByRole('cell', { name: 'None' })).toHaveLength(
      2,
    );
  });

  it('redelivers one and lists the new delivery', async () => {
    const again = delivery({
      id: 'new',
      status: 'pending',
      attempts: 0,
      responseCode: undefined,
      durationMs: undefined,
      redeliveryOf: deliveryId,
    });
    const server = renderWebhook(
      { [redeliverRoute]: json(again, 202) },
      webhook(),
      [delivery()],
    );
    fireEvent.click(await screen.findByRole('button', { name: /^Redeliver/ }));
    await screen.findByText('Queued to be sent again.');
    expect(server.count(redeliverRoute)).toBe(1);
    screen.getByText('Redelivery');
    expect(screen.getAllByRole('button', { name: /^Redeliver/ })).toHaveLength(
      2,
    );
  });

  it('cannot redeliver while the webhook is off', async () => {
    renderWebhook({}, webhook({ enabled: false, disabledReason: 'owner' }), [
      delivery(),
    ]);
    const button = await screen.findByRole('button', { name: /^Redeliver/ });
    expect(button).toHaveProperty('disabled', true);
  });

  it('shows the refusal when a redelivery fails', async () => {
    renderWebhook({ [redeliverRoute]: fail }, webhook(), [delivery()]);
    fireEvent.click(await screen.findByRole('button', { name: /^Redeliver/ }));
    await screen.findByText(/Something went wrong/);
  });

  it('keeps the webhook when its deliveries cannot be read, and refreshes', async () => {
    let answer: Handler = fail;
    renderWebhook({ [deliveriesRoute]: (u, b) => answer(u, b) });
    await screen.findByText(/Could not load the deliveries/);
    screen.getByRole('heading', { level: 1, name: 'Deploys channel' });
    answer = json({ items: [delivery()] });
    click('Refresh');
    await screen.findByRole('cell', { name: 'deploy.activated' });
  });

  it('looks again while one is pending, and stops when none is', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let items = [
      delivery({
        status: 'pending',
        attempts: 0,
        responseCode: undefined,
        durationMs: undefined,
      }),
    ];
    const server = renderWebhook({
      [deliveriesRoute]: () => Response.json({ items }),
    });
    await screen.findByText('Queued');
    items = [delivery()];
    await act(() => vi.advanceTimersByTimeAsync(refreshInterval));
    await screen.findByText('Delivered');
    const reads = server.count(deliveriesRoute);
    await act(() => vi.advanceTimersByTimeAsync(refreshInterval * 3));
    expect(server.count(deliveriesRoute)).toBe(reads);
  });
});
