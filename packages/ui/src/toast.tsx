// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState } from 'react';
import { cx } from './cx.js';

export type ToastTone = 'info' | 'danger';

/** Apple platforms undo with ⌘Z; everything else with Ctrl+Z. */
const apple = () =>
  typeof navigator !== 'undefined' &&
  /Mac|iPhone|iPad/.test(navigator.userAgent);

const editable = (target: EventTarget | null) =>
  target instanceof HTMLElement &&
  (target.isContentEditable ||
    ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));

/** Whether a key press is this platform's undo, outside a text field. */
function isUndo(event: KeyboardEvent) {
  if (event.key.toLowerCase() !== 'z' || event.shiftKey || event.altKey)
    return false;
  if (editable(event.target)) return false;
  return apple()
    ? event.metaKey && !event.ctrlKey
    : event.ctrlKey && !event.metaKey;
}

function ToastCard({
  message,
  tone,
  onUndo,
  onDismiss,
  duration,
}: {
  message: string;
  tone: ToastTone;
  onUndo: (() => void) | undefined;
  onDismiss: () => void;
  duration: number;
}) {
  const [hover, setHover] = useState(false);
  const [focus, setFocus] = useState(false);
  const held = hover || focus;
  const actions = useRef({ onUndo, onDismiss });
  useEffect(() => {
    actions.current = { onUndo, onDismiss };
  }, [onUndo, onDismiss]);
  useEffect(() => {
    if (held || tone === 'danger') return;
    const timer = setTimeout(() => actions.current.onDismiss(), duration);
    return () => clearTimeout(timer);
  }, [held, duration, tone]);
  const undoable = onUndo !== undefined;
  useEffect(() => {
    if (!undoable) return;
    const onKey = (event: KeyboardEvent) => {
      if (!isUndo(event)) return;
      event.preventDefault();
      actions.current.onUndo?.();
      actions.current.onDismiss();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [undoable]);
  const shortcut = apple() ? '⌘Z' : 'Ctrl Z';

  return (
    <div
      className={cx('wl-toast', tone === 'danger' ? 'wl-toast-danger' : null)}
      onPointerEnter={() => setHover(true)}
      onPointerLeave={() => setHover(false)}
      onFocus={() => setFocus(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null))
          setFocus(false);
      }}
    >
      <span className="wl-toast-message">
        {message}
        {undoable ? (
          <span className="wl-visually-hidden">
            {' '}
            {apple() ? 'Command Z' : 'Control Z'} undoes it.
          </span>
        ) : null}
      </span>
      {onUndo ? (
        <button
          type="button"
          className="wl-toast-undo"
          aria-keyshortcuts={apple() ? 'Meta+Z' : 'Control+Z'}
          onClick={() => {
            onUndo();
            onDismiss();
          }}
        >
          Undo
          <kbd className="wl-kbd wl-toast-key" aria-hidden="true">
            {shortcut}
          </kbd>
        </button>
      ) : null}
      {tone === 'danger' ? (
        <button
          type="button"
          className="wl-toast-undo wl-toast-dismiss"
          onClick={onDismiss}
        >
          Dismiss
        </button>
      ) : null}
    </div>
  );
}

/**
 * A brief message at the foot of the screen ("Tagged 3 items"), with an
 * optional Undo. Both live regions, polite for `info` and assertive for
 * `danger`, are always mounted, so each new `message` is announced; `null`
 * shows nothing. An `info` toast dismisses itself after `duration` (5 s, or
 * 10 s with Undo), but waits while the pointer is over it or focus is inside
 * it; a `danger` toast (something failed) stays until Dismiss. With
 * `onUndo`, ⌘Z on Apple platforms or Ctrl+Z elsewhere also undoes, outside
 * text fields, and the toast says so. Undo calls `onUndo`, then `onDismiss`.
 * Give each message a new `toastKey` (a counter will do) so the same words
 * twice restart the timer.
 */
export function Toast({
  message,
  toastKey,
  tone = 'info',
  onUndo,
  onDismiss,
  duration,
}: {
  message: string | null;
  toastKey?: string | number;
  tone?: ToastTone;
  onUndo?: () => void;
  onDismiss: () => void;
  duration?: number;
}) {
  const card =
    message === null ? null : (
      <ToastCard
        key={toastKey ?? message}
        message={message}
        tone={tone}
        onUndo={onUndo}
        onDismiss={onDismiss}
        duration={duration ?? (onUndo ? 10_000 : 5000)}
      />
    );
  return (
    <div className="wl-toast-region">
      <div role="status">{tone === 'info' ? card : null}</div>
      <div role="alert">{tone === 'danger' ? card : null}</div>
    </div>
  );
}
