// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Checkbox,
  Dialog,
  Fieldset,
  Notice,
  SelectField,
  TextField,
} from '@wardlock/ui';
import { useId, useState, type FormEvent } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { scopeChoices } from '../cli-login/cli-login.js';
import type { CreatedApiToken } from './format.js';

type Scope = components['schemas']['Scope'];
type Request = components['schemas']['CreateApiTokenRequest'];
const fields = ['name', 'scopes', 'expiresAt'] as const;
type Field = (typeof fields)[number];

const day = 24 * 3600_000;
const expiries = {
  '30d': { text: 'In 30 days', after: 30 * day },
  '90d': { text: 'In 90 days', after: 90 * day },
  '365d': { text: 'In 365 days', after: 365 * day },
  custom: { text: 'On a date you choose', after: undefined },
  never: { text: 'Never', after: undefined },
} as const;
type Expiry = keyof typeof expiries;

const maxName = 100;

/** `YYYY-MM-DD` in local time, the value of a date input. */
function localDate(at: number) {
  const d = new Date(at - new Date(at).getTimezoneOffset() * 60_000);
  return d.toISOString().slice(0, 10);
}

/**
 * Asks for a name, scopes and an expiry, then creates the token and hands
 * the one-time response to `onCreated`. Deploy starts ticked and Manage not,
 * as on the CLI login approval page: least privilege.
 */
export function CreateTokenDialog({
  api,
  onClose,
  onCreated,
}: {
  api: ApiClient;
  onClose: () => void;
  onCreated: (created: CreatedApiToken) => void;
}) {
  const formId = useId();
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<ReadonlySet<Scope>>(
    () => new Set<Scope>(['deploy']),
  );
  const [expiry, setExpiry] = useState<Expiry>('90d');
  // When the dialog opened: the earliest custom expiry it offers.
  const [openedAt] = useState(() => Date.now());
  const [date, setDate] = useState(() => localDate(Date.now() + 90 * day));
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({});
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const close = () => {
    if (!busy) onClose();
  };

  const toggle = (scope: Scope, on: boolean) => {
    const next = new Set(scopes);
    if (on) next.add(scope);
    else next.delete(scope);
    setScopes(next);
  };

  /** The request, or the field errors that stop it. */
  const build = ():
    | { body: Request }
    | { body?: never; errors: Partial<Record<Field, string>> } => {
    const found: Partial<Record<Field, string>> = {};
    const trimmed = name.trim();
    if (!trimmed) found.name = 'Enter a name, so you can tell it apart later.';
    else if (trimmed.length > maxName)
      found.name = `Use at most ${maxName} characters.`;
    const chosen = scopeChoices
      .map((choice) => choice.scope)
      .filter((scope) => scopes.has(scope));
    if (chosen.length === 0)
      found.scopes = 'Choose at least one thing this token may do.';
    const body: Request = { name: trimmed, scopes: chosen };
    if (expiry === 'custom') {
      // The token lasts to the end of the chosen day, in local time.
      const at = new Date(`${date}T23:59:59.999`).getTime();
      if (!date || Number.isNaN(at)) found.expiresAt = 'Choose a date.';
      else if (at <= Date.now())
        found.expiresAt = 'Choose today or a date in the future.';
      else body.expiresAt = at;
    } else {
      const after = expiries[expiry].after;
      if (after !== undefined) body.expiresAt = Date.now() + after;
    }
    return Object.keys(found).length > 0 ? { errors: found } : { body };
  };

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const built = build();
    setErrors(built.body ? {} : built.errors);
    setProblem(null);
    if (!built.body) return;
    setBusy(true);
    const result = await api
      .POST('/api/v0/tokens', { body: built.body })
      .catch(() => null);
    setBusy(false);
    if (result?.data) return onCreated(result.data);
    const target = fields.find((field) => field === result?.error?.target);
    if (target) return setErrors({ [target]: result?.error?.message });
    setProblem(result ? failure(result.response) : unreachable);
  };

  return (
    <Dialog
      open
      title="Create an API token"
      onClose={close}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy}>
            {busy ? 'Creating…' : 'Create token'}
          </Button>
        </>
      }
    >
      <form id={formId} className="token-form" onSubmit={create} noValidate>
        <TextField
          label="Name"
          hint="What will use it, such as “GitHub deploys”."
          maxLength={maxName}
          autoComplete="off"
          value={name}
          error={errors.name}
          onChange={(event) => setName(event.target.value)}
        />
        <Fieldset
          legend="What may this token do?"
          hint="Both can read your sites and deploys. Choose only what it needs."
          error={errors.scopes}
        >
          {scopeChoices.map((choice) => (
            <Checkbox
              key={choice.scope}
              label={choice.label}
              hint={choice.description}
              name="scopes"
              value={choice.scope}
              checked={scopes.has(choice.scope)}
              onChange={(event) => toggle(choice.scope, event.target.checked)}
            />
          ))}
        </Fieldset>
        <SelectField
          label="Expires"
          hint="An expiry cannot be changed later: create a new token instead."
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
            label="Expiry date"
            hint="The token stops working at the end of this day."
            type="date"
            value={date}
            min={localDate(openedAt)}
            error={errors.expiresAt}
            onChange={(event) => setDate(event.target.value)}
          />
        )}
      </form>
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Dialog>
  );
}
