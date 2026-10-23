// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import '@wardlock/ui/styles.css';
import './app.css';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app.js';
import { api } from './api.js';
import { applyTheme, readTheme } from './shell/theme.js';

// Before the first paint, so a forced theme never flashes the other one.
applyTheme(readTheme());

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App api={api} />
  </StrictMode>,
);
