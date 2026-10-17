// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ButtonHTMLAttributes } from 'react';
import { cx } from './cx.js';

export type ButtonVariant = 'primary' | 'secondary';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Required so a button inside a form never submits by accident. */
  type: 'button' | 'submit' | 'reset';
  /** One primary button per area. */
  variant?: ButtonVariant;
}

export function Button({
  variant = 'secondary',
  className,
  type,
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cx('wl-button', `wl-button-${variant}`, className)}
      {...props}
    />
  );
}
