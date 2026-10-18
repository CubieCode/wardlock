// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import { Button, Dialog, Notice, SelectField, TextField } from '@wardlock/ui';
import { useId, useState, type FormEvent } from 'react';
import type { Site } from '../sites/format.js';
import type { CreatedShareLink } from './format.js';

type Request = components['schemas']['CreateShareLinkRequest'];
const fields = ['label', 'path', 'expiresAt', 'maxUses'] as const;
type Field = (typeof fields)[number];

const hour = 3600_000;
const expiries = {
  never: { text: 'Never', after: undefined },
  '1h': { text: 'In 1 hour', after: hour },
  '1d': { text: 'In 1 day', after: 24 * hour },
  '7d': { text: 'In 7 days', after: 7 * 24 * hour },
  '30d': { text: 'In 30 days', after: 30 * 24 * hour },
  custom: { text: 'On a date you choose', after: undefined },
} as const;
type Expiry = keyof typeof expiries;
type Uses = 'unlimited' | 'once' | 'limited';

const maxLabel = 200;
const maxUses = 1_000_000;

/** `YYYY-MM-DDTHH:mm` in local time, the value of a datetime-local input. */
function localInput(at: number) {
  const d = new Date(at - new Date(at).getTimezoneOffset() * 60_000);
  return d.toISOString().slice(0, 16);
}

/**
 * Asks for a label, scope, expiry and use limit, then creates the link and
 * hands the one-time response to `onCreated`.
 */
export function CreateLinkDialog({
  api,
  site,
  onClose,
  onCreated,
}: {
  api: ApiClient;
  site: Site;
  onClose: () => void;
  onCreated: (created: CreatedShareLink) => void;
}) {
  const formId = useId();
  const [label, setLabel] = useState('');
  const [path, setPath] = useState('/');
  const [expiry, setExpiry] = useState<Expiry>('never');
  // When the dialog opened: the earliest custom expiry it offers.
  const [openedAt] = useState(() => Date.now());
  const [date, setDate] = useState(() =>
    localInput(Date.now() + 7 * 24 * hour),
  );
  const [uses, setUses] = useState<Uses>('unlimited');
  const [count, setCount] = useState('5');
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const close = () => {
    if (!busy) onClose();
  };

  /** The request, or the field errors that stop it. */
  const build = ():
    | { body: Request }
    | { body?: never; errors: Partial<Record<Field, string>> } => {
    const found: Partial<Record<Field, string>> = {};
    const body: Request = {};
    const trimmed = label.trim();
    if (trimmed.length > maxLabel)
      found.label = `Use at most ${maxLabel} characters.`;
    else if (trimmed) body.label = trimmed;
    const scope = path.trim() || '/';
    if (!scope.startsWith('/'))
      found.path =
        'Start with a slash: / for the whole site, or a folder such as /docs/.';
    else body.path = scope;
    if (expiry === 'custom') {
      const at = new Date(date).getTime();
      if (!date || Number.isNaN(at))
        found.expiresAt = 'Choose a date and time.';
      else if (at <= Date.now())
        found.expiresAt = 'Choose a time in the future.';
      else body.expiresAt = at;
    } else {
      const after = expiries[expiry].after;
      if (after !== undefined) body.expiresAt = Date.now() + after;
    }
    if (uses === 'once') body.maxUses = 1;
    if (uses === 'limited') {
      const n = Number(count);
      if (!Number.isInteger(n) || n < 1 || n > maxUses)
        found.maxUses = `Enter a whole number from 1 to ${maxUses.toLocaleString('en-NZ')}.`;
      else body.maxUses = n;
    }
    return Object.keys(found).length > 0 ? { errors: found } : { body };
  };

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const built = build();
    setErrors(built.body ? {} : built.errors);
    setFailure(null);
    if (!built.body) return;
    setBusy(true);
    const result = await api
      .POST('/api/v0/sites/{site}/links', {
        params: { path: { site: site.name } },
        body: built.body,
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) return onCreated(result.data);
    const error = result?.error;
    const target = fields.find((field) => field === error?.target);
    if (error?.code === 'invalid_share_link' && target)
      return setErrors({ [target]: error.message });
    setFailure(
      error?.message
        ? `Could not create the link: ${error.message}`
        : 'Could not create the link. Check your connection, then try again.',
    );
  };

  return (
    <Dialog
      open
      title={`Create a share link for ${site.name}`}
      onClose={close}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy}>
            {busy ? 'Creating…' : 'Create link'}
          </Button>
        </>
      }
    >
      <p>
        Anyone with the link can open what it covers, without signing in, until
        it expires, runs out of uses or you revoke it.
      </p>
      <form id={formId} className="link-form" onSubmit={create} noValidate>
        <TextField
          label="Label"
          hint="Who or what it is for, such as “Linear ENG-123”. Only you see it."
          maxLength={maxLabel}
          autoComplete="off"
          value={label}
          error={errors.label}
          onChange={(event) => setLabel(event.target.value)}
        />
        <TextField
          label="Opens"
          hint="/ for the whole site, or a folder such as /docs/."
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          className="link-mono"
          value={path}
          error={errors.path}
          onChange={(event) => setPath(event.target.value)}
        />
        <SelectField
          label="Expires"
          value={expiry}
          error={expiry === 'custom' ? undefined : errors.expiresAt}
          onChange={(event) => setExpiry(event.target.value as Expiry)}
        >
          {Object.entries(expiries).map(([value, { text }]) => (
            <option key={value} value={value}>
              {text}
            </option>
          ))}
        </SelectField>
        {expiry === 'custom' && (
          <TextField
            label="Expiry date and time"
            type="datetime-local"
            value={date}
            min={localInput(openedAt)}
            error={errors.expiresAt}
            onChange={(event) => setDate(event.target.value)}
          />
        )}
        <SelectField
          label="Uses"
          hint="Limited links show a “Click to view” button, so link previews do not spend a use."
          value={uses}
          error={uses === 'limited' ? undefined : errors.maxUses}
          onChange={(event) => setUses(event.target.value as Uses)}
        >
          <option value="unlimited">Unlimited</option>
          <option value="once">One time</option>
          <option value="limited">A set number of times</option>
        </SelectField>
        {uses === 'limited' && (
          <TextField
            label="Number of uses"
            type="number"
            inputMode="numeric"
            min={1}
            max={maxUses}
            value={count}
            error={errors.maxUses}
            onChange={(event) => setCount(event.target.value)}
          />
        )}
      </form>
      {failure && <Notice tone="danger">{failure}</Notice>}
    </Dialog>
  );
}
