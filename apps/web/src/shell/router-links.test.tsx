// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { useLinkInterception } from './router.js';

function Links() {
  useLinkInterception();
  return (
    <>
      <a href="/sites/docs">HTML link</a>
      <svg>
        <a href="/sites/docs?view=map" aria-label="SVG link">
          <circle r="4" />
        </a>
        <a href="/sites/away" target="_blank" aria-label="New tab">
          <circle r="4" />
        </a>
      </svg>
    </>
  );
}

afterEach(() => cleanup());

describe('link interception', () => {
  it.each([
    ['HTML link', '/sites/docs'],
    ['SVG link', '/sites/docs?view=map'],
  ])('keeps a click on an %s in the page', (name, want) => {
    window.history.replaceState(null, '', '/');
    render(<Links />);
    const link = screen.getByRole('link', { name });
    // The child, as a pointer on the dot would hit.
    const target = link.firstElementChild ?? link;
    const handled = !fireEvent.click(target);
    expect(handled).toBe(true);
    expect(window.location.pathname + window.location.search).toBe(want);
  });

  it('leaves a link with a target to the browser', () => {
    window.history.replaceState(null, '', '/');
    render(<Links />);
    const handled = !fireEvent.click(
      screen.getByRole('link', { name: 'New tab' }).firstElementChild!,
    );
    expect(handled).toBe(false);
    expect(window.location.pathname).toBe('/');
  });
});
