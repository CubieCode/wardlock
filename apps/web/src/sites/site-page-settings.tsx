// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, SelectField } from '@wardlock/ui';
import { useState } from 'react';
import type { Site } from './format.js';
import { TagChips, TagPicker, type Organise } from './site-page-organise.js';
import { TeardownDialog } from './teardown.js';

/** The Settings tab: the site's group and tags, and teardown. */
export function SettingsTab({
  api,
  site,
  organise,
}: {
  api: ApiClient;
  site: Site;
  organise: Organise;
}) {
  const [tearingDown, setTearingDown] = useState(false);
  const { state } = organise;

  return (
    <div className="site-settings">
      <Card title="Group and tags">
        <p className="site-card-lead">
          Only you see groups and tags. They change nothing visitors see.
        </p>
        {state.kind === 'loading' && (
          <p role="status">Loading groups and tags…</p>
        )}
        {state.kind === 'error' && (
          <p className="site-muted">
            Could not load groups and tags. Reload them from the header above.
          </p>
        )}
        {state.kind === 'ready' && (
          <>
            <SelectField
              label="Group"
              hint={
                state.groups.length === 0
                  ? 'You have no groups yet.'
                  : 'A site is in one group at most.'
              }
              value={site.groupId ?? ''}
              disabled={organise.busy}
              onChange={(event) =>
                organise.setGroup(event.target.value || undefined)
              }
            >
              <option value="">No group</option>
              {state.groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
            </SelectField>
            <div
              className="site-organisation"
              role="group"
              aria-label="Tags"
              aria-busy={organise.busy}
            >
              {site.tagIds.length === 0 && (
                <span className="site-muted">No tags</span>
              )}
              <TagChips site={site} tags={state.tags} organise={organise} />
              <TagPicker site={site} tags={state.tags} organise={organise} />
            </div>
          </>
        )}
      </Card>

      <Card title="Tear down this site">
        {site.unavailable ? (
          <p>
            A site the operator has taken down cannot be torn down here. Ask the
            operator of this server about it.
          </p>
        ) : (
          <>
            <p>
              Deletes the site with its deploys and access settings, stops
              serving <code>{site.hostname}</code> and frees the name{' '}
              <strong>{site.name}</strong>. This cannot be undone.
            </p>
            <div>
              <Button
                type="button"
                variant="danger"
                onClick={() => setTearingDown(true)}
              >
                Tear down site
              </Button>
            </div>
          </>
        )}
      </Card>
      <TeardownDialog
        api={api}
        site={site}
        open={tearingDown}
        onClose={() => setTearingDown(false)}
      />
    </div>
  );
}
