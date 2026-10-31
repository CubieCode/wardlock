// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  Button,
  Checkbox,
  Dialog,
  Fieldset,
  Notice,
  Radio,
  TextField,
} from '@wardlock/ui';
import { useEffect, useId, useState, type FormEvent } from 'react';
import { failure, unreachable } from '../auth/messages.js';
import { loadAllSiteNames } from './sites.js';
import {
  eventChoices,
  type CreatedWebhook,
  type Webhook,
  type WebhookEvent,
} from './format.js';

type CreateBody = components['schemas']['CreateWebhookRequest'];
type UpdateBody = components['schemas']['UpdateWebhookRequest'];

const fields = ['url', 'description', 'events', 'sites'] as const;
type Field = (typeof fields)[number];
type Errors = Partial<Record<Field, string>>;

const maxDescription = 200;

type SitesState =
  { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; names: string[] };

const sameItems = (a: readonly string[], b: ReadonlySet<string>) =>
  a.length === b.size && a.every((item) => b.has(item));

function chosen<T extends string>(set: ReadonlySet<T>, item: T, on: boolean) {
  const next = new Set(set);
  if (on) next.add(item);
  else next.delete(item);
  return next;
}

/**
 * Adds a webhook, or with `webhook` edits one. Only what changed is sent: a
 * new URL makes the server forget the webhook's failures, and a list emptied
 * by deleted sites stays as it is until the owner changes it.
 */
export function WebhookFormDialog({
  api,
  webhook,
  onClose,
  onCreated,
  onSaved,
}: {
  api: ApiClient;
  webhook?: Webhook;
  onClose: () => void;
  onCreated?: (created: CreatedWebhook) => void;
  onSaved?: (webhook: Webhook) => void;
}) {
  const formId = useId();
  const [url, setUrl] = useState(webhook?.url ?? '');
  const [description, setDescription] = useState(webhook?.description ?? '');
  const [allEvents, setAllEvents] = useState(!webhook?.events);
  const [events, setEvents] = useState<ReadonlySet<WebhookEvent>>(
    () => new Set(webhook?.events),
  );
  const [allSites, setAllSites] = useState(!webhook?.sites);
  const [sites, setSites] = useState<ReadonlySet<string>>(
    () => new Set(webhook?.sites),
  );
  const [siteList, setSiteList] = useState<SitesState>({ kind: 'loading' });
  const [errors, setErrors] = useState<Errors>({});
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Read when the choice is first offered: most webhooks want every site.
  const needSites = !allSites;
  useEffect(() => {
    if (!needSites) return;
    let live = true;
    loadAllSiteNames(api).then(
      (names) => live && setSiteList({ kind: 'ready', names }),
      () => live && setSiteList({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, needSites]);

  const close = () => {
    if (!busy) onClose();
  };

  const eventsChanged = webhook?.events
    ? allEvents || !sameItems(webhook.events, events)
    : !allEvents;
  const sitesChanged = webhook?.sites
    ? allSites || !sameItems(webhook.sites, sites)
    : !allSites;

  /** The field errors that stop the form from being sent. */
  const check = () => {
    const found: Errors = {};
    if (!url.trim()) found.url = 'Enter the address to send events to.';
    if (eventsChanged && !allEvents && events.size === 0)
      found.events = 'Choose at least one event, or All events.';
    if (sitesChanged && !allSites && sites.size === 0)
      found.sites = 'Choose at least one site, or All sites.';
    return found;
  };

  const eventList = () =>
    eventChoices.map((c) => c.event).filter((event) => events.has(event));

  const createBody = (): CreateBody => ({
    url: url.trim(),
    description: description.trim(),
    ...(allEvents ? {} : { events: eventList() }),
    ...(allSites ? {} : { sites: [...sites] }),
  });

  const updateBody = (current: Webhook): UpdateBody => {
    const body: UpdateBody = {};
    if (url.trim() !== current.url) body.url = url.trim();
    if (description.trim() !== current.description)
      body.description = description.trim();
    if (eventsChanged) {
      if (allEvents) body.allEvents = true;
      else body.events = eventList();
    }
    if (sitesChanged) {
      if (allSites) body.allSites = true;
      else body.sites = [...sites];
    }
    return body;
  };

  const refused = (
    result: {
      error?: { target?: string; message?: string; code?: string };
      response: Response;
    } | null,
  ) => {
    const target = fields.find((field) => field === result?.error?.target);
    if (target) return setErrors({ [target]: result?.error?.message });
    // The list of sites is all that can be missing when adding.
    if (result?.response.status === 404 && !webhook)
      return setErrors({ sites: 'One of these sites no longer exists.' });
    if (result?.response.status === 404)
      return setProblem('This webhook or one of its sites no longer exists.');
    if (
      (result?.response.status === 400 || result?.response.status === 413) &&
      result.error?.message
    )
      return setProblem(result.error.message);
    setProblem(result ? failure(result.response) : unreachable);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const found = check();
    setErrors(found);
    setProblem(null);
    if (Object.keys(found).length > 0) return;
    setBusy(true);
    if (webhook) {
      const result = await api
        .PATCH('/api/v0/webhooks/{webhookId}', {
          params: { path: { webhookId: webhook.id } },
          body: updateBody(webhook),
        })
        .catch(() => null);
      setBusy(false);
      if (result?.data) return onSaved?.(result.data);
      return refused(result);
    }
    const result = await api
      .POST('/api/v0/webhooks', { body: createBody() })
      .catch(() => null);
    setBusy(false);
    if (result?.data) return onCreated?.(result.data);
    refused(result);
  };

  const submitLabel = webhook ? 'Save changes' : 'Add webhook';
  return (
    <Dialog
      open
      title={webhook ? 'Edit webhook' : 'Add a webhook'}
      onClose={close}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={busy}>
            {busy ? 'Saving…' : submitLabel}
          </Button>
        </>
      }
    >
      <form id={formId} className="webhook-form" onSubmit={submit} noValidate>
        <TextField
          label="Endpoint URL"
          hint="Wardlock sends a signed POST here. HTTPS only."
          type="url"
          inputMode="url"
          autoComplete="off"
          spellCheck={false}
          placeholder="https://hooks.example.com/wardlock"
          maxLength={2048}
          value={url}
          error={errors.url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <TextField
          label="Description (optional)"
          hint="What it is for, such as “Deploys channel”."
          maxLength={maxDescription}
          autoComplete="off"
          value={description}
          error={errors.description}
          onChange={(e) => setDescription(e.target.value)}
        />
        <Fieldset legend="Events" error={errors.events}>
          <Radio
            label="All events"
            hint="Including any added later."
            name="events"
            checked={allEvents}
            onChange={() => setAllEvents(true)}
          />
          <Radio
            label="Only these events"
            name="events"
            checked={!allEvents}
            onChange={() => setAllEvents(false)}
          />
          {!allEvents && (
            <div className="webhook-choices">
              {eventChoices.map(({ event, hint }) => (
                <Checkbox
                  key={event}
                  label={event}
                  hint={hint}
                  checked={events.has(event)}
                  onChange={(e) =>
                    setEvents(chosen(events, event, e.target.checked))
                  }
                />
              ))}
            </div>
          )}
        </Fieldset>
        <Fieldset legend="Sites" error={errors.sites}>
          <Radio
            label="All sites"
            hint="Including sites you create later."
            name="sites"
            checked={allSites}
            onChange={() => setAllSites(true)}
          />
          <Radio
            label="Only these sites"
            name="sites"
            checked={!allSites}
            onChange={() => setAllSites(false)}
          />
          {!allSites && siteList.kind === 'loading' && (
            <p role="status">Loading your sites…</p>
          )}
          {!allSites && siteList.kind === 'error' && (
            <Notice tone="danger">
              Could not load your sites. Choose All sites, or close this and try
              again.
            </Notice>
          )}
          {!allSites && siteList.kind === 'ready' && (
            <div className="webhook-choices webhook-sites">
              {siteList.names.length === 0 && <p>You have no sites yet.</p>}
              {siteList.names.map((name) => (
                <Checkbox
                  key={name}
                  label={name}
                  checked={sites.has(name)}
                  onChange={(e) =>
                    setSites(chosen(sites, name, e.target.checked))
                  }
                />
              ))}
            </div>
          )}
        </Fieldset>
      </form>
      {problem && <Notice tone="danger">{problem}</Notice>}
    </Dialog>
  );
}
