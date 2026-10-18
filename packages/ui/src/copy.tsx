// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, useState } from 'react';

type CopyState = 'idle' | 'copied' | 'failed';

/**
 * A labelled, read-only value with a Copy button. Copying says so under the
 * field, which screen readers announce; when the clipboard is unavailable the value
 * is selected so it can be copied from the keyboard.
 */
export function CopyField({
  label,
  value,
}: {
  /** Visible label for the value. */
  label: string;
  value: string;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [state, setState] = useState<CopyState>('idle');

  useEffect(() => {
    if (state !== 'copied') return;
    const timer = setTimeout(() => setState('idle'), 2000);
    return () => clearTimeout(timer);
  }, [state]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setState('copied');
    } catch {
      input.current?.select();
      setState('failed');
    }
  };

  return (
    <div className="wl-field">
      <label className="wl-field-label" htmlFor={id}>
        {label}
      </label>
      <div className="wl-copy">
        <input
          ref={input}
          id={id}
          className="wl-input wl-copy-value"
          readOnly
          value={value}
          onFocus={(event) => event.currentTarget.select()}
        />
        <button
          type="button"
          className="wl-button wl-button-secondary"
          aria-label={`Copy ${label.toLowerCase()}`}
          onClick={copy}
        >
          Copy
        </button>
      </div>
      <p className="wl-field-hint wl-copy-status" role="status">
        {state === 'copied' ? `${label} copied.` : null}
        {state === 'failed'
          ? 'Could not copy automatically. The text is selected; copy it with your keyboard.'
          : null}
      </p>
    </div>
  );
}
