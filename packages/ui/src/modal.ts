// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useEffect,
  useRef,
  type ReactEventHandler,
  type RefObject,
  type SyntheticEvent,
} from 'react';

function show(dialog: HTMLDialogElement) {
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
}

/**
 * Keeps a native modal `<dialog>` in step with a caller-owned `open` prop:
 * shown modally while open, closed otherwise, with focus returned to whatever
 * had it before. Escape asks the caller to close (`onClose`). If the browser
 * closes the dialog itself (a second Escape in Chromium cannot be cancelled),
 * it calls `onClose` and shows the dialog again when the caller keeps `open`,
 * so the element never drifts from the prop. Spread the returned handlers on
 * the `<dialog>`.
 */
export function useModal(
  ref: RefObject<HTMLDialogElement | null>,
  open: boolean,
  onClose: () => void,
): {
  onCancel: ReactEventHandler<HTMLDialogElement>;
  onClose: ReactEventHandler<HTMLDialogElement>;
} {
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
    // Browsers focus the first control themselves; not every environment does.
    if (!dialog.contains(document.activeElement))
      dialog
        .querySelector<HTMLElement>(
          'input, select, textarea, button, a[href], [tabindex]',
        )
        ?.focus();
    return () => {
      if (typeof dialog.close === 'function') dialog.close();
      else dialog.removeAttribute('open');
      if (opener?.isConnected) opener.focus();
    };
  }, [ref, open]);

  return {
    onCancel: (event: SyntheticEvent<HTMLDialogElement>) => {
      // Keep the element in step with `open`: the caller decides.
      event.preventDefault();
      onClose();
    },
    onClose: (event: SyntheticEvent<HTMLDialogElement>) => {
      // Closed by the browser while the caller still has it open.
      if (!openRef.current) return;
      const dialog = event.currentTarget;
      onClose();
      // A caller that ignores the request (an action in flight) keeps
      // `open`; show the dialog again once React has had its say.
      setTimeout(() => {
        if (openRef.current && !dialog.open) show(dialog);
      });
    },
  };
}
