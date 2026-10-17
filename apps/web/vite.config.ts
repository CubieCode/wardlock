// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

// In development wardlockd proxies the dashboard host to this server
// (WARDLOCK_DEV_DASHBOARD_URL), so open the dashboard through wardlockd,
// not this port. scripts/dev.mjs starts both.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '../..', 'WARDLOCK_');
  return {
    plugins: [react()],
    envDir: '../..',
    envPrefix: 'WARDLOCK_PUBLIC_',
    server: {
      host: '127.0.0.1',
      port: Number(env.WARDLOCK_WEB_DEV_PORT || 5173),
      strictPort: true,
      allowedHosts: ['.localhost'],
    },
  };
});
