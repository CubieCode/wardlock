// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
} from 'react';
import { cx } from './cx.js';

export interface PopoverTriggerProps {
  ref: RefObject<HTMLButtonElement | null>;
  'aria-expanded': boolean;
  'aria-controls': string | undefined;
  'aria-haspopup': 'dialog';
  onClick: () => void;
}

const menuItemSelector =
  '[role="menuitem"]:not([aria-disabled="true"]), [role="menuitemcheckbox"]:not([aria-disabled="true"])';
const gap = 6;
const edge = 8;

/**
 * The lowest point a panel may reach: the top of a fixed bar at the foot of
 * the screen (a `BottomBar`) when one is showing, else the viewport's foot.
 */
function floor() {
  const bar = document.querySelector('.wl-bottom-bar');
  const top = bar?.getBoundingClientRect();
  return top && top.height > 0
    ? Math.min(window.innerHeight, top.top)
    : window.innerHeight;
}

/**
 * Where the panel goes: under or over the trigger, kept on screen and clear
 * of a bottom bar, and no taller than the room on that side.
 */
function place(
  trigger: HTMLElement,
  panel: HTMLElement,
  placement: 'below' | 'above',
): CSSProperties {
  const rect = trigger.getBoundingClientRect();
  const width = panel.offsetWidth;
  // The panel's natural height, not the height a previous fit gave it.
  const fitted = panel.style.maxHeight;
  panel.style.maxHeight = '';
  const height = panel.offsetHeight;
  panel.style.maxHeight = fitted;
  const bottom = floor();
  const left = Math.max(
    edge,
    Math.min(window.innerWidth - width - edge, rect.left),
  );
  const roomBelow = bottom - rect.bottom - gap - edge;
  const roomAbove = rect.top - gap - edge;
  const above =
    placement === 'above'
      ? roomAbove >= height || roomAbove > roomBelow
      : roomBelow < height && roomAbove > roomBelow;
  return above
    ? {
        left,
        bottom: window.innerHeight - rect.top + gap,
        maxHeight: Math.max(0, roomAbove),
      }
    : { left, top: rect.bottom + gap, maxHeight: Math.max(0, roomBelow) };
}

/**
 * A small non-modal panel anchored to the button that opens it, for a quick
 * choice such as picking labels (a filter field over a `Menu`). The caller
 * owns `open`. `trigger` renders the button from the props it is given, which
 * wire up `aria-expanded`, `aria-controls` and toggling. Opening moves focus
 * to the first field or item inside; Escape closes it and returns focus to
 * the trigger; a click outside or Tab past the panel closes it. The panel
 * stays on screen and clear of a `BottomBar`, flipping above the trigger
 * when there is no room below (`placement="above"` prefers above, for a bar
 * at the foot of the screen), and is never taller than the room it has.
 */
export function Popover({
  label,
  open,
  onOpenChange,
  trigger,
  placement = 'below',
  children,
}: {
  /** Names the panel for assistive technology. */
  label: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  trigger: (props: PopoverTriggerProps) => ReactNode;
  placement?: 'below' | 'above';
  children: ReactNode;
}) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const panelId = useId();
  const [position, setPosition] = useState<CSSProperties>({});

  const close = useCallback(
    (returnFocus: boolean) => {
      onOpenChange(false);
      if (returnFocus) triggerRef.current?.focus();
    },
    [onOpenChange],
  );

  useLayoutEffect(() => {
    if (!open) return;
    const reposition = () => {
      if (triggerRef.current && panelRef.current)
        setPosition(place(triggerRef.current, panelRef.current, placement));
    };
    reposition();
    window.addEventListener('resize', reposition);
    window.addEventListener('scroll', reposition, true);
    return () => {
      window.removeEventListener('resize', reposition);
      window.removeEventListener('scroll', reposition, true);
    };
  }, [open, placement]);

  useEffect(() => {
    if (!open) return;
    panelRef.current
      ?.querySelector<HTMLElement>(`input, ${menuItemSelector}`)
      ?.focus();
    const outside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (
        !panelRef.current?.contains(target) &&
        !triggerRef.current?.contains(target)
      )
        close(false);
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, [open, close]);

  return (
    <>
      {trigger({
        ref: triggerRef,
        'aria-expanded': open,
        'aria-controls': open ? panelId : undefined,
        'aria-haspopup': 'dialog',
        onClick: () => onOpenChange(!open),
      })}
      {open ? (
        <div
          ref={panelRef}
          id={panelId}
          role="dialog"
          aria-label={label}
          className="wl-popover"
          style={position}
          onKeyDown={(event) => {
            if (event.key !== 'Escape') return;
            event.stopPropagation();
            close(true);
          }}
          onBlur={(event) => {
            const next = event.relatedTarget as Node | null;
            if (
              next &&
              !event.currentTarget.contains(next) &&
              next !== triggerRef.current
            )
              close(false);
          }}
        >
          {children}
        </div>
      ) : null}
    </>
  );
}

/**
 * A labelled filter field at the top of a popover. Down arrow moves to the
 * first item of the menu below it.
 */
export function PopoverFilter({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <div className="wl-popover-filter">
      <label htmlFor={id} className="wl-popover-filter-label">
        {label}
      </label>
      <input
        id={id}
        type="search"
        className="wl-input"
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowDown') return;
          event.preventDefault();
          event.currentTarget
            .closest('.wl-popover')
            ?.querySelector<HTMLElement>(menuItemSelector)
            ?.focus();
        }}
      />
    </div>
  );
}

function moveFocus(event: KeyboardEvent<HTMLUListElement>) {
  const items = [
    ...event.currentTarget.querySelectorAll<HTMLElement>(menuItemSelector),
  ];
  const at = items.indexOf(document.activeElement as HTMLElement);
  const last = items.length - 1;
  let next: number;
  switch (event.key) {
    case 'ArrowDown':
      next = at < last ? at + 1 : 0;
      break;
    case 'ArrowUp':
      if (at === 0) {
        // Back to the filter field above, when there is one.
        const filter = event.currentTarget
          .closest('.wl-popover')
          ?.querySelector<HTMLElement>('.wl-popover-filter input');
        if (filter) {
          event.preventDefault();
          filter.focus();
          return;
        }
      }
      next = at > 0 ? at - 1 : last;
      break;
    case 'Home':
      next = 0;
      break;
    case 'End':
      next = last;
      break;
    default:
      return;
  }
  event.preventDefault();
  items[next]?.focus();
}

/**
 * A list of `MenuItem`s and `MenuCheckboxItem`s. Arrow keys, Home and End
 * move between items; one item at a time is in the Tab order.
 */
export function Menu({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLUListElement>(null);
  // Roving tab stop: keep exactly one enabled item tabbable, the one last
  // focused or else the first.
  useLayoutEffect(() => {
    const items = [
      ...(ref.current?.querySelectorAll<HTMLElement>(menuItemSelector) ?? []),
    ];
    if (items.some((item) => item.tabIndex === 0)) return;
    items[0]?.setAttribute('tabindex', '0');
  });
  return (
    <ul
      ref={ref}
      role="menu"
      aria-label={label}
      className="wl-menu"
      onKeyDown={moveFocus}
      onFocus={(event) => {
        if (!event.target.matches(menuItemSelector)) return;
        for (const item of event.currentTarget.querySelectorAll(
          menuItemSelector,
        ))
          item.setAttribute('tabindex', item === event.target ? '0' : '-1');
      }}
    >
      {children}
    </ul>
  );
}

/** An action in a `Menu`, such as "Create label". */
export function MenuItem({
  onSelect,
  icon,
  hint,
  children,
}: {
  onSelect: () => void;
  /** A small decorative glyph in the checkbox column, such as "+". */
  icon?: ReactNode;
  /** Short secondary text at the end of the row. */
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <li role="none">
      <button
        type="button"
        role="menuitem"
        tabIndex={-1}
        className="wl-menu-item"
        onClick={onSelect}
      >
        <span className="wl-menu-icon" aria-hidden="true">
          {icon}
        </span>
        <span className="wl-menu-label">{children}</span>
        {hint === undefined ? null : (
          <span className="wl-menu-hint">{hint}</span>
        )}
      </button>
    </li>
  );
}

/**
 * An on/off choice in a `Menu`. `mixed` says some of the selected things
 * have it and some do not; choosing a mixed item is the caller's to decide
 * (usually it turns on for all).
 */
export function MenuCheckboxItem({
  checked,
  onSelect,
  hint,
  children,
}: {
  checked: boolean | 'mixed';
  onSelect: () => void;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <li role="none">
      <button
        type="button"
        role="menuitemcheckbox"
        aria-checked={checked}
        tabIndex={-1}
        className="wl-menu-item"
        onClick={onSelect}
      >
        <span
          className={cx(
            'wl-menu-box',
            checked === true
              ? 'wl-menu-box-on'
              : checked === 'mixed'
                ? 'wl-menu-box-mixed'
                : null,
          )}
          aria-hidden="true"
        >
          {checked === true ? '✓' : checked === 'mixed' ? '–' : ''}
        </span>
        <span className="wl-menu-label">{children}</span>
        {hint === undefined ? null : (
          <span className="wl-menu-hint">{hint}</span>
        )}
      </button>
    </li>
  );
}
