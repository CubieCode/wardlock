// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import {
  fileCount,
  formatBytes,
  shortId,
  Time,
  type Deploy,
  type Site,
} from './format.js';

/**
 * Confirms a rollback by showing the deploy that is live now beside the one
 * that will replace it, then reactivates `to`.
 */
export function RollbackDialog({
  api,
  site,
  live,
  to,
  onClose,
  onDone,
}: {
  api: ApiClient;
  site: Site;
  /** The live deploy, when it is in the loaded part of the history. */
  live: Deploy | undefined;
  to: Deploy;
  onClose: () => void;
  onDone: (deploy: Deploy) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const rollBack = async () => {
    setBusy(true);
    setError(null);
    const result = await api
      .POST('/api/v0/sites/{site}/rollback', {
        params: { path: { site: site.name } },
        body: { deployId: to.id },
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) return onDone(result.data.deploy);
    setError(
      result?.error?.message
        ? `Could not roll back: ${result.error.message}`
        : 'Could not roll back. Check your connection, then try again.',
    );
  };

  return (
    <Dialog
      open
      title={`Roll back ${site.name}?`}
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
            variant="primary"
            disabled={busy}
            onClick={rollBack}
          >
            {busy ? 'Rolling back…' : 'Roll back to this deploy'}
          </Button>
        </>
      }
    >
      <p>
        Visitors to <code>{site.hostname}</code> see the earlier deploy straight
        away. Nothing is uploaded, and the current deploy stays in the history
        so you can return to it the same way.
      </p>
      <dl className="rollback-swap">
        <div>
          <dt>Live now</dt>
          <dd>
            {live ? (
              <DeploySummary deploy={live} />
            ) : (
              <code>{shortId(site.activeDeployId ?? '')}</code>
            )}
          </dd>
        </div>
        <div>
          <dt>Goes live</dt>
          <dd>
            <DeploySummary deploy={to} />
          </dd>
        </div>
      </dl>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}

function DeploySummary({ deploy }: { deploy: Deploy }) {
  return (
    <>
      <span className="rollback-title">
        <code>{shortId(deploy.id)}</code> {deploy.message || 'No message'}
      </span>
      <span className="rollback-meta">
        Uploaded <Time at={deploy.createdAt} /> · {fileCount(deploy.fileCount)}{' '}
        · {formatBytes(deploy.totalSize)}
      </span>
    </>
  );
}
