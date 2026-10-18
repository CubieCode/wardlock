// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice, TextField } from '@wardlock/ui';
import { useState, type FormEvent } from 'react';
import { navigate } from '../shell/router.js';
import { setFlash } from './flash.js';
import type { Site } from './format.js';

/**
 * Confirms a teardown by having the owner type the site's name, then deletes
 * the site and returns to the site list.
 */
export function TeardownDialog({
  api,
  site,
  open,
  onClose,
}: {
  api: ApiClient;
  site: Site;
  open: boolean;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = () => {
    if (busy) return;
    setTyped('');
    setError(null);
    onClose();
  };

  const tearDown = async (event: FormEvent) => {
    event.preventDefault();
    if (typed !== site.name) return;
    setBusy(true);
    setError(null);
    const result = await api
      .DELETE('/api/v0/sites/{site}', {
        params: { path: { site: site.name } },
      })
      .catch(() => null);
    setBusy(false);
    // 404: it is already gone, which is what was asked for.
    if (result && (result.response.ok || result.response.status === 404)) {
      setFlash(`${site.name} was torn down. Its name is free to use again.`);
      navigate('/sites');
      return;
    }
    setError(
      result?.error?.message
        ? `Could not tear down the site: ${result.error.message}`
        : 'Could not tear down the site. Check your connection, then try again.',
    );
  };

  const formId = `teardown-${site.id}`;
  return (
    <Dialog
      open={open}
      title={`Tear down ${site.name}?`}
      onClose={close}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button
            type="submit"
            form={formId}
            variant="danger"
            disabled={busy || typed !== site.name}
          >
            {busy ? 'Tearing down…' : 'Tear down site'}
          </Button>
        </>
      }
    >
      <Notice tone="warning">This cannot be undone.</Notice>
      <p>
        Wardlock stops serving <code>{site.hostname}</code> immediately and
        deletes every deploy and access setting of <strong>{site.name}</strong>.
        The name becomes free, and anyone on this server can claim it.
      </p>
      <form id={formId} onSubmit={tearDown} noValidate>
        <TextField
          label={`Type ${site.name} to confirm`}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
        />
      </form>
      {error && <Notice tone="danger">{error}</Notice>}
    </Dialog>
  );
}
