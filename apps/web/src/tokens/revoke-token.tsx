// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import type { ApiToken } from './format.js';

/** Confirms revoking a token, naming it and what stops working. */
export function RevokeTokenDialog({
  api,
  token,
  onClose,
  onRevoked,
}: {
  api: ApiClient;
  token: ApiToken;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const revoke = async () => {
    setBusy(true);
    setProblem(null);
    const result = await api
      .DELETE('/api/v0/tokens/{tokenId}', {
        params: { path: { tokenId: token.id } },
      })
      .catch(() => null);
    setBusy(false);
    // 404: already gone, which is what was asked for.
    if (result?.response.ok || result?.response.status === 404)
      return onRevoked();
    setProblem(result ? failure(result.response) : unreachable);
  };

  return (
    <Dialog
      open
      title={`Revoke “${token.name}”?`}
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
            onClick={() => void revoke()}
          >
            {busy ? 'Revoking…' : 'Revoke token'}
          </Button>
        </>
      }
    >
      <p>
        Anything using <code>{token.publicId}</code>, such as a CI job or the
        CLI on another computer, is refused from its next request. This cannot
        be undone; create a new token to replace it.
      </p>
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Dialog>
  );
}
