// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@wardlock/apiclient-ts';
import type { StatusTone } from '@wardlock/ui';

export type Webhook = components['schemas']['Webhook'];
export type CreatedWebhook = components['schemas']['CreatedWebhook'];
export type WebhookDelivery = components['schemas']['WebhookDelivery'];
export type WebhookEvent = components['schemas']['WebhookEvent'];

/** At most this many per owner; the server refuses the next with 413. */
export const maxWebhooks = 10;

export const eventChoices: { event: WebhookEvent; hint: string }[] = [
  { event: 'deploy.activated', hint: 'A deploy goes live on a site.' },
  { event: 'preview.deployed', hint: 'A preview is deployed.' },
  { event: 'preview.deleted', hint: 'A preview is deleted.' },
  { event: 'site.created', hint: 'A site is created.' },
  { event: 'site.deleted', hint: 'A site is deleted.' },
  { event: 'domain.status_changed', hint: 'A custom domain changes status.' },
  { event: 'link.created', hint: 'A share link is created.' },
  { event: 'link.revoked', hint: 'A share link is revoked.' },
  { event: 'link.used_up', hint: 'A share link has no uses left.' },
  { event: 'file.withheld', hint: 'Content screening withholds a file.' },
];

/**
 * What a webhook is called on the page: its description, or else the URL's
 * host. Never the whole URL, whose path can be the receiver's secret, so
 * this is what titles, notices and confirmations use.
 */
export function webhookName(webhook: Webhook) {
  if (webhook.description) return webhook.description;
  try {
    return new URL(webhook.url).hostname;
  } catch {
    return 'this endpoint';
  }
}

export function webhookStatus(webhook: Webhook): {
  word: string;
  tone: StatusTone;
} {
  if (webhook.enabled) {
    return webhook.failingSince === undefined
      ? { word: 'Active', tone: 'success' }
      : { word: 'Failing', tone: 'warning' };
  }
  return webhook.disabledReason === 'failing'
    ? { word: 'Turned off after failing', tone: 'danger' }
    : { word: 'Turned off', tone: 'neutral' };
}

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? '' : 's'}`;

export const eventsText = (webhook: Webhook) =>
  webhook.events ? count(webhook.events.length, 'event') : 'All events';

export const sitesText = (webhook: Webhook) =>
  webhook.sites ? count(webhook.sites.length, 'site') : 'All sites';

export function deliveryStatus(delivery: WebhookDelivery): {
  word: string;
  tone: StatusTone;
} {
  switch (delivery.status) {
    case 'succeeded':
      return { word: 'Delivered', tone: 'success' };
    case 'failed':
      return { word: 'Failed', tone: 'danger' };
    case 'dropped':
      return { word: 'Dropped', tone: 'neutral' };
    case 'pending':
      return delivery.attempts > 0
        ? { word: 'Failed, retrying', tone: 'warning' }
        : { word: 'Queued', tone: 'neutral' };
  }
}

const failureText: Record<
  NonNullable<WebhookDelivery['error']>,
  string | undefined
> = {
  status: undefined,
  timeout: 'Timed out',
  dns: 'Host not found',
  tls: 'TLS failed',
  connection: 'Could not connect',
  refused_address: 'Address refused',
};

/** The response code, or why there was none. */
export const deliveryResponse = (delivery: WebhookDelivery) =>
  delivery.responseCode ??
  (delivery.error ? failureText[delivery.error] : undefined) ??
  'None';

export function formatDuration(ms: number | undefined) {
  if (ms === undefined) return 'None';
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}
