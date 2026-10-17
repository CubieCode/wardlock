// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  useId,
  useState,
  type AnchorHTMLAttributes,
  type ReactNode,
} from 'react';
import { cx } from './cx.js';

/** The key-in-ward mark used beside the product name. Decorative. */
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
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="12" cy="9" r="4" />
      <path d="M12 13v8M12 17h3M12 20h2" />
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
 * sidebar collapses into a top bar whose Menu button shows the navigation;
 * following a link in it closes it again.
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
  const hasSidebar = Boolean(navigation || account);
  return (
    <div className={cx('wl-shell', hasSidebar ? 'wl-shell-sidebar' : null)}>
      <header className="wl-shell-header">
        <Wordmark productName={productName} href={homeHref} />
        {hasSidebar ? (
          <button
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
