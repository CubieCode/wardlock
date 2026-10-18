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
import { Button, CopyField, Dialog, EmptyState } from './index.js';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function Confirm({
  onClose = () => {},
  busy = false,
}: {
  onClose?: () => void;
  /** While busy the dialog ignores requests to close, as callers do. */
  busy?: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button type="button" onClick={() => setOpen(true)}>
        Remove
      </Button>
      <Dialog
        open={open}
        title="Remove the thing?"
        onClose={() => {
          onClose();
          if (!busy) setOpen(false);
        }}
        actions={
          <>
            <Button type="button" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="danger">
              Remove thing
            </Button>
          </>
        }
      >
        <p>It cannot be undone.</p>
      </Dialog>
    </>
  );
}

describe('Dialog', () => {
  it('is named by its title, described by its body and renders nothing closed', () => {
    render(<Confirm />);
    expect(screen.queryByText('It cannot be undone.')).toBeNull();
    const opener = screen.getByRole('button', { name: 'Remove' });
    opener.focus();
    fireEvent.click(opener);
    const dialog = screen.getByRole('dialog', { name: 'Remove the thing?' });
    expect(dialog.getAttribute('aria-describedby')).toBe(
      screen.getByText('It cannot be undone.').parentElement!.id,
    );
    expect(
      screen.getByRole('button', { name: 'Remove thing' }).className,
    ).toContain('wl-button-danger');

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('It cannot be undone.')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it('asks the caller to close on Escape', () => {
    const onClose = vi.fn();
    render(<Confirm onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = screen.getByRole('dialog');
    const cancel = new Event('cancel', { cancelable: true });
    act(() => {
      dialog.dispatchEvent(cancel);
    });
    expect(onClose).toHaveBeenCalledOnce();
    expect(cancel.defaultPrevented).toBe(true);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('Dialog closed by the browser', () => {
  // Chromium closes a modal dialog on a second Escape without a cancellable
  // cancel event; the element then fires only "close".
  const closeNatively = (dialog: HTMLElement) =>
    act(async () => {
      dialog.removeAttribute('open');
      dialog.dispatchEvent(new Event('close'));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

  it('tells the caller, which closes it', async () => {
    const onClose = vi.fn();
    render(<Confirm onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    await closeNatively(screen.getByRole('dialog'));
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.queryByText('It cannot be undone.')).toBeNull();
  });

  it('shows itself again while the caller keeps it open', async () => {
    const onClose = vi.fn();
    render(<Confirm onClose={onClose} busy />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = screen.getByRole('dialog') as HTMLDialogElement;
    await closeNatively(dialog);
    expect(onClose).toHaveBeenCalledOnce();
    expect(dialog.hasAttribute('open')).toBe(true);
    expect(screen.getByText('It cannot be undone.')).toBeTruthy();
  });

  it('ignores the close that follows its own closing', async () => {
    const onClose = vi.fn();
    render(<Confirm onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = screen.getByRole('dialog', { hidden: true });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await closeNatively(dialog);
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe('CopyField', () => {
  it('copies the value and says so', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    render(<CopyField label="Site address" value="https://a.example.test/" />);
    expect(
      (
        screen.getByRole('textbox', {
          name: 'Site address',
        }) as HTMLInputElement
      ).readOnly,
    ).toBe(true);
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy site address' }),
      );
    });
    expect(writeText).toHaveBeenCalledWith('https://a.example.test/');
    expect(screen.getByRole('status').textContent).toBe('Site address copied.');
  });

  it('selects the value when the clipboard is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    });
    render(<CopyField label="Site address" value="https://a.example.test/" />);
    const input = screen.getByRole('textbox', {
      name: 'Site address',
    }) as HTMLInputElement;
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy site address' }),
      );
    });
    expect(screen.getByRole('status').textContent).toContain(
      'Could not copy automatically',
    );
    expect(input.selectionEnd! - input.selectionStart!).toBe(
      input.value.length,
    );
  });
});

describe('EmptyState', () => {
  it('titles the empty list and offers the next step', () => {
    render(
      <EmptyState title="Nothing yet" action={<a href="/new">Add one</a>}>
        <p>Things you add appear here.</p>
      </EmptyState>,
    );
    expect(
      screen.getByRole('heading', { level: 2, name: 'Nothing yet' }),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Add one' })).toBeTruthy();
  });
});
