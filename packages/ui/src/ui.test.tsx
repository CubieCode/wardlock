// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  AppShell,
  Button,
  Card,
  Notice,
  PageHeader,
  StatusBadge,
} from './index.js';

afterEach(cleanup);

describe('components', () => {
  it('renders the shell with one main landmark and heading', () => {
    render(
      <AppShell productName="Wardlock">
        <PageHeader title="Overview" description="Hello" />
        <Card title="Server">
          <StatusBadge tone="success">Connected</StatusBadge>
        </Card>
      </AppShell>,
    );
    expect(screen.getByRole('main')).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(
      'Overview',
    );
    expect(screen.getByRole('region', { name: 'Server' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Wardlock' })).toBeTruthy();
  });

  it('keeps the button type explicit', () => {
    render(
      <Button type="button" variant="primary">
        Save
      </Button>,
    );
    const button = screen.getByRole('button', { name: 'Save' });
    expect(button.getAttribute('type')).toBe('button');
    expect(button.className).toContain('wl-button-primary');
  });

  it('announces danger notices as alerts', () => {
    render(<Notice tone="danger">Could not reach the server.</Notice>);
    expect(screen.getByRole('alert').textContent).toContain('Could not reach');
  });
});
