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
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { useEffect, useState } from 'react';
import { navigate } from '../shell/router.js';
import { useTitle } from '../shell/page.js';
import { ShownOnceSecretDialog } from '../tokens/shown-once-secret.js';
import {
  eventsText,
  maxWebhooks,
  sitesText,
  webhookName,
  webhookStatus,
  type CreatedWebhook,
  type Webhook,
} from './format.js';
import { WebhookFormDialog } from './webhook-form.js';
import './webhooks.css';

type ListState =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; webhooks: Webhook[] };

type Open =
  | { kind: 'none' }
  | { kind: 'add' }
  // The one-time response: held only while its dialog is open.
  | { kind: 'reveal'; created: CreatedWebhook };

const columns: TableColumn<Webhook>[] = [
  {
    header: 'Endpoint',
    cell: (webhook) => (
      <span className="webhook-endpoint">
        <a href={`/webhooks/${webhook.id}`}>{webhookName(webhook)}</a>
        <code className="webhook-url">{webhook.url}</code>
      </span>
    ),
  },
  {
    header: 'Status',
    nowrap: true,
    cell: (webhook) => {
      const { word, tone } = webhookStatus(webhook);
      return <StatusBadge tone={tone}>{word}</StatusBadge>;
    },
  },
  { header: 'Events', nowrap: true, cell: eventsText },
  { header: 'Sites', nowrap: true, cell: sitesText },
];

/**
 * `/webhooks`: the owner's webhooks, oldest first, and adding one. The
 * signing secret shows once, then the new webhook's page offers a test.
 */
export function WebhooksPage({ api }: { api: ApiClient }) {
  useTitle('Webhooks');
  const [state, setState] = useState<ListState>({ kind: 'loading' });
  const [open, setOpen] = useState<Open>({ kind: 'none' });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    api.GET('/api/v0/webhooks').then(
      ({ data }) =>
        live &&
        setState(
          data ? { kind: 'ready', webhooks: data.items } : { kind: 'error' },
        ),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  const full = state.kind === 'ready' && state.webhooks.length >= maxWebhooks;

  let body;
  if (state.kind === 'loading') body = <p role="status">Loading webhooks…</p>;
  else if (state.kind === 'error')
    body = (
      <div className="webhook-error">
        <Notice tone="danger">
          Could not load your webhooks. Check your connection, then try again.
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
    );
  else if (state.webhooks.length === 0)
    body = (
      <EmptyState title="No webhooks yet">
        <p>
          A webhook tells a chat channel or your own service when a deploy goes
          live, a share link is used up or a site changes.
        </p>
      </EmptyState>
    );
  else
    body = (
      <Table
        caption="Webhooks, oldest first"
        columns={columns}
        rows={state.webhooks}
        rowKey={(webhook) => webhook.id}
      />
    );

  return (
    <>
      <PageHeader
        title="Webhooks"
        description="Send signed events to your own endpoints when something happens on your sites."
      />
      <Card title="Your webhooks">
        <div className="webhook-intro">
          <p>
            {full
              ? `You have the most webhooks allowed, ${maxWebhooks}. Delete one to add another.`
              : 'Each webhook has its own signing secret, so your endpoint can check a request came from Wardlock.'}
          </p>
          <Button
            type="button"
            variant="primary"
            disabled={state.kind !== 'ready' || full}
            onClick={() => setOpen({ kind: 'add' })}
          >
            Add webhook
          </Button>
        </div>
        {body}
      </Card>

      {open.kind === 'add' && (
        <WebhookFormDialog
          api={api}
          onClose={() => setOpen({ kind: 'none' })}
          onCreated={(created) => {
            setState((current) =>
              current.kind === 'ready'
                ? {
                    ...current,
                    webhooks: [...current.webhooks, created.webhook],
                  }
                : current,
            );
            setOpen({ kind: 'reveal', created });
          }}
        />
      )}
      {open.kind === 'reveal' && (
        <ShownOnceSecretDialog
          title="Copy your signing secret"
          label="Signing secret"
          noun="signing secret"
          value={open.created.secret}
          onDone={() => {
            const { id } = open.created.webhook;
            setOpen({ kind: 'none' });
            navigate(`/webhooks/${id}`);
          }}
        >
          <p className="webhook-details">
            For {webhookName(open.created.webhook)}. Your endpoint uses it to
            check the <code>Wardlock-Signature</code> header on every request.
          </p>
        </ShownOnceSecretDialog>
      )}
    </>
  );
}
