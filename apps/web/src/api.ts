// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// The dashboard's only route to the server. Screens receive this client
// (or a test double); they never call fetch directly.
import { createApiClient } from '@wardlock/apiclient-ts';

export const api = createApiClient();
