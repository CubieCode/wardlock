// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import {
  Button,
  EmptyState,
  StatusBadge,
  Table,
  type TableColumn,
} from '@wardlock/ui';
import { relativeTime, Time } from '../sites/format.js';
import {
  deliveryResponse,
  deliveryStatus,
  formatDuration,
  type WebhookDelivery,
} from './format.js';

/**
 * A webhook's recent deliveries, newest first. `canRedeliver` is false while
 * the webhook is off, when the server would refuse.
 */
export function DeliveriesTable({
  deliveries,
  canRedeliver,
  redelivering,
  onRedeliver,
}: {
  deliveries: WebhookDelivery[];
  canRedeliver: boolean;
  /** The delivery being sent again, if any. */
  redelivering: string | null;
  onRedeliver: (delivery: WebhookDelivery) => void;
}) {
  if (deliveries.length === 0)
    return (
      <EmptyState title="No deliveries yet">
        <p>Send a test, or wait for an event this webhook wants.</p>
      </EmptyState>
    );

  const columns: TableColumn<WebhookDelivery>[] = [
    {
      header: 'Event',
      cell: (delivery) => (
        <span className="webhook-endpoint">
          <code>{delivery.type}</code>
          {delivery.redeliveryOf && (
            <span className="webhook-details">Redelivery</span>
          )}
        </span>
      ),
    },
    {
      header: 'Status',
      nowrap: true,
      cell: (delivery) => {
        const { word, tone } = deliveryStatus(delivery);
        return <StatusBadge tone={tone}>{word}</StatusBadge>;
      },
    },
    { header: 'Response', nowrap: true, cell: deliveryResponse },
    {
      header: 'Duration',
      nowrap: true,
      cell: (delivery) => formatDuration(delivery.durationMs),
    },
    {
      header: 'Attempts',
      nowrap: true,
      cell: (delivery) => delivery.attempts,
    },
    {
      header: 'Sent',
      nowrap: true,
      cell: (delivery) => <Time at={delivery.createdAt} />,
    },
    {
      header: 'Actions',
      cell: (delivery) => (
        <Button
          type="button"
          aria-label={`Redeliver ${delivery.type} from ${relativeTime(delivery.createdAt)}`}
          disabled={!canRedeliver || redelivering !== null}
          onClick={() => onRedeliver(delivery)}
        >
          {redelivering === delivery.id ? 'Sending…' : 'Redeliver'}
        </Button>
      ),
    },
  ];

  return (
    <Table
      caption="Recent deliveries, newest first"
      columns={columns}
      rows={deliveries}
      rowKey={(delivery) => delivery.id}
    />
  );
}
