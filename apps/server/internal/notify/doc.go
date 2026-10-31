// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package notify tells owners when something needs them (I6.2) and signs
// events to their webhooks (I6.3). A worker reads what happened from the
// store, writes each event to a durable outbox keyed so it can only be
// queued once, and delivers the outbox with retries. Other packages add to
// the outbox through small interfaces of their own. See
// docs/ARCHITECTURE.md.
package notify
