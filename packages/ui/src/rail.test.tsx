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
import {
  BottomBar,
  Drawer,
  Rail,
  RailButton,
  RailItem,
  RailLayout,
} from './index.js';

afterEach(cleanup);

const places = (onSearch = () => {}) => (
  <>
    <RailItem href="/" icon="◇" label="Overview" current />
    <RailItem href="/places" icon="◆" label="Places" />
    <RailButton
      icon="⌕"
      label="Search"
      shortcut="⌘K"
      aria-keyshortcuts="Meta+K"
      onClick={onSearch}
    />
  </>
);

describe('Rail and BottomBar', () => {
  it('names each place by its label and marks the current one', () => {
    render(
      <Rail label="Main" productName="Product">
        {places()}
      </Rail>,
    );
    const rail = screen.getByRole('navigation', { name: 'Main' });
    const overview = within(rail).getByRole('link', { name: 'Overview' });
    expect(overview.getAttribute('aria-current')).toBe('page');
    expect(
      within(rail)
        .getByRole('link', { name: 'Places' })
        .hasAttribute('aria-current'),
    ).toBe(false);
    // The icon is decorative; the name is the label alone.
    expect(overview.querySelector('[aria-hidden="true"]')?.textContent).toBe(
      '◇',
    );
    expect(
      within(rail).getByRole('link', { name: 'Product' }).getAttribute('href'),
    ).toBe('/');
  });

  it('gives a button its label as the name and announces the shortcut', () => {
    const onSearch = vi.fn();
    render(<BottomBar label="Main">{places(onSearch)}</BottomBar>);
    const bar = screen.getByRole('navigation', { name: 'Main' });
    const search = within(bar).getByRole('button', { name: 'Search' });
    expect(search.getAttribute('type')).toBe('button');
    expect(search.getAttribute('aria-keyshortcuts')).toBe('Meta+K');
    fireEvent.click(search);
    expect(onSearch).toHaveBeenCalledOnce();
  });

  it('puts the rail foot after the places', () => {
    render(
      <Rail
        label="Main"
        productName="Product"
        foot={<RailButton icon="●" label="Account" />}
      >
        {places()}
      </Rail>,
    );
    const names = screen
      .getAllByRole('button')
      .map((button) => button.textContent);
    expect(names).toEqual(['⌕Search⌘K', '●Account']);
  });
});

describe('RailLayout', () => {
  it('has one main landmark and names the navigator column', () => {
    render(
      <RailLayout
        rail={
          <Rail label="Main" productName="Product">
            {places()}
          </Rail>
        }
        bottomBar={<BottomBar label="Main, bottom">{places()}</BottomBar>}
        header={<span>Top bar</span>}
        navigator={<p>Filters</p>}
        navigatorLabel="Place filters"
      >
        <h1>Overview</h1>
      </RailLayout>,
    );
    expect(screen.getAllByRole('main')).toHaveLength(1);
    expect(screen.getByRole('main').textContent).toBe('Overview');
    expect(
      screen.getByRole('complementary', { name: 'Place filters' }).textContent,
    ).toBe('Filters');
    expect(screen.getByRole('banner').textContent).toBe('Top bar');
  });

  it('leaves out the navigator column when there is none', () => {
    render(
      <RailLayout
        rail={<Rail label="Main" productName="Product" children={places()} />}
        bottomBar={null}
      >
        <h1>Overview</h1>
      </RailLayout>,
    );
    expect(screen.queryByRole('complementary')).toBeNull();
    expect(screen.queryByRole('banner')).toBeNull();
  });
});

function DrawerDemo({ onClose = () => {} }: { onClose?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Browse
      </button>
      <Drawer
        open={open}
        title="Filters"
        onClose={() => {
          onClose();
          setOpen(false);
        }}
      >
        <a href="#one" onClick={(event) => event.preventDefault()}>
          One
        </a>
        <p>Some text</p>
      </Drawer>
    </>
  );
}

describe('Drawer', () => {
  it('renders nothing while closed', () => {
    render(<DrawerDemo />);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByText('One')).toBeNull();
  });

  it('is named by its title, moves focus in and returns it on Close', () => {
    render(<DrawerDemo />);
    const opener = screen.getByRole('button', { name: 'Browse' });
    opener.focus();
    fireEvent.click(opener);
    const drawer = screen.getByRole('dialog', { name: 'Filters' });
    expect(drawer.contains(document.activeElement)).toBe(true);
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }));
    expect(screen.queryByText('One')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it('closes on Escape', () => {
    const onClose = vi.fn();
    render(<DrawerDemo onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Browse' }));
    const drawer = screen.getByRole('dialog', { name: 'Filters' });
    act(() => {
      drawer.dispatchEvent(new Event('cancel', { cancelable: true }));
    });
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.queryByText('One')).toBeNull();
  });

  it('closes when a link inside is followed or the backdrop is clicked', () => {
    const onClose = vi.fn();
    render(<DrawerDemo onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Browse' }));
    fireEvent.click(screen.getByRole('link', { name: 'One' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Browse' }));
    // Text inside the drawer is not the backdrop.
    fireEvent.click(screen.getByText('Some text'));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('dialog', { name: 'Filters' }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
