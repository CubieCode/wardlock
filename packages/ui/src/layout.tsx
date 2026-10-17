// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ReactNode } from 'react';

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

/** Page frame: product header and a single main landmark. */
export function AppShell({
  productName,
  homeHref = '/',
  children,
}: {
  productName: string;
  homeHref?: string;
  children: ReactNode;
}) {
  return (
    <div className="wl-shell">
      <header className="wl-shell-header">
        <a className="wl-wordmark" href={homeHref}>
          <Mark />
          {productName}
        </a>
      </header>
      <main className="wl-main">{children}</main>
    </div>
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
