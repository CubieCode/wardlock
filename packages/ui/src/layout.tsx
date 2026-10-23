// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useEffect,
  useId,
  useRef,
  useState,
  type AnchorHTMLAttributes,
  type ReactNode,
} from 'react';
import { cx } from './cx.js';

/**
 * The mark used beside the product name: a keyhole inside the ward, the
 * brass diamond. Decorative.
 */
export function Mark({ size = 24 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path className="wl-mark-ward" d="M12 2.5 21.5 12 12 21.5 2.5 12Z" />
      <circle cx="12" cy="10.5" r="2" />
      <path d="M12 12.5v3.5" />
    </svg>
  );
}

/** The product wordmark: the mark and the product name, linking home. */
function Wordmark({
  productName,
  href,
}: {
  productName: string;
  href: string;
}) {
  return (
    <a className="wl-wordmark" href={href}>
      <Mark />
      {productName}
    </a>
  );
}

/**
 * Page frame for signed-in screens: a sidebar with the wordmark, the
 * navigation and an account area, and a single main landmark. Below 48rem the
 * sidebar collapses into a top bar whose Menu button shows the navigation:
 * opening it moves focus to the first link, and Escape or following a link
 * closes it (Escape returns focus to the button).
 */
export function AppShell({
  productName,
  homeHref = '/',
  navigation,
  account,
  children,
}: {
  productName: string;
  homeHref?: string;
  /** Usually a NavList. Omit for a frame without navigation. */
  navigation?: ReactNode;
  /** Who is signed in and how to sign out; pinned to the sidebar's foot. */
  account?: ReactNode;
  children: ReactNode;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const panelId = useId();
  const menuButton = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (menuOpen)
      panel.current?.querySelector<HTMLElement>('a[href], button')?.focus();
  }, [menuOpen]);
  const hasSidebar = Boolean(navigation || account);
  return (
    <div className={cx('wl-shell', hasSidebar ? 'wl-shell-sidebar' : null)}>
      <header
        className="wl-shell-header"
        onKeyDown={(event) => {
          if (event.key !== 'Escape' || !menuOpen) return;
          setMenuOpen(false);
          menuButton.current?.focus();
        }}
      >
        <Wordmark productName={productName} href={homeHref} />
        {hasSidebar ? (
          <button
            ref={menuButton}
            type="button"
            className="wl-button wl-button-secondary wl-shell-menu"
            aria-expanded={menuOpen}
            aria-controls={panelId}
            onClick={() => setMenuOpen((open) => !open)}
          >
            {menuOpen ? 'Close' : 'Menu'}
          </button>
        ) : null}
        {hasSidebar ? (
          <div
            ref={panel}
            id={panelId}
            className="wl-shell-panel"
            data-open={menuOpen}
            onClick={(event) => {
              if ((event.target as Element).closest('a')) setMenuOpen(false);
            }}
          >
            {navigation}
            {account ? <div className="wl-shell-account">{account}</div> : null}
          </div>
        ) : null}
      </header>
      <main className="wl-main">{children}</main>
    </div>
  );
}

/**
 * Frame for a single focused task outside the signed-in shell, such as
 * signing in: the wordmark above one centred panel.
 */
export function PanelLayout({
  productName,
  homeHref = '/',
  children,
}: {
  productName: string;
  homeHref?: string;
  children: ReactNode;
}) {
  return (
    <div className="wl-panel-layout">
      <header className="wl-panel-header">
        <Wordmark productName={productName} href={homeHref} />
      </header>
      <main className="wl-panel">{children}</main>
    </div>
  );
}

/** A labelled list of navigation links. */
export function NavList({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <nav className="wl-nav" aria-label={label}>
      <ul>{children}</ul>
    </nav>
  );
}

/** One navigation link; `current` marks the page being shown. */
export function NavItem({
  current = false,
  className,
  ...props
}: AnchorHTMLAttributes<HTMLAnchorElement> & {
  href: string;
  current?: boolean;
}) {
  return (
    <li>
      <a
        className={cx('wl-nav-item', className)}
        aria-current={current ? 'page' : undefined}
        {...props}
      />
    </li>
  );
}

/** The page's single h1 and an optional description. */
export function PageHeader({
  title,
  description,
}: {
  title: string;
  description?: ReactNode;
}) {
  return (
    <div className="wl-page-header">
      <h1>{title}</h1>
      {description ? <p>{description}</p> : null}
    </div>
  );
}

/** A titled section of related content. */
export function Card({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <section className="wl-card" aria-label={title}>
      <h2>{title}</h2>
      {children}
    </section>
  );
}

/**
 * What a list shows when it has nothing in it: what would appear here and
 * how to add the first one.
 */
export function EmptyState({
  title,
  children,
  action,
}: {
  title: string;
  children: ReactNode;
  /** An optional next step, such as a link or button. */
  action?: ReactNode;
}) {
  return (
    <div className="wl-empty">
      <h2>{title}</h2>
      <div className="wl-empty-body">{children}</div>
      {action}
    </div>
  );
}
