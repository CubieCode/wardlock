// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CommandPalette, type CommandGroup } from './index.js';

afterEach(cleanup);

const all: CommandGroup[] = [
  {
    label: 'Places',
    items: [
      { id: 'alpha', label: 'alpha', hint: 'alpha.example.test' },
      { id: 'beta', label: 'beta' },
    ],
  },
  { label: 'Pages', items: [{ id: 'overview', label: 'Overview' }] },
];

function Host({ onSelect = () => {} }: { onSelect?: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const groups = all.map((group) => ({
    ...group,
    items: group.items.filter((item) =>
      item.label.toLowerCase().includes(query.toLowerCase()),
    ),
  }));
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Jump to
      </button>
      <CommandPalette
        open={open}
        onClose={() => setOpen(false)}
        label="Jump to"
        query={query}
        onQueryChange={setQuery}
        groups={groups}
        onSelect={(item) => {
          onSelect(item.id);
          setOpen(false);
        }}
      />
    </>
  );
}

const open = () => {
  const opener = screen.getByRole('button', { name: 'Jump to' });
  opener.focus();
  fireEvent.click(opener);
  return opener;
};

describe('CommandPalette', () => {
  it('is a named modal with a combobox over a grouped listbox', () => {
    render(<Host />);
    open();
    const dialog = screen.getByRole('dialog', { name: 'Jump to' });
    const input = within(dialog).getByRole('combobox', { name: 'Jump to' });
    expect(document.activeElement).toBe(input);
    const list = within(dialog).getByRole('listbox', { name: 'Jump to' });
    expect(input.getAttribute('aria-controls')).toBe(list.id);
    const groups = within(list).getAllByRole('group');
    expect(groups.map((g) => g.getAttribute('aria-labelledby'))).toHaveLength(
      2,
    );
    expect(within(list).getByRole('group', { name: 'Places' })).toBeTruthy();
    expect(within(list).getByRole('group', { name: 'Pages' })).toBeTruthy();
    const options = within(list).getAllByRole('option');
    expect(options).toHaveLength(3);
    expect(options[0]!.getAttribute('aria-selected')).toBe('true');
    expect(input.getAttribute('aria-activedescendant')).toBe(options[0]!.id);
  });

  it('moves the highlight with arrow keys, wrapping, and opens with Enter', () => {
    const onSelect = vi.fn();
    render(<Host onSelect={onSelect} />);
    const opener = open();
    const input = screen.getByRole('combobox');
    const options = screen.getAllByRole('option');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input.getAttribute('aria-activedescendant')).toBe(options[2]!.id);
    expect(options[2]!.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(input.getAttribute('aria-activedescendant')).toBe(options[0]!.id);
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(input.getAttribute('aria-activedescendant')).toBe(options[2]!.id);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('overview');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it('filters as the caller narrows the groups, highlighting the first match', () => {
    render(<Host />);
    open();
    const input = screen.getByRole('combobox');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.change(input, { target: { value: 'over' } });
    const options = screen.getAllByRole('option');
    expect(options.map((o) => o.textContent)).toEqual(['Overview']);
    expect(screen.queryByRole('group', { name: 'Places' })).toBeNull();
    expect(input.getAttribute('aria-activedescendant')).toBe(options[0]!.id);
  });

  it('says when nothing matches', () => {
    render(<Host />);
    open();
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'zzz' } });
    expect(screen.queryAllByRole('option')).toHaveLength(0);
    expect(screen.getByText('Nothing matches')).toBeTruthy();
    expect(input.hasAttribute('aria-activedescendant')).toBe(false);
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('opens an option on click', () => {
    const onSelect = vi.fn();
    render(<Host onSelect={onSelect} />);
    open();
    fireEvent.click(screen.getByRole('option', { name: /beta/ }));
    expect(onSelect).toHaveBeenCalledWith('beta');
  });

  it('closes on Escape and returns focus to the opener', () => {
    render(<Host />);
    const opener = open();
    const dialog = screen.getByRole('dialog');
    act(() => {
      dialog.dispatchEvent(new Event('cancel', { cancelable: true }));
    });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });
});
