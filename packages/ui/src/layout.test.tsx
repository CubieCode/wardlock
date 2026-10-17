// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { AppShell, NavItem, NavList, PanelLayout } from './index.js';

afterEach(cleanup);

const shell = () =>
  render(
    <AppShell
      productName="Wardlock"
      navigation={
        <NavList label="Main">
          <NavItem href="/" current>
            Overview
          </NavItem>
          <NavItem href="/other" onClick={(event) => event.preventDefault()}>
            Other
          </NavItem>
        </NavList>
      }
      account={<p>Signed in</p>}
    >
      <h1>Overview</h1>
    </AppShell>,
  );

describe('AppShell with navigation', () => {
  it('labels the navigation and marks the current page', () => {
    shell();
    const nav = screen.getByRole('navigation', { name: 'Main' });
    expect(nav).toBeTruthy();
    expect(
      screen
        .getByRole('link', { name: 'Overview' })
        .getAttribute('aria-current'),
    ).toBe('page');
    expect(
      screen.getByRole('link', { name: 'Other' }).hasAttribute('aria-current'),
    ).toBe(false);
    expect(screen.getByRole('main')).toBeTruthy();
    expect(screen.getByText('Signed in')).toBeTruthy();
  });

  it('toggles the small-screen menu and closes it when a link is followed', () => {
    shell();
    const menu = screen.getByRole('button', { name: 'Menu' });
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    const panel = document.getElementById(menu.getAttribute('aria-controls')!)!;
    expect(panel.dataset.open).toBe('false');

    fireEvent.click(menu);
    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(menu.textContent).toBe('Close');
    expect(panel.dataset.open).toBe('true');

    fireEvent.click(screen.getByRole('link', { name: 'Other' }));
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(panel.dataset.open).toBe('false');
  });

  it('has no menu button without navigation', () => {
    render(
      <AppShell productName="Wardlock">
        <h1>Overview</h1>
      </AppShell>,
    );
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe('PanelLayout', () => {
  it('frames one task with the wordmark and a main landmark', () => {
    render(
      <PanelLayout productName="Wardlock" homeHref="/sign-in">
        <h1>Sign in</h1>
      </PanelLayout>,
    );
    expect(
      screen.getByRole('link', { name: 'Wardlock' }).getAttribute('href'),
    ).toBe('/sign-in');
    expect(screen.getByRole('main').textContent).toBe('Sign in');
  });
});
