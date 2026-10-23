// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  BulkBar,
  Menu,
  MenuCheckboxItem,
  MenuItem,
  Popover,
  PopoverFilter,
  Toast,
} from './index.js';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const labels = ['docs', 'internal', 'launch'];

function Picker({
  onCreate = () => {},
}: {
  onCreate?: (name: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [state, setState] = useState<Record<string, boolean | 'mixed'>>({
    docs: true,
    internal: 'mixed',
    launch: false,
  });
  const shown = labels.filter((name) => name.includes(query));
  return (
    <>
      <p>Outside</p>
      <Popover
        label="Labels"
        open={open}
        onOpenChange={setOpen}
        trigger={(props) => (
          <button type="button" {...props}>
            Labels
          </button>
        )}
      >
        <PopoverFilter label="Labels" value={query} onChange={setQuery} />
        <Menu label="Labels">
          {shown.map((name) => (
            <MenuCheckboxItem
              key={name}
              checked={state[name]!}
              hint="2"
              onSelect={() =>
                setState((s) => ({ ...s, [name]: s[name] !== true }))
              }
            >
              {name}
            </MenuCheckboxItem>
          ))}
          {query && !labels.includes(query) ? (
            <MenuItem icon="+" onSelect={() => onCreate(query)}>
              Create “{query}”
            </MenuItem>
          ) : null}
        </Menu>
      </Popover>
    </>
  );
}

const trigger = () => screen.getByRole('button', { name: 'Labels' });

describe('Popover with a menu', () => {
  it('opens from its trigger and focuses the filter', () => {
    render(<Picker />);
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(trigger().getAttribute('aria-haspopup')).toBe('dialog');
    fireEvent.click(trigger());
    const panel = screen.getByRole('dialog', { name: 'Labels' });
    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(trigger().getAttribute('aria-controls')).toBe(panel.id);
    expect(document.activeElement).toBe(
      screen.getByRole('searchbox', { name: 'Labels' }),
    );
  });

  it('shows checked, unchecked and mixed items', () => {
    render(<Picker />);
    fireEvent.click(trigger());
    const item = (name: string) =>
      screen.getByRole('menuitemcheckbox', { name: new RegExp(name) });
    expect(item('docs').getAttribute('aria-checked')).toBe('true');
    expect(item('internal').getAttribute('aria-checked')).toBe('mixed');
    expect(item('launch').getAttribute('aria-checked')).toBe('false');
    fireEvent.click(item('internal'));
    expect(item('internal').getAttribute('aria-checked')).toBe('true');
    fireEvent.click(item('docs'));
    expect(item('docs').getAttribute('aria-checked')).toBe('false');
  });

  it('moves with arrow keys between the filter and items', () => {
    render(<Picker />);
    fireEvent.click(trigger());
    const filter = screen.getByRole('searchbox');
    const items = screen.getAllByRole('menuitemcheckbox');
    fireEvent.keyDown(filter, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[0]);
    const menu = screen.getByRole('menu', { name: 'Labels' });
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(menu, { key: 'End' });
    expect(document.activeElement).toBe(items[2]);
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(menu, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(filter);
    // One item at a time is in the Tab order.
    fireEvent.keyDown(filter, { key: 'ArrowDown' });
    fireEvent.keyDown(menu, { key: 'ArrowDown' });
    expect(items.map((item) => item.tabIndex)).toEqual([-1, 0, -1]);
  });

  it('filters, and offers to create what is not there', () => {
    const onCreate = vi.fn();
    render(<Picker onCreate={onCreate} />);
    fireEvent.click(trigger());
    fireEvent.change(screen.getByRole('searchbox'), {
      target: { value: 'la' },
    });
    expect(screen.getAllByRole('menuitemcheckbox')).toHaveLength(1);
    fireEvent.click(screen.getByRole('menuitem', { name: 'Create “la”' }));
    expect(onCreate).toHaveBeenCalledWith('la');
  });

  it('closes on Escape and returns focus to the trigger', () => {
    render(<Picker />);
    fireEvent.click(trigger());
    fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(trigger());
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });

  it('closes on a click outside', () => {
    render(<Picker />);
    fireEvent.click(trigger());
    fireEvent.pointerDown(screen.getByText('Outside'));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('Popover placement', () => {
  it('stays clear of a bottom bar and fits the room it has', () => {
    const rects = new Map<string, Partial<DOMRect>>([
      ['trigger', { top: 600, bottom: 640, left: 20, height: 40 }],
      ['bar', { top: 780, bottom: 844, left: 0, height: 64 }],
    ]);
    const rect = vi
      .spyOn(Element.prototype, 'getBoundingClientRect')
      .mockImplementation(function (this: Element) {
        const key = this.classList.contains('wl-bottom-bar')
          ? 'bar'
          : this.tagName === 'BUTTON'
            ? 'trigger'
            : '';
        return {
          top: 0,
          bottom: 0,
          left: 0,
          height: 0,
          ...rects.get(key),
        } as DOMRect;
      });
    const height = vi
      .spyOn(HTMLElement.prototype, 'offsetHeight', 'get')
      .mockReturnValue(300);
    Object.assign(window, { innerHeight: 844, innerWidth: 390 });
    try {
      render(
        <>
          <nav className="wl-bottom-bar" />
          <Picker />
        </>,
      );
      fireEvent.click(trigger());
      const panel = screen.getByRole('dialog', { name: 'Labels' });
      // 300px does not fit between the trigger and the bar (124px), so it
      // opens above, no taller than the 586px above the trigger.
      expect(panel.style.top).toBe('');
      expect(panel.style.bottom).toBe(`${844 - 600 + 6}px`);
      expect(panel.style.maxHeight).toBe(`${600 - 6 - 8}px`);
    } finally {
      rect.mockRestore();
      height.mockRestore();
    }
  });
});

describe('BulkBar', () => {
  function Selection({ initial }: { initial: number }) {
    const [count, setCount] = useState(initial);
    return (
      <BulkBar label="Bulk actions" count={count} onClear={() => setCount(0)}>
        <button type="button">Tags</button>
        <button type="button">Move</button>
      </BulkBar>
    );
  }

  it('renders nothing with nothing selected', () => {
    render(<Selection initial={0} />);
    expect(screen.queryByRole('toolbar')).toBeNull();
  });

  it('is a named toolbar with the count, actions and Clear selection', () => {
    render(<Selection initial={3} />);
    const bar = screen.getByRole('toolbar', { name: 'Bulk actions' });
    expect(bar.textContent).toContain('3 selected');
    const [tags, move, clear] = [
      screen.getByRole('button', { name: 'Tags' }),
      screen.getByRole('button', { name: 'Move' }),
      screen.getByRole('button', { name: 'Clear selection' }),
    ];
    tags.focus();
    fireEvent.keyDown(tags, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(move);
    fireEvent.keyDown(move, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(clear);
    fireEvent.keyDown(clear, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(tags);
    fireEvent.keyDown(tags, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(clear);
    fireEvent.click(clear);
    expect(screen.queryByRole('toolbar')).toBeNull();
  });
});

describe('Toast', () => {
  function Host({ onUndo }: { onUndo?: () => void }) {
    const [message, setMessage] = useState<string | null>('Tagged 3 items');
    return (
      <>
        <Toast
          message={message}
          onDismiss={() => setMessage(null)}
          {...(onUndo ? { onUndo } : {})}
        />
        <button type="button">Elsewhere</button>
      </>
    );
  }

  it('announces its message politely and leaves after 5 seconds', () => {
    vi.useFakeTimers();
    render(<Host />);
    const region = screen.getByRole('status');
    expect(region.textContent).toBe('Tagged 3 items');
    expect(screen.queryByRole('button', { name: 'Undo' })).toBeNull();
    act(() => vi.advanceTimersByTime(4999));
    expect(region.textContent).toBe('Tagged 3 items');
    act(() => vi.advanceTimersByTime(1));
    expect(region.textContent).toBe('');
    // The live region stays, ready for the next message.
    expect(screen.getByRole('status')).toBe(region);
  });

  it('undoes and dismisses', () => {
    const onUndo = vi.fn();
    render(<Host onUndo={onUndo} />);
    fireEvent.click(screen.getByRole('button', { name: /Undo/ }));
    expect(onUndo).toHaveBeenCalledOnce();
    expect(screen.getByRole('status').textContent).toBe('');
  });

  it('waits while focus is inside it', () => {
    vi.useFakeTimers();
    render(<Host onUndo={() => {}} />);
    const undo = screen.getByRole('button', { name: /Undo/ });
    act(() => undo.focus());
    act(() => vi.advanceTimersByTime(10000));
    expect(screen.getByRole('status').textContent).toContain('Tagged 3 items');
    act(() => screen.getByRole('button', { name: 'Elsewhere' }).focus());
    act(() => vi.advanceTimersByTime(10000));
    expect(screen.getByRole('status').textContent).toBe('');
  });

  it('gives a change with Undo ten seconds, and says how to undo by key', () => {
    vi.useFakeTimers();
    render(<Host onUndo={() => {}} />);
    const status = screen.getByRole('status');
    expect(status.textContent).toContain('Control Z undoes it.');
    expect(
      screen
        .getByRole('button', { name: /Undo/ })
        .getAttribute('aria-keyshortcuts'),
    ).toBe('Control+Z');
    act(() => vi.advanceTimersByTime(9999));
    expect(status.textContent).toContain('Tagged 3 items');
    act(() => vi.advanceTimersByTime(1));
    expect(status.textContent).toBe('');
  });

  it('undoes on Ctrl+Z, but not while typing in a field', () => {
    const onUndo = vi.fn();
    render(
      <>
        <Host onUndo={onUndo} />
        <input aria-label="Search" />
      </>,
    );
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Search' }), {
      key: 'z',
      ctrlKey: true,
    });
    expect(onUndo).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: 'z', ctrlKey: true });
    expect(onUndo).toHaveBeenCalledOnce();
    expect(screen.getByRole('status').textContent).toBe('');
    // Gone with its toast: a second press does nothing.
    fireEvent.keyDown(document.body, { key: 'z', ctrlKey: true });
    expect(onUndo).toHaveBeenCalledOnce();
  });

  it('keeps a failure, announced assertively, until it is dismissed', () => {
    vi.useFakeTimers();
    function Failure() {
      const [message, setMessage] = useState<string | null>(
        'Could not tag 3 items.',
      );
      return (
        <Toast
          tone="danger"
          message={message}
          onDismiss={() => setMessage(null)}
        />
      );
    }
    render(<Failure />);
    const alert = screen.getByRole('alert');
    expect(alert.textContent).toContain('Could not tag 3 items.');
    expect(screen.getByRole('status').textContent).toBe('');
    act(() => vi.advanceTimersByTime(60_000));
    expect(alert.textContent).toContain('Could not tag 3 items.');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(alert.textContent).toBe('');
  });

  it('waits while the pointer is over it', () => {
    vi.useFakeTimers();
    render(<Host />);
    const toast = screen.getByText('Tagged 3 items').parentElement!;
    fireEvent.pointerEnter(toast);
    act(() => vi.advanceTimersByTime(10000));
    expect(screen.getByRole('status').textContent).toBe('Tagged 3 items');
    fireEvent.pointerLeave(toast);
    act(() => vi.advanceTimersByTime(5000));
    expect(screen.getByRole('status').textContent).toBe('');
  });
});
