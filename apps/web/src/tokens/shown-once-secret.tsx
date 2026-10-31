// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Button, CopyField, Dialog, Notice } from '@wardlock/ui';
import type { ReactNode } from 'react';

/**
 * Shows a secret the server returns only once, in full, with a Copy button.
 * The value lives only in the caller's state while this dialog is open: on
 * `onDone`, or on leaving the page, it is gone. Only the Done button closes
 * it, so a stray Escape cannot lose the secret before it is copied.
 */
export function ShownOnceSecretDialog({
  title,
  label,
  noun,
  value,
  onDone,
  children,
}: {
  title: string;
  /** The field's label, such as "API token". */
  label: string;
  /** What the secret is called in a sentence, such as "token". */
  noun: string;
  value: string;
  onDone: () => void;
  /** What it is, by the details that stay visible afterwards. */
  children?: ReactNode;
}) {
  return (
    <Dialog
      open
      title={title}
      onClose={() => {}}
      actions={
        <Button type="button" variant="primary" onClick={onDone}>
          Done
        </Button>
      }
    >
      <Notice tone="warning">
        This is the only time Wardlock shows this {noun}. Copy it now: it cannot
        be shown again.
      </Notice>
      <CopyField label={label} value={value} />
      {children}
    </Dialog>
  );
}
