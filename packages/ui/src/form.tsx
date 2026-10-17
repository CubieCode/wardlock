// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useId, type InputHTMLAttributes, type ReactNode } from 'react';
import { cx } from './cx.js';

export interface TextFieldProps extends Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'id' | 'children'
> {
  /** Visible label; always rendered, never a placeholder substitute. */
  label: string;
  /** Guidance shown under the label, before the input. */
  hint?: ReactNode;
  /** What is wrong and how to fix it. Marks the input invalid. */
  error?: ReactNode;
}

/**
 * A labelled text input with optional hint and error. The hint and error are
 * linked to the input with aria-describedby, and the error is announced.
 */
export function TextField({
  label,
  hint,
  error,
  className,
  type = 'text',
  'aria-describedby': describedByProp,
  'aria-invalid': invalidProp,
  ...props
}: TextFieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  // Our hint and error always describe the input; a caller's ids are added.
  const describedBy =
    [describedByProp, hint ? hintId : null, error ? errorId : null]
      .filter(Boolean)
      .join(' ') || undefined;
  return (
    <div className={cx('wl-field', error ? 'wl-field-invalid' : null)}>
      <label className="wl-field-label" htmlFor={id}>
        {label}
      </label>
      {hint ? (
        <p className="wl-field-hint" id={hintId}>
          {hint}
        </p>
      ) : null}
      <input
        {...props}
        id={id}
        type={type}
        className={cx('wl-input', className)}
        aria-invalid={error ? true : invalidProp}
        aria-describedby={describedBy}
      />
      {error ? (
        <p className="wl-field-error" id={errorId} role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
