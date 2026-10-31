// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { webhookName, type Webhook } from './format.js';

/** Confirms deleting a webhook, naming it and what goes with it. */
export function DeleteWebhookDialog({
  api,
  webhook,
  onClose,
  onDeleted,
}: {
  api: ApiClient;
  webhook: Webhook;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const remove = async () => {
    setBusy(true);
    setProblem(null);
    const result = await api
      .DELETE('/api/v0/webhooks/{webhookId}', {
        params: { path: { webhookId: webhook.id } },
      })
      .catch(() => null);
    setBusy(false);
    // 404: already gone, which is what was asked for.
    if (result?.response.ok || result?.response.status === 404)
      return onDeleted();
    setProblem(result ? failure(result.response) : unreachable);
  };

  return (
    <Dialog
      open
      title={`Delete “${webhookName(webhook)}”?`}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="danger"
            disabled={busy}
            onClick={() => void remove()}
          >
            {busy ? 'Deleting…' : 'Delete webhook'}
          </Button>
        </>
      }
    >
      <p>
        Wardlock stops sending to this endpoint at once. Anything queued and its
        delivery history go too. This cannot be undone.
      </p>
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Dialog>
  );
}
