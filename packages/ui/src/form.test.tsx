// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { TextField } from './index.js';

afterEach(cleanup);

describe('TextField', () => {
  it('labels the input and passes input attributes through', () => {
    render(
      <TextField
        label="Email address"
        type="email"
        name="email"
        autoComplete="email"
        required
      />,
    );
    const input = screen.getByRole('textbox', { name: 'Email address' });
    expect(input.getAttribute('type')).toBe('email');
    expect(input.getAttribute('autocomplete')).toBe('email');
    expect(input.hasAttribute('required')).toBe(true);
    expect(input.hasAttribute('aria-invalid')).toBe(false);
    expect(input.hasAttribute('aria-describedby')).toBe(false);
  });

  it('describes the input with its hint', () => {
    render(<TextField label="Code" hint="Six digits from the email." />);
    expect(
      screen
        .getByRole('textbox', { name: 'Code' })
        .getAttribute('aria-describedby'),
    ).toBeTruthy();
    expect(
      screen.getByRole('textbox', {
        description: 'Six digits from the email.',
      }),
    ).toBeTruthy();
  });

  it('marks the input invalid and announces the error', () => {
    render(
      <TextField
        label="Code"
        hint="Six digits from the email."
        error="That code is not right."
      />,
    );
    const input = screen.getByRole('textbox', {
      name: 'Code',
      description: 'Six digits from the email. That code is not right.',
    });
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByRole('alert').textContent).toBe(
      'That code is not right.',
    );
  });

  it('gives each field its own ids', () => {
    render(
      <>
        <TextField label="First" error="Wrong" />
        <TextField label="Second" error="Also wrong" />
      </>,
    );
    expect(
      screen.getByRole('textbox', { name: 'First', description: 'Wrong' }),
    ).toBeTruthy();
    expect(
      screen.getByRole('textbox', {
        name: 'Second',
        description: 'Also wrong',
      }),
    ).toBeTruthy();
  });
});
