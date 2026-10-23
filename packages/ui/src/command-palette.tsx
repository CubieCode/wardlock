// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { cx } from './cx.js';
import { useModal } from './modal.js';

export interface CommandItem {
  id: string;
  label: string;
  /** Short mono text at the end of the row, such as an address. */
  hint?: string;
  /** A small decorative glyph before the label. */
  icon?: ReactNode;
}

export interface CommandGroup {
  label: string;
  items: CommandItem[];
}

/**
 * A modal search over places and actions, opened from a button or a
 * shortcut. The caller owns `open`, `query` and the matching `groups`; the
 * palette shows them under their headings. The field is a combobox over a
 * grouped listbox: Up and Down move the highlighted option (wrapping), Enter
 * or a click calls `onSelect`, Escape calls `onClose`, and closing returns
 * focus to whatever opened it. Typing moves the highlight back to the first
 * option. Otherwise the highlight stays on its option (by `id`) as `groups`
 * change, so results that arrive late never move what Enter opens; when that
 * option is gone it falls back to the first. Below 48rem it spans the
 * screen's width.
 */
export function CommandPalette({
  open,
  onClose,
  label,
  placeholder,
  query,
  onQueryChange,
  groups,
  onSelect,
  emptyText = 'Nothing matches',
  footer,
}: {
  open: boolean;
  onClose: () => void;
  /** Names the palette and labels its field, such as "Jump to". */
  label: string;
  placeholder?: string;
  query: string;
  onQueryChange: (query: string) => void;
  /** Matches for `query`, in order. Empty groups are left out. */
  groups: CommandGroup[];
  /** Called with the chosen option; the caller usually closes the palette. */
  onSelect: (item: CommandItem) => void;
  emptyText?: string;
  /** Short text at the foot, such as how many things are searched. */
  footer?: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const handlers = useModal(ref, open, onClose);
  const id = useId();
  // The highlighted option's id; undefined means the first option.
  const [activeId, setActiveId] = useState<string>();

  const shown = groups.filter((group) => group.items.length > 0);
  const options = shown.flatMap((group) => group.items);
  // Where each group's options start in `options`.
  const starts = shown.map((_, g) =>
    shown.slice(0, g).reduce((sum, group) => sum + group.items.length, 0),
  );
  const found = options.findIndex((item) => item.id === activeId);
  const at = Math.max(0, found);
  // Pin the highlight to the option shown, so it stays put when results
  // arrive above it (setting state while rendering re-renders at once).
  if (found < 0 && options.length > 0) setActiveId(options[0]!.id);
  const optionId = (index: number) => `${id}-option-${index}`;

  useEffect(() => {
    if (!open) return;
    const option = listRef.current?.querySelector(`[aria-selected="true"]`);
    if (option && 'scrollIntoView' in option)
      option.scrollIntoView({ block: 'nearest' });
  }, [open, at, query]);

  return (
    <dialog
      ref={ref}
      className="wl-palette"
      aria-label={label}
      {...handlers}
      onClick={(event) => {
        // A click on the backdrop lands on the dialog element itself.
        if (event.target === event.currentTarget) onClose();
      }}
    >
      {open ? (
        <>
          <div className="wl-palette-search">
            <label htmlFor={`${id}-input`} className="wl-visually-hidden">
              {label}
            </label>
            <input
              id={`${id}-input`}
              className="wl-palette-input"
              type="text"
              role="combobox"
              aria-expanded="true"
              aria-controls={`${id}-list`}
              aria-autocomplete="list"
              aria-activedescendant={
                options.length > 0 ? optionId(at) : undefined
              }
              autoComplete="off"
              spellCheck={false}
              placeholder={placeholder}
              value={query}
              autoFocus
              onChange={(event) => {
                setActiveId(undefined);
                onQueryChange(event.target.value);
              }}
              onKeyDown={(event) => {
                const count = options.length;
                if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                  event.preventDefault();
                  if (count === 0) return;
                  const step = event.key === 'ArrowDown' ? 1 : -1;
                  setActiveId(options[(at + step + count) % count]!.id);
                } else if (event.key === 'Enter') {
                  event.preventDefault();
                  const item = options[at];
                  if (item) onSelect(item);
                }
              }}
            />
            <kbd className="wl-kbd" aria-hidden="true">
              esc
            </kbd>
          </div>
          <ul
            ref={listRef}
            id={`${id}-list`}
            role="listbox"
            aria-label={label}
            className="wl-palette-list"
          >
            {shown.length === 0 ? (
              <li role="presentation" className="wl-palette-empty">
                {emptyText}
              </li>
            ) : (
              shown.map((group, g) => (
                <li key={group.label} role="presentation">
                  <ul role="group" aria-labelledby={`${id}-group-${g}`}>
                    <li
                      role="presentation"
                      id={`${id}-group-${g}`}
                      className="wl-palette-group"
                    >
                      {group.label}
                    </li>
                    {group.items.map((item, n) => {
                      const i = starts[g]! + n;
                      return (
                        <li
                          key={item.id}
                          id={optionId(i)}
                          role="option"
                          aria-selected={i === at}
                          className={cx(
                            'wl-palette-option',
                            i === at ? 'wl-palette-option-active' : null,
                          )}
                          onPointerMove={() => {
                            if (i !== at) setActiveId(item.id);
                          }}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => onSelect(item)}
                        >
                          <span className="wl-palette-icon" aria-hidden="true">
                            {item.icon}
                          </span>
                          <span className="wl-palette-label">{item.label}</span>
                          {item.hint ? (
                            <span className="wl-palette-hint">{item.hint}</span>
                          ) : null}
                        </li>
                      );
                    })}
                  </ul>
                </li>
              ))
            )}
          </ul>
          <div className="wl-palette-foot">
            <span aria-hidden="true">
              <kbd className="wl-kbd">↑↓</kbd> move
            </span>
            <span aria-hidden="true">
              <kbd className="wl-kbd">↵</kbd> open
            </span>
            {footer ? <span>{footer}</span> : null}
          </div>
        </>
      ) : null}
    </dialog>
  );
}
