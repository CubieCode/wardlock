// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import type { Site } from '../sites/format.js';
import type { Domain } from './format.js';

/**
 * Confirms removing a custom domain: serving stops at once, and the owner
 * is reminded to delete the DNS record, which would otherwise let whoever
 * later holds the site name claim the hostname.
 */
export function RemoveDomainDialog({
  api,
  site,
  domain,
  onClose,
  onRemoved,
}: {
  api: ApiClient;
  site: Site;
  domain: Domain;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const remove = async () => {
    setBusy(true);
    setError(null);
    const result = await api
      .DELETE('/api/v0/sites/{site}/domains/{hostname}', {
        params: { path: { site: site.name, hostname: domain.hostname } },
      })
      .catch(() => null);
    setBusy(false);
    // Already gone counts as removed: the outcome the owner asked for.
    if (result?.response.ok || result?.response.status === 404)
      return onRemoved();
    setError(
      result?.error?.message
        ? `Could not remove the domain: ${result.error.message}`
        : 'Could not remove the domain. Check your connection, then try again.',
    );
  };

  return (
    <Dialog
      open
      title={`Remove ${domain.hostname}?`}
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
            onClick={remove}
          >
            {busy ? 'Removing…' : 'Remove domain'}
          </Button>
        </>
      }
    >
      <p>
        Wardlock stops serving <strong>{site.name}</strong> at{' '}
        <code>{domain.hostname}</code> immediately and deletes its certificate.
        The site stays at <code>{site.hostname}</code>.
      </p>
      <Notice tone="warning">
        Then delete the CNAME record for <code>{domain.hostname}</code>, and the
        TXT record for <code>{domain.verificationRecord.name}</code> if you
        added it, at your DNS provider. A record left in place could let whoever
        later holds the site name {site.name} claim this address.
      </Notice>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}
