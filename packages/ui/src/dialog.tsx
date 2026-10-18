// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, type ReactNode } from 'react';

function show(dialog: HTMLDialogElement) {
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
}

/**
 * A modal dialog on the native `<dialog>` element: the page behind it is
 * inert, focus moves to the first control inside it, and Escape asks to
 * close. The caller owns `open`; closing (Escape or a Cancel button) calls
 * `onClose`, and focus returns to whatever had it before the dialog opened.
 * If the browser closes the dialog itself (a second Escape in Chromium cannot
 * be cancelled), it calls `onClose` and shows the dialog again when the
 * caller keeps `open`, so the element never drifts from the prop.
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
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog || !open) return;
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    show(dialog);
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
      onClose={(event) => {
        // Closed by the browser while the caller still has it open.
        if (!openRef.current) return;
        const dialog = event.currentTarget;
        onClose();
        // A caller that ignores the request (an action in flight) keeps
        // `open`; show the dialog again once React has had its say.
        setTimeout(() => {
          if (openRef.current && !dialog.open) show(dialog);
        });
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
