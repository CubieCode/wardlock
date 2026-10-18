// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { Button, CopyField, Dialog, Notice } from '@wardlock/ui';
import {
  expiryText,
  scopeText,
  usesText,
  type CreatedShareLink,
} from './format.js';

/**
 * Shows a new or rotated link's URL, the only time the dashboard ever has
 * it, with a Copy button. The URL lives only in the caller's state for as
 * long as this dialog is open; closing it forgets the URL.
 */
export function RevealDialog({
  created,
  rotated = false,
  onClose,
}: {
  created: CreatedShareLink;
  rotated?: boolean;
  onClose: () => void;
}) {
  const link = created.shareLink;
  return (
    <Dialog
      open
      title={rotated ? 'Copy the new link' : 'Copy your share link'}
      onClose={onClose}
      actions={
        <Button type="button" variant="primary" onClick={onClose}>
          Done
        </Button>
      }
    >
      <Notice tone="warning">
        This is the only time Wardlock shows this link. Copy it now; afterwards
        you will see only its label and ID <code>{link.publicId}</code>.
      </Notice>
      <CopyField label="Share link" value={created.url} />
      <p className="link-grants">
        {scopeText(link.path)} · {expiryText(link)} · {usesText(link)}
      </p>
      {rotated && (
        <p>The previous link has stopped working. Send this one instead.</p>
      )}
    </Dialog>
  );
}
