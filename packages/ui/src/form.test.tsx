// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  Checkbox,
  Fieldset,
  Radio,
  SelectField,
  TextAreaField,
  TextField,
} from './index.js';

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

  it('keeps its own description and invalid state over passed props', () => {
    render(
      <>
        <p id="extra">Extra guidance.</p>
        <TextField
          label="Code"
          error="That code is not right."
          aria-describedby="extra"
          aria-invalid={false}
        />
      </>,
    );
    const input = screen.getByRole('textbox', {
      name: 'Code',
      description: 'Extra guidance. That code is not right.',
    });
    expect(input.getAttribute('aria-invalid')).toBe('true');
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

describe('SelectField', () => {
  const options = (
    <>
      <option value="never">Never</option>
      <option value="1d">In 1 day</option>
    </>
  );

  it('labels the select, describes it and reports changes', () => {
    let chosen = '';
    render(
      <SelectField
        label="Expires"
        hint="When the link stops working."
        defaultValue="never"
        onChange={(event) => (chosen = event.target.value)}
      >
        {options}
      </SelectField>,
    );
    const select = screen.getByRole('combobox', {
      name: 'Expires',
      description: 'When the link stops working.',
    });
    expect((select as HTMLSelectElement).value).toBe('never');
    expect(select.hasAttribute('aria-invalid')).toBe(false);
    fireEvent.change(select, { target: { value: '1d' } });
    expect(chosen).toBe('1d');
  });

  it('marks the select invalid and announces the error', () => {
    render(
      <SelectField label="Expires" error="Choose when the link stops working.">
        {options}
      </SelectField>,
    );
    const select = screen.getByRole('combobox', {
      name: 'Expires',
      description: 'Choose when the link stops working.',
    });
    expect(select.getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByRole('alert').textContent).toBe(
      'Choose when the link stops working.',
    );
  });
});

describe('TextAreaField', () => {
  it('labels the text area, describes it and marks errors', () => {
    render(
      <TextAreaField
        label="Allowed people"
        hint="One per line."
        error="Add at least one."
        defaultValue="a@example.test"
      />,
    );
    const area = screen.getByRole('textbox', {
      name: 'Allowed people',
      description: 'One per line. Add at least one.',
    });
    expect(area.tagName).toBe('TEXTAREA');
    expect((area as HTMLTextAreaElement).value).toBe('a@example.test');
    expect(area.getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByRole('alert').textContent).toBe('Add at least one.');
  });
});

describe('Fieldset, Radio and Checkbox', () => {
  it('groups radios under a legend and reports the choice', () => {
    let chosen = '';
    render(
      <Fieldset legend="Who can open it" hint="Choose one.">
        <Radio
          name="who"
          value="everyone"
          label="Everyone"
          hint="No sign-in."
          defaultChecked
          onChange={(event) => (chosen = event.target.value)}
        />
        <Radio
          name="who"
          value="some"
          label="Some people"
          onChange={(event) => (chosen = event.target.value)}
        />
      </Fieldset>,
    );
    expect(
      screen.getByRole('group', {
        name: 'Who can open it',
        description: 'Choose one.',
      }),
    ).toBeTruthy();
    const everyone = screen.getByRole('radio', {
      name: 'Everyone',
      description: 'No sign-in.',
    });
    expect((everyone as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByText('Some people'));
    expect(chosen).toBe('some');
  });

  it('announces a group error and labels checkboxes', () => {
    render(
      <Fieldset legend="Ways in" error="Choose at least one.">
        <Checkbox label="Password" hint="One password for everyone." />
      </Fieldset>,
    );
    const group = screen.getByRole('group', {
      name: 'Ways in',
      description: 'Choose at least one.',
    });
    expect(group.hasAttribute('aria-invalid')).toBe(false);
    expect(screen.getByRole('alert').textContent).toBe('Choose at least one.');
    const box = screen.getByRole('checkbox', {
      name: 'Password',
      description: 'One password for everyone.',
    });
    fireEvent.click(screen.getByText('Password'));
    expect((box as HTMLInputElement).checked).toBe(true);
  });

  it('shows a partly chosen box as mixed and can hide its label', () => {
    const { rerender } = render(
      <Checkbox label="Select all in Docs" indeterminate hideLabel />,
    );
    const box = screen.getByRole('checkbox', { name: 'Select all in Docs' });
    expect((box as HTMLInputElement).indeterminate).toBe(true);
    expect(screen.getByText('Select all in Docs').className).toBe(
      'wl-visually-hidden',
    );
    // The label wraps the box, so clicking the 44px target toggles it.
    fireEvent.click(box.closest('label')!);
    expect((box as HTMLInputElement).checked).toBe(true);
    rerender(<Checkbox label="Select all in Docs" checked readOnly />);
    expect(
      (
        screen.getByRole('checkbox', {
          name: 'Select all in Docs',
        }) as HTMLInputElement
      ).indeterminate,
    ).toBe(false);
  });
});
