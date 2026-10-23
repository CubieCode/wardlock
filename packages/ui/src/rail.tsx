// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useId,
  useRef,
  type AnchorHTMLAttributes,
  type ButtonHTMLAttributes,
  type ReactNode,
  type Ref,
} from 'react';
import { cx } from './cx.js';
import { Mark } from './layout.js';
import { useModal } from './modal.js';

/**
 * Page frame for signed-in screens. From 48rem: the `rail` down the left, an
 * optional `navigator` column beside it, then the page. Below 48rem the rail
 * and navigator are hidden: `header` runs across the top, `bottomBar` across
 * the foot (pass the same places to both), and the navigator moves into a
 * `Drawer` that the header opens. One `main` landmark.
 */
export function RailLayout({
  rail,
  bottomBar,
  header,
  navigator,
  navigatorLabel,
  children,
}: {
  /** A `Rail`. */
  rail: ReactNode;
  /** A `BottomBar` with the rail's places. */
  bottomBar: ReactNode;
  /** The small-screen top bar: usually the wordmark and a drawer button. */
  header?: ReactNode;
  /** A secondary column, such as filters, from 48rem. */
  navigator?: ReactNode;
  /** Names the navigator column; required with `navigator`. */
  navigatorLabel?: string;
  children: ReactNode;
}) {
  return (
    <div
      className={cx(
        'wl-rail-layout',
        navigator ? 'wl-rail-layout-navigator' : null,
      )}
    >
      {rail}
      {navigator ? (
        <aside className="wl-rail-navigator" aria-label={navigatorLabel}>
          {navigator}
        </aside>
      ) : null}
      {header ? <header className="wl-rail-header">{header}</header> : null}
      <main className="wl-main">{children}</main>
      {bottomBar}
    </div>
  );
}

/**
 * The main places as a column of icons, from 48rem: the mark linking home,
 * `children` (`RailItem`s), then `foot` at the bottom (`RailButton`s such as
 * search or the account menu). Each item's name shows beside it on hover and
 * keyboard focus.
 */
export function Rail({
  label,
  productName,
  homeHref = '/',
  children,
  foot,
}: {
  /** Names the navigation landmark, such as "Main". */
  label: string;
  /** The home link's name. */
  productName: string;
  homeHref?: string;
  children: ReactNode;
  foot?: ReactNode;
}) {
  return (
    <nav className="wl-rail" aria-label={label}>
      <a className="wl-rail-home" href={homeHref}>
        <Mark />
        <span className="wl-visually-hidden">{productName}</span>
      </a>
      <div className="wl-rail-items">{children}</div>
      {foot ? <div className="wl-rail-items wl-rail-foot">{foot}</div> : null}
    </nav>
  );
}

/**
 * The main places along the foot of the screen below 48rem, each an icon over
 * its name. Takes the same `RailItem`s and `RailButton`s as the `Rail`. Leave
 * room for it at the page's foot (`RailLayout` does).
 */
export function BottomBar({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <nav className="wl-bottom-bar" aria-label={label}>
      {children}
    </nav>
  );
}

/**
 * One place in a `Rail` or `BottomBar`: a link with a decorative `icon` and
 * its `label` as the name. `current` marks the place being shown.
 */
export function RailItem({
  icon,
  label,
  current = false,
  className,
  ...props
}: Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'children'> & {
  href: string;
  icon: ReactNode;
  label: string;
  current?: boolean;
}) {
  return (
    <a
      className={cx('wl-rail-item', className)}
      aria-current={current ? 'page' : undefined}
      {...props}
    >
      <span className="wl-rail-icon" aria-hidden="true">
        {icon}
      </span>
      <span className="wl-rail-label">{label}</span>
    </a>
  );
}

/**
 * An action in a `Rail` or `BottomBar` drawn like a `RailItem`, such as
 * opening search or an account menu (it can be a `Popover` trigger). A
 * `shortcut` shows beside the name in the rail and is announced through
 * `aria-keyshortcuts` (pass that too, in its ARIA form).
 */
export function RailButton({
  icon,
  label,
  shortcut,
  className,
  ref,
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'type'> & {
  icon: ReactNode;
  label: string;
  shortcut?: string;
  ref?: Ref<HTMLButtonElement>;
}) {
  return (
    <button
      ref={ref}
      type="button"
      className={cx('wl-rail-item', className)}
      {...props}
    >
      <span className="wl-rail-icon" aria-hidden="true">
        {icon}
      </span>
      <span className="wl-rail-label">
        {label}
        {shortcut ? (
          <kbd className="wl-kbd wl-rail-shortcut" aria-hidden="true">
            {shortcut}
          </kbd>
        ) : null}
      </span>
    </button>
  );
}

/**
 * A panel that slides in from the start edge over the page, for content that
 * sits beside the page on wide screens (a navigator). A native modal
 * `<dialog>`: the page behind is inert and focus stays inside; the caller
 * owns `open`. Escape, the Close button, a click on the backdrop and
 * following a link inside all call `onClose`, and focus returns to whatever
 * opened it.
 */
export function Drawer({
  open,
  onClose,
  title,
  children,
}: {
  open: boolean;
  onClose: () => void;
  /** The drawer's heading and accessible name. */
  title: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const handlers = useModal(ref, open, onClose);
  return (
    <dialog
      ref={ref}
      className="wl-drawer"
      aria-labelledby={titleId}
      {...handlers}
      onClick={(event) => {
        // A click on the backdrop lands on the dialog element itself.
        if (
          event.target === event.currentTarget ||
          (event.target as Element).closest('a[href]')
        )
          onClose();
      }}
    >
      {open ? (
        <>
          <div className="wl-drawer-head">
            <h2 id={titleId} className="wl-drawer-title">
              {title}
            </h2>
            <button type="button" className="wl-drawer-close" onClick={onClose}>
              Close
            </button>
          </div>
          <div className="wl-drawer-body">{children}</div>
        </>
      ) : null}
    </dialog>
  );
}
