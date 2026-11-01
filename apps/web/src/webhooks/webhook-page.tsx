// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  PageHeader,
  StatusBadge,
} from '@wardlock/ui';
import { useCallback, useEffect, useRef, useState } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { navigate } from '../shell/router.js';
import { useTitle } from '../shell/page.js';
import { Time } from '../sites/format.js';
import { DeleteWebhookDialog } from './delete-webhook.js';
import { DeliveriesTable } from './deliveries.js';
import {
  webhookName,
  webhookStatus,
  type Webhook,
  type WebhookDelivery,
} from './format.js';
import { WebhookFormDialog } from './webhook-form.js';
import './webhooks.css';

/** How often the page looks again while a delivery is still pending. */
export const refreshInterval = 5000;

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'gone' }
  | {
      kind: 'ready';
      webhook: Webhook;
      /** Null when the deliveries could not be read. */
      deliveries: WebhookDelivery[] | null;
    };

type Message = { tone: 'info' | 'danger'; text: string };

/** Why the webhook is off or in trouble, and how to put it right. */
function Trouble({
  webhook,
  busy,
  onTurnOn,
}: {
  webhook: Webhook;
  busy: boolean;
  onTurnOn: () => void;
}) {
  if (webhook.enabled) {
    if (webhook.failingSince === undefined) return null;
    return (
      <Notice tone="warning">
        Deliveries started failing <Time at={webhook.failingSince} />. If they
        keep failing for 3 days, Wardlock turns this webhook off.
      </Notice>
    );
  }
  const failed = webhook.disabledReason === 'failing';
  return (
    <div className="webhook-off">
      <Notice tone={failed ? 'warning' : 'info'}>
        {failed ? (
          <>
            Wardlock turned this webhook off{' '}
            {webhook.disabledAt !== undefined && (
              <>
                <Time at={webhook.disabledAt} />{' '}
              </>
            )}
            because its deliveries failed for 3 days, and dropped what was
            queued. Once the endpoint works again, turn it back on.
          </>
        ) : (
          'This webhook is turned off, so it gets no events. Turn it on to receive new ones.'
        )}
      </Notice>
      <div>
        <Button type="button" disabled={busy} onClick={onTurnOn}>
          Turn on
        </Button>
      </div>
    </div>
  );
}

function Scope({
  label,
  items,
  all,
}: {
  label: string;
  items: string[] | undefined;
  all: string;
}) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>
        {items === undefined ? (
          all
        ) : items.length === 0 ? (
          'None left: the sites it listed were deleted.'
        ) : (
          <span className="webhook-scope">
            {items.map((item) => (
              <code key={item}>{item}</code>
            ))}
          </span>
        )}
      </dd>
    </div>
  );
}

/**
 * `/webhooks/:webhookId`: one webhook, what can be done to it and its recent
 * deliveries. The title never carries the URL, which can hold the receiver's
 * secret; notices name the webhook by `webhookName`.
 */
export function WebhookPage({
  api,
  webhookId,
}: {
  api: ApiClient;
  webhookId: string;
}) {
  useTitle('Webhook');
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [open, setOpen] = useState<'none' | 'edit' | 'delete'>('none');
  const [busy, setBusy] = useState(false);
  const [redelivering, setRedelivering] = useState<string | null>(null);
  const [message, setMessage] = useState<Message | null>(null);
  const [attempt, setAttempt] = useState(0);
  // Bumped by every change, so an older read cannot undo it.
  const generation = useRef(0);

  const path = { params: { path: { webhookId } } };

  const read = useCallback(async () => {
    const [webhook, deliveries] = await Promise.all([
      api.GET('/api/v0/webhooks/{webhookId}', {
        params: { path: { webhookId } },
      }),
      api
        .GET('/api/v0/webhooks/{webhookId}/deliveries', {
          params: { path: { webhookId } },
        })
        .catch(() => null),
    ]);
    if (webhook.response.status === 404) return { kind: 'gone' } as const;
    if (!webhook.data) return { kind: 'error' } as const;
    return {
      kind: 'ready',
      webhook: webhook.data,
      deliveries: deliveries?.data?.items ?? null,
    } as const;
  }, [api, webhookId]);

  useEffect(() => {
    let live = true;
    read().then(
      (next) => live && setState(next),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [read, attempt]);

  const refresh = useCallback(async () => {
    const started = generation.current;
    const next = await read().catch(() => null);
    if (next?.kind === 'ready' && started === generation.current)
      setState(next);
  }, [read]);

  // Results arrive on their own while something is on its way.
  const pending =
    state.kind === 'ready' &&
    state.webhook.enabled &&
    state.deliveries?.some((delivery) => delivery.status === 'pending');
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => {
      if (!document.hidden) void refresh();
    }, refreshInterval);
    return () => clearInterval(timer);
  }, [pending, refresh]);

  const setWebhook = (webhook: Webhook) => {
    generation.current += 1;
    setState((current) =>
      current.kind === 'ready' ? { ...current, webhook } : current,
    );
  };

  const addDelivery = (delivery: WebhookDelivery) => {
    generation.current += 1;
    setState((current) =>
      current.kind === 'ready'
        ? {
            ...current,
            deliveries: [delivery, ...(current.deliveries ?? [])],
          }
        : current,
    );
  };

  /** Shows why a call on this webhook was refused. */
  const refused = (response: Response | undefined) => {
    if (response?.status === 404) return setState({ kind: 'gone' });
    if (response?.status === 409) {
      setMessage({
        tone: 'danger',
        text: 'This webhook is turned off, so nothing was sent. Turn it on first.',
      });
      void refresh();
      return;
    }
    setMessage({
      tone: 'danger',
      text: response ? failure(response) : unreachable,
    });
  };

  const setEnabled = async (enabled: boolean) => {
    setBusy(true);
    setMessage(null);
    const result = await api
      .PATCH('/api/v0/webhooks/{webhookId}', {
        ...path,
        body: { enabled },
      })
      .catch(() => null);
    setBusy(false);
    if (!result?.data) return refused(result?.response);
    setWebhook(result.data);
    setMessage({
      tone: 'info',
      text: enabled
        ? 'Turned on. New events will be sent.'
        : 'Turned off. Anything still queued will be dropped.',
    });
    void refresh();
  };

  const sendTest = async () => {
    setBusy(true);
    setMessage(null);
    const result = await api
      .POST('/api/v0/webhooks/{webhookId}/test', path)
      .catch(() => null);
    setBusy(false);
    if (!result?.data) return refused(result?.response);
    addDelivery(result.data);
    setMessage({ tone: 'info', text: 'Test queued. It is listed below.' });
  };

  const redeliver = async (delivery: WebhookDelivery) => {
    setRedelivering(delivery.id);
    setMessage(null);
    const result = await api
      .POST('/api/v0/webhooks/{webhookId}/deliveries/{deliveryId}/redeliver', {
        params: { path: { webhookId, deliveryId: delivery.id } },
      })
      .catch(() => null);
    setRedelivering(null);
    if (!result?.data) return refused(result?.response);
    addDelivery(result.data);
    setMessage({ tone: 'info', text: 'Queued to be sent again.' });
  };

  if (state.kind === 'loading') return <p role="status">Loading webhook…</p>;
  const back = (
    <p className="webhook-back">
      <a href="/webhooks">All webhooks</a>
    </p>
  );
  if (state.kind === 'gone')
    return (
      <>
        {back}
        <EmptyState title="Webhook not found">
          <p>It may have been deleted.</p>
        </EmptyState>
      </>
    );
  if (state.kind === 'error')
    return (
      <>
        {back}
        <div className="webhook-error">
          <Notice tone="danger">
            Could not load this webhook. Check your connection, then try again.
          </Notice>
          <Button
            type="button"
            onClick={() => {
              setState({ kind: 'loading' });
              setAttempt((n) => n + 1);
            }}
          >
            Try again
          </Button>
        </div>
      </>
    );

  const { webhook, deliveries } = state;
  const status = webhookStatus(webhook);
  return (
    <>
      {back}
      <PageHeader title={webhookName(webhook)} />
      <div className="webhook-status">
        <StatusBadge tone={status.tone}>{status.word}</StatusBadge>
      </div>
      <Trouble
        webhook={webhook}
        busy={busy}
        onTurnOn={() => void setEnabled(true)}
      />
      {message && <Notice tone={message.tone}>{message.text}</Notice>}
      <Card title="Details">
        <dl className="webhook-facts">
          <div>
            <dt>Endpoint URL</dt>
            <dd>
              <code className="webhook-url">{webhook.url}</code>
            </dd>
          </div>
          <Scope label="Events" items={webhook.events} all="All events" />
          <Scope label="Sites" items={webhook.sites} all="All sites" />
          <div>
            <dt>Created</dt>
            <dd>
              <Time at={webhook.createdAt} />
            </dd>
          </div>
        </dl>
        <div className="webhook-actions">
          <Button
            type="button"
            disabled={busy || !webhook.enabled}
            onClick={() => void sendTest()}
          >
            Send test
          </Button>
          <Button
            type="button"
            onClick={() => {
              setMessage(null);
              setOpen('edit');
            }}
          >
            Edit
          </Button>
          {webhook.enabled && (
            <Button
              type="button"
              disabled={busy}
              onClick={() => void setEnabled(false)}
            >
              Turn off
            </Button>
          )}
          <Button
            type="button"
            variant="danger"
            onClick={() => {
              setMessage(null);
              setOpen('delete');
            }}
          >
            Delete
          </Button>
        </div>
      </Card>
      <Card title="Recent deliveries">
        <div className="webhook-intro">
          <p>
            The last 50, with what the latest attempt got back. Failed
            deliveries retry for 24 hours.
          </p>
          <Button type="button" onClick={() => void refresh()}>
            Refresh
          </Button>
        </div>
        {deliveries === null ? (
          <Notice tone="danger">
            Could not load the deliveries. Use Refresh to try again.
          </Notice>
        ) : (
          <DeliveriesTable
            deliveries={deliveries}
            canRedeliver={webhook.enabled}
            redelivering={redelivering}
            onRedeliver={(delivery) => void redeliver(delivery)}
          />
        )}
      </Card>

      {open === 'edit' && (
        <WebhookFormDialog
          api={api}
          webhook={webhook}
          onClose={() => setOpen('none')}
          onSaved={(saved) => {
            setWebhook(saved);
            setOpen('none');
            setMessage({ tone: 'info', text: 'Changes saved.' });
          }}
        />
      )}
      {open === 'delete' && (
        <DeleteWebhookDialog
          api={api}
          webhook={webhook}
          onClose={() => setOpen('none')}
          onDeleted={() => navigate('/webhooks')}
        />
      )}
    </>
  );
}
