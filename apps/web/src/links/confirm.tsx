// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import type { Site } from '../sites/format.js';
import { linkName, type CreatedShareLink, type ShareLink } from './format.js';

/** Runs a confirmed action, reporting a failure inside the dialog. */
function useAction<T>(
  run: () => Promise<{
    data?: T;
    error?: { message: string };
    response: Response;
  } | null>,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const act = async (verb: string, onDone: (data: T | undefined) => void) => {
    setBusy(true);
    setError(null);
    const result = await run().catch(() => null);
    setBusy(false);
    if (result?.response.ok) return onDone(result.data);
    if (result?.response.status === 404)
      return setError(
        `Could not ${verb} the link: it no longer exists. Close this and refresh the list.`,
      );
    setError(
      result?.error?.message
        ? `Could not ${verb} the link: ${result.error.message}`
        : `Could not ${verb} the link. Check your connection, then try again.`,
    );
  };
  return { busy, error, act };
}

/** Confirms revoking a link, naming it and what stops working. */
export function RevokeLinkDialog({
  api,
  site,
  link,
  onClose,
  onRevoked,
}: {
  api: ApiClient;
  site: Site;
  link: ShareLink;
  onClose: () => void;
  onRevoked: () => void;
}) {
  const { busy, error, act } = useAction(() =>
    api.DELETE('/api/v0/sites/{site}/links/{linkId}', {
      params: { path: { site: site.name, linkId: link.publicId } },
    }),
  );
  return (
    <Dialog
      open
      title={`Revoke ${linkName(link)}?`}
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
            onClick={() => act('revoke', onRevoked)}
          >
            {busy ? 'Revoking…' : 'Revoke link'}
          </Button>
        </>
      }
    >
      <p>
        Anyone using this link loses access to <code>{site.hostname}</code>{' '}
        immediately, including visitors who already opened it. This cannot be
        undone; create a new link to share again.
      </p>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}

/** Confirms rotating a link, then hands over the new one-time response. */
export function RotateLinkDialog({
  api,
  site,
  link,
  onClose,
  onRotated,
}: {
  api: ApiClient;
  site: Site;
  link: ShareLink;
  onClose: () => void;
  onRotated: (created: CreatedShareLink) => void;
}) {
  const { busy, error, act } = useAction(() =>
    api.POST('/api/v0/sites/{site}/links/{linkId}/rotate', {
      params: { path: { site: site.name, linkId: link.publicId } },
    }),
  );
  return (
    <Dialog
      open
      title={`Rotate ${linkName(link)}?`}
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
            onClick={() =>
              act('rotate', (created) => created && onRotated(created))
            }
          >
            {busy ? 'Rotating…' : 'Rotate link'}
          </Button>
        </>
      }
    >
      <p>
        The current link stops working immediately, including for visitors who
        already opened it. You get a new link to send instead; its label, scope,
        expiry and uses stay the same.
      </p>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}
