// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Tab, Tabs } from './index.js';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

/** Lays out a 300px row of 100px tabs, the row scrolled to `scroll`. */
function layout(scroll: number) {
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: Element) {
      if (this.classList.contains('wl-tabs'))
        return DOMRect.fromRect({ x: 0, width: 300, height: 44 });
      const i = [
        ...(this.parentElement?.parentElement?.children ?? []),
      ].indexOf(this.parentElement!);
      return DOMRect.fromRect({ x: i * 100 - scroll, width: 100, height: 44 });
    },
  );
}

const row = (current: number) => (
  <Tabs label="Site">
    {['One', 'Two', 'Three', 'Four', 'Five', 'Six'].map((name, i) => (
      <Tab key={name} href={`/${i}`} current={i === current}>
        {name}
      </Tab>
    ))}
  </Tabs>
);

describe('Tabs', () => {
  it('scrolls the row to centre a current tab that is out of view', () => {
    layout(0);
    render(row(4));
    // Tab 5 spans 400–500; centred in 300px means a scroll of 300.
    expect(screen.getByRole('navigation').scrollLeft).toBe(300);
  });

  it('leaves the row alone while the current tab is in view', () => {
    layout(0);
    render(row(1));
    expect(screen.getByRole('navigation').scrollLeft).toBe(0);
  });
});
