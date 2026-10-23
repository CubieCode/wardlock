// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useRef, useState } from 'react';

function ToastCard({
  message,
  onUndo,
  onDismiss,
  duration,
}: {
  message: string;
  onUndo: (() => void) | undefined;
  onDismiss: () => void;
  duration: number;
}) {
  const [hover, setHover] = useState(false);
  const [focus, setFocus] = useState(false);
  const held = hover || focus;
  const dismiss = useRef(onDismiss);
  useEffect(() => {
    dismiss.current = onDismiss;
  }, [onDismiss]);
  useEffect(() => {
    if (held) return;
    const timer = setTimeout(() => dismiss.current(), duration);
    return () => clearTimeout(timer);
  }, [held, duration]);

  return (
    <div
      className="wl-toast"
      onPointerEnter={() => setHover(true)}
      onPointerLeave={() => setHover(false)}
      onFocus={() => setFocus(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null))
          setFocus(false);
      }}
    >
      <span className="wl-toast-message">{message}</span>
      {onUndo ? (
        <button
          type="button"
          className="wl-toast-undo"
          onClick={() => {
            onUndo();
            onDismiss();
          }}
        >
          Undo
        </button>
      ) : null}
    </div>
  );
}

/**
 * A brief confirmation at the foot of the screen ("Tagged 3 items"), with an
 * optional Undo. The polite live region is always mounted, so each new
 * `message` is announced; `null` shows nothing. The toast dismisses itself
 * after `duration` (5 s), but waits while the pointer is over it or focus is
 * inside it. Undo calls `onUndo`, then `onDismiss`. Give each change a new
 * `toastKey` (a counter will do) so the same words twice restart the timer.
 */
export function Toast({
  message,
  toastKey,
  onUndo,
  onDismiss,
  duration = 5000,
}: {
  message: string | null;
  toastKey?: string | number;
  onUndo?: () => void;
  onDismiss: () => void;
  duration?: number;
}) {
  return (
    <div role="status" className="wl-toast-region">
      {message === null ? null : (
        <ToastCard
          key={toastKey ?? message}
          message={message}
          onUndo={onUndo}
          onDismiss={onDismiss}
          duration={duration}
        />
      )}
    </div>
  );
}
