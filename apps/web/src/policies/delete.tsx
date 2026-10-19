// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice } from '@wardlock/ui';
import { useState } from 'react';
import type { Policy, Site } from '../sites/format.js';
import { fallback, policyName } from './format.js';

/** Confirms deleting a path policy, naming what its paths fall back to. */
export function DeletePolicyDialog({
  api,
  site,
  policy,
  policies,
  onClose,
  onDeleted,
}: {
  api: ApiClient;
  site: Site;
  policy: Policy;
  policies: Policy[];
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const next = fallback(policies, policy);
  const nextAccess = next?.visibility === 'public' ? 'public' : 'protected';

  const remove = async () => {
    setBusy(true);
    setError(null);
    const result = await api
      .DELETE('/api/v0/sites/{site}/policies/{policyId}', {
        params: { path: { site: site.name, policyId: policy.id } },
      })
      .catch(() => null);
    setBusy(false);
    // Already gone is what the owner asked for.
    if (result?.response.ok || result?.response.status === 404)
      return onDeleted();
    setError(
      result?.error?.message
        ? `Could not delete the policy: ${result.error.message}`
        : 'Could not delete the policy. Check your connection, then try again.',
    );
  };

  return (
    <Dialog
      open
      title={`Delete the policy for ${policy.path}?`}
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
            {busy ? 'Deleting…' : 'Delete policy'}
          </Button>
        </>
      }
    >
      <p>
        <code>{policy.path}</code> and everything under it will follow{' '}
        {next ? (
          <>
            {next.path === '/' ? (
              policyName('/')
            ) : (
              <>
                the policy for <code>{next.path}</code>
              </>
            )}{' '}
            instead, so it becomes {nextAccess}
          </>
        ) : (
          'the site default instead'
        )}
        . Visitors who got in under this policy are signed out.
      </p>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}
