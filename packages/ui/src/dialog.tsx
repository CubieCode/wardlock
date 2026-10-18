// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, type ReactNode } from 'react';

/**
 * A modal dialog on the native `<dialog>` element: the page behind it is
 * inert, focus moves to the first control inside it, and Escape asks to
 * close. The caller owns `open`; closing (Escape or a Cancel button) calls
 * `onClose`, and focus returns to whatever had it before the dialog opened.
 */
export function Dialog({
  open,
  title,
  onClose,
  children,
  actions,
}: {
  open: boolean;
  /** The dialog's heading and accessible name; name the object it acts on. */
  title: string;
  /** Called on Escape. Ignore it while an action is in flight. */
  onClose: () => void;
  /** The consequences, and any field needed to confirm. */
  children: ReactNode;
  /** Buttons at the foot: Cancel first, then the action. */
  actions: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const bodyId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog || !open) return;
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    return () => {
      if (typeof dialog.close === 'function') dialog.close();
      else dialog.removeAttribute('open');
      if (opener?.isConnected) opener.focus();
    };
  }, [open]);

  return (
    <dialog
      ref={ref}
      className="wl-dialog"
      aria-labelledby={titleId}
      aria-describedby={bodyId}
      onCancel={(event) => {
        // Keep the element in step with `open`: the caller decides.
        event.preventDefault();
        onClose();
      }}
    >
      {open ? (
        <>
          <h2 id={titleId} className="wl-dialog-title">
            {title}
          </h2>
          <div id={bodyId} className="wl-dialog-body">
            {children}
          </div>
          <div className="wl-dialog-actions">{actions}</div>
        </>
      ) : null}
    </dialog>
  );
}
