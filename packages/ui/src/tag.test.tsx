// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  HubLayout,
  SegmentedControl,
  StatusTag,
  Tab,
  TagChip,
  Tabs,
  Ward,
  Wire,
} from './index.js';

afterEach(cleanup);

describe('StatusTag', () => {
  it('shows its word with the tone as a class', () => {
    render(<StatusTag tone="ward">Protected</StatusTag>);
    const tag = screen.getByText('Protected');
    expect(tag.className).toBe('wl-status-tag wl-status-tag-ward');
  });

  it('defaults to neutral', () => {
    render(<StatusTag>Draft</StatusTag>);
    expect(screen.getByText('Draft').className).toContain(
      'wl-status-tag-neutral',
    );
  });
});

describe('TagChip', () => {
  it('draws its colour slot beside the name, hidden from assistive technology', () => {
    render(<TagChip label="docs" slot={3} />);
    const chip = screen.getByText('docs').closest('.wl-tag-chip')!;
    const dot = chip.querySelector('.wl-tag-dot') as HTMLElement;
    expect(dot.getAttribute('aria-hidden')).toBe('true');
    expect(dot.style.background).toBe('var(--color-tag-3)');
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('offers a named remove button when it can be removed', () => {
    const onRemove = vi.fn();
    render(<TagChip label="docs" onRemove={onRemove} />);
    const chip = screen.getByText('docs').closest('.wl-tag-chip')!;
    expect(
      (chip.querySelector('.wl-tag-dot') as HTMLElement).style.background,
    ).toBe('var(--color-tag-0)');
    fireEvent.click(screen.getByRole('button', { name: 'Remove docs' }));
    expect(onRemove).toHaveBeenCalledOnce();
  });
});

describe('TagChip in a narrow space', () => {
  it('keeps the whole name on hover for when it is cut short', () => {
    render(<TagChip label="a-very-long-tag-name-indeed" />);
    expect(
      screen.getByText('a-very-long-tag-name-indeed').getAttribute('title'),
    ).toBe('a-very-long-tag-name-indeed');
  });
});

describe('Ward', () => {
  it('is decorative', () => {
    const { container } = render(<Ward />);
    expect(
      container.querySelector('.wl-ward')?.getAttribute('aria-hidden'),
    ).toBe('true');
  });
});

describe('HubLayout and Wire', () => {
  it('lists wires under the hub, with nested rows and decorative wards', () => {
    render(
      <HubLayout hub={<b>server</b>} label="Groups">
        <Wire ward>
          <h2>Docs</h2>
          <HubLayout label="Docs rows">
            <Wire ward>alpha</Wire>
            <Wire tone="public">beta</Wire>
          </HubLayout>
        </Wire>
        <Wire>No group</Wire>
      </HubLayout>,
    );
    const groups = screen.getByRole('list', { name: 'Groups' });
    expect(within(groups).getAllByRole('listitem')).toHaveLength(4);
    const rows = screen.getByRole('list', { name: 'Docs rows' });
    const [alpha, beta] = within(rows).getAllByRole('listitem');
    expect(alpha!.querySelector('.wl-ward')?.getAttribute('aria-hidden')).toBe(
      'true',
    );
    expect(beta!.className).toContain('wl-wire-public');
    expect(beta!.querySelector('.wl-ward')).toBeNull();
    expect(screen.getByText('server').closest('.wl-hub-node')).toBeTruthy();
  });

  it('draws no hub node when none is given', () => {
    const { container } = render(
      <HubLayout>
        <Wire>row</Wire>
      </HubLayout>,
    );
    expect(container.querySelector('.wl-hub-node')).toBeNull();
  });
});

describe('Tabs', () => {
  it('is a labelled navigation of links, the current one marked, with counts', () => {
    render(
      <Tabs label="Sections">
        <Tab href="/a" current>
          Overview
        </Tab>
        <Tab href="/a/links" count={4}>
          Links
        </Tab>
      </Tabs>,
    );
    const nav = screen.getByRole('navigation', { name: 'Sections' });
    const current = within(nav).getByRole('link', { name: 'Overview' });
    expect(current.getAttribute('aria-current')).toBe('page');
    const links = within(nav).getByRole('link', { name: 'Links 4' });
    expect(links.getAttribute('aria-current')).toBeNull();
    expect(links.getAttribute('href')).toBe('/a/links');
  });
});

describe('SegmentedControl', () => {
  function Range() {
    const [value, setValue] = useState<'7' | '30' | '90'>('7');
    return (
      <SegmentedControl
        label="Range"
        value={value}
        onChange={setValue}
        options={[
          { value: '7', label: '7 days' },
          { value: '30', label: '30 days' },
          { value: '90', label: '90 days' },
        ]}
      />
    );
  }

  it('is a named group of radios with one checked', () => {
    render(<Range />);
    const group = screen.getByRole('group', { name: 'Range' });
    const radios = within(group).getAllByRole('radio');
    expect(radios).toHaveLength(3);
    expect(
      (screen.getByRole('radio', { name: '7 days' }) as HTMLInputElement)
        .checked,
    ).toBe(true);
    // The radios share one name, so arrow keys move the choice natively.
    expect(new Set(radios.map((r) => r.getAttribute('name'))).size).toBe(1);
  });

  it('reports a new choice', () => {
    render(<Range />);
    fireEvent.click(screen.getByRole('radio', { name: '30 days' }));
    expect(
      (screen.getByRole('radio', { name: '30 days' }) as HTMLInputElement)
        .checked,
    ).toBe(true);
    expect(
      (screen.getByRole('radio', { name: '7 days' }) as HTMLInputElement)
        .checked,
    ).toBe(false);
  });
});
