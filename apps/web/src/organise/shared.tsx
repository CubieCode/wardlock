// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Button, Dialog, Notice, TextAreaField, TextField } from '@wardlock/ui';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { failure, unreachable } from '../auth/messages.js';

/** What an apiclient call answers, as far as these screens need it. */
interface Answer<T> {
  data?: T;
  error?: { message: string; target?: string | undefined };
  response: Response;
}

/** A refusal in words, and the field it is about when the server says. */
export interface Refusal {
  error: string;
  target?: string | undefined;
}

/**
 * Runs one call and answers its data, or what went wrong in words: the
 * server's own message for a field it refused (a taken name, a name too
 * long), otherwise what to do next.
 */
export async function attempt<T>(
  call: () => Promise<Answer<T>>,
): Promise<{ data: T | undefined } | Refusal> {
  let answer: Answer<T>;
  try {
    answer = await call();
  } catch {
    return { error: unreachable };
  }
  const { data, error, response } = answer;
  if (response.ok) return { data };
  if (response.status === 404)
    return { error: 'It no longer exists. Reload the page to see the rest.' };
  if (error?.message && [400, 409, 413].includes(response.status))
    return { error: error.message, target: error.target };
  return { error: failure(response) };
}

/** How a delete ended: done now, or already gone (a `404`). */
export type Removal = 'deleted' | 'gone';

/**
 * Runs a delete. A `404` means the group or tag is already gone, which is
 * what the owner wanted, so it counts as done; anything else is an error.
 */
export async function remove(
  call: () => Promise<Answer<unknown>>,
): Promise<Removal | { error: string }> {
  let response: Response;
  try {
    ({ response } = await call());
  } catch {
    return { error: unreachable };
  }
  if (response.ok) return 'deleted';
  if (response.status === 404) return 'gone';
  return { error: failure(response) };
}

/** "1 site", "4 sites". */
export const sites = (n: number) => `${n} ${n === 1 ? 'site' : 'sites'}`;

/**
 * How many sites a group or tag holds, linking to the sites list filtered to
 * them. `context` names the group or tag for assistive technology, since
 * every row has such a link.
 */
export function SitesLink({
  href,
  count,
  context,
}: {
  href: string;
  count: number;
  context: string;
}) {
  return (
    <a
      className="organise-count"
      href={href}
      aria-label={`${sites(count)} ${context}`}
    >
      {sites(count)}
    </a>
  );
}

/**
 * A name field, and with `note` a note field under it, with Save and
 * Cancel, for creating or editing. `onSubmit` answers null when it worked,
 * or the refusal to show: under the note when the server names `note`,
 * otherwise under the name. Escape cancels.
 */
export function NameForm({
  label,
  hint,
  initial = '',
  note,
  submitLabel,
  busyLabel,
  onSubmit,
  onCancel,
}: {
  label: string;
  hint: string;
  initial?: string;
  note?: { label: string; hint: string; initial?: string | undefined };
  submitLabel: string;
  busyLabel: string;
  onSubmit: (name: string, note: string) => Promise<string | Refusal | null>;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial);
  const [noteText, setNoteText] = useState(note?.initial ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Refusal | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const onNote = note !== undefined && error?.target === 'note';

  return (
    <form
      ref={form}
      className="organise-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (busy) return;
        setBusy(true);
        setError(null);
        void onSubmit(name.trim(), noteText.trim()).then((problem) => {
          // On success the form is gone; only a refusal comes back here.
          if (problem === null) return;
          const refusal =
            typeof problem === 'string' ? { error: problem } : problem;
          setBusy(false);
          setError(refusal);
          // Back to the field to fix; Save was disabled meanwhile.
          form.current
            ?.querySelector<HTMLElement>(
              note !== undefined && refusal.target === 'note'
                ? 'textarea'
                : 'input',
            )
            ?.focus();
        });
      }}
      onKeyDown={(event) => {
        if (event.key !== 'Escape' || busy) return;
        event.stopPropagation();
        onCancel();
      }}
    >
      <TextField
        label={label}
        hint={hint}
        error={error && !onNote ? error.error : undefined}
        value={name}
        autoComplete="off"
        // The form appears because the owner asked for it.
        autoFocus
        onChange={(event) => setName(event.target.value)}
      />
      {note ? (
        <TextAreaField
          label={note.label}
          hint={note.hint}
          error={onNote ? error?.error : undefined}
          value={noteText}
          rows={2}
          maxLength={200}
          onChange={(event) => setNoteText(event.target.value)}
        />
      ) : null}
      <div className="organise-form-actions">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? busyLabel : submitLabel}
        </Button>
        <Button type="button" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

/**
 * Confirms deleting a group or tag, saying what happens to its sites. A
 * failure is reported inside the dialog.
 */
export function DeleteDialog({
  title,
  confirmLabel,
  recount,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  confirmLabel: string;
  /**
   * Reads the site counts again, so `children` names how many sites the
   * owner's sites hold now rather than when the page loaded. Delete waits
   * for it; if it fails, the count already shown stands.
   */
  recount: () => Promise<void>;
  /** Answers null when it worked, or the error to show. */
  onConfirm: () => Promise<string | null>;
  onClose: () => void;
  children: ReactNode;
}) {
  const [busy, setBusy] = useState(false);
  const [counting, setCounting] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // When the dialog opens, not on every render.
  const recountOnOpen = useRef(recount);
  useEffect(() => {
    let live = true;
    void recountOnOpen.current().finally(() => {
      if (live) setCounting(false);
    });
    return () => {
      live = false;
    };
  }, []);
  return (
    <Dialog
      open
      title={title}
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
            disabled={busy || counting}
            onClick={() => {
              setBusy(true);
              setError(null);
              void onConfirm().then((problem) => {
                if (problem === null) return;
                setBusy(false);
                setError(problem);
              });
            }}
          >
            {busy ? 'Deleting…' : confirmLabel}
          </Button>
        </>
      }
    >
      {counting ? <p role="status">Counting its sites…</p> : children}
      {error ? <Notice tone="danger">Could not delete: {error}</Notice> : null}
    </Dialog>
  );
}
