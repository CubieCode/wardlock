// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from 'react';
import { cx } from './cx.js';

interface FieldText {
  /** Visible label; always rendered, never a placeholder substitute. */
  label: string;
  /** Guidance shown under the label, before the control. */
  hint?: ReactNode;
  /** What is wrong and how to fix it. Marks the control invalid. */
  error?: ReactNode;
}

export interface TextFieldProps
  extends
    Omit<InputHTMLAttributes<HTMLInputElement>, 'id' | 'children'>,
    FieldText {}

export interface SelectFieldProps
  extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'id'>, FieldText {
  /** The `<option>` elements. */
  children: ReactNode;
}

/**
 * The label, hint and error around one control. The hint and error are
 * linked to the control with aria-describedby, and the error is announced.
 */
function Field({
  label,
  hint,
  error,
  describedBy: describedByProp,
  invalid,
  control,
}: FieldText & {
  describedBy: string | undefined;
  invalid: boolean | 'true' | 'false' | 'grammar' | 'spelling' | undefined;
  control: (attrs: {
    id: string;
    'aria-invalid':
      boolean | 'true' | 'false' | 'grammar' | 'spelling' | undefined;
    'aria-describedby': string | undefined;
  }) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  // Our hint and error always describe the control; a caller's ids are added.
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
      {control({
        id,
        'aria-invalid': error ? true : invalid,
        'aria-describedby': describedBy,
      })}
      {error ? (
        <p className="wl-field-error" id={errorId} role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}

/** A labelled text input with optional hint and error. */
export function TextField({
  label,
  hint,
  error,
  className,
  type = 'text',
  'aria-describedby': describedBy,
  'aria-invalid': invalid,
  ...props
}: TextFieldProps) {
  return (
    <Field
      label={label}
      hint={hint}
      error={error}
      describedBy={describedBy}
      invalid={invalid}
      control={(attrs) => (
        <input
          {...props}
          {...attrs}
          type={type}
          className={cx('wl-input', className)}
        />
      )}
    />
  );
}

/** A labelled native select with optional hint and error. */
export function SelectField({
  label,
  hint,
  error,
  className,
  children,
  'aria-describedby': describedBy,
  'aria-invalid': invalid,
  ...props
}: SelectFieldProps) {
  return (
    <Field
      label={label}
      hint={hint}
      error={error}
      describedBy={describedBy}
      invalid={invalid}
      control={(attrs) => (
        <select
          {...props}
          {...attrs}
          className={cx('wl-input', 'wl-select', className)}
        >
          {children}
        </select>
      )}
    />
  );
}
