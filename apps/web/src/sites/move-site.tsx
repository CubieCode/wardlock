// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Dialog, Notice, SelectField } from '@wardlock/ui';
import { useState } from 'react';
import { teamRefusal } from '../teams/messages.js';
import { useScope, useSiteAccess } from '../teams/scope.js';
import type { Site } from './format.js';

/** Where a site can go: `personal`, or a team's ID. */
const personal = 'personal';

/**
 * Moves the site to or from a team. Needs its owner or an admin of its team,
 * and admin of the team it goes to, so the card is left out for anyone with
 * nowhere to move it. A site only ever lands in the caller's own account,
 * never someone else's.
 */
export function MoveSiteCard({
  api,
  site,
  onMoved,
}: {
  api: ApiClient;
  site: Site;
  /** The site as it is now, and the name of where it went. */
  onMoved: (site: Site, to: string) => void;
}) {
  const { teams } = useScope();
  const { admin } = useSiteAccess(site);
  const [moving, setMoving] = useState(false);

  const targets = [
    ...(site.owner.kind === 'team'
      ? [{ value: personal, name: 'Your personal account' }]
      : []),
    ...(teams.kind === 'ready' ? teams.teams : [])
      .filter((team) => team.role === 'admin' && team.id !== site.owner.id)
      .map((team) => ({ value: team.id, name: team.name })),
  ];
  if (!admin || targets.length === 0) return null;

  return (
    <Card title="Move this site">
      {site.unavailable ? (
        <p>
          A site the operator has taken down cannot be moved. Ask the operator
          of this server about it.
        </p>
      ) : (
        <>
          <p>
            Hand this site to a team, or take it back. Its deploys, links,
            domains and analytics go with it, and its address stays the same.
          </p>
          <div>
            <Button type="button" onClick={() => setMoving(true)}>
              Move site
            </Button>
          </div>
        </>
      )}
      {moving ? (
        <MoveSiteDialog
          api={api}
          site={site}
          targets={targets}
          onClose={() => setMoving(false)}
          onMoved={onMoved}
        />
      ) : null}
    </Card>
  );
}

function MoveSiteDialog({
  api,
  site,
  targets,
  onClose,
  onMoved,
}: {
  api: ApiClient;
  site: Site;
  targets: { value: string; name: string }[];
  onClose: () => void;
  onMoved: (site: Site, to: string) => void;
}) {
  const [target, setTarget] = useState(targets[0]!.value);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const destination = targets.find((t) => t.value === target)!;

  const move = async () => {
    setBusy(true);
    setProblem(null);
    const result = await api
      .POST('/api/v0/sites/{site}/transfer', {
        params: { path: { site: site.name } },
        body: {
          to:
            target === personal
              ? { kind: 'owner' }
              : { kind: 'team', id: target },
        },
      })
      .catch(() => null);
    setBusy(false);
    if (result?.data) {
      onClose();
      return onMoved(result.data, destination.name);
    }
    setProblem(teamRefusal(result));
  };

  return (
    <Dialog
      open
      title={`Move ${site.name}?`}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
            disabled={busy}
            onClick={() => void move()}
          >
            {busy ? 'Moving…' : 'Move site'}
          </Button>
        </>
      }
    >
      <SelectField
        label="Move to"
        value={target}
        disabled={busy}
        onChange={(event) => setTarget(event.target.value)}
      >
        {targets.map((t) => (
          <option key={t.value} value={t.value}>
            {t.name}
          </option>
        ))}
      </SelectField>
      <ul className="site-move-list">
        <li>
          {site.owner.kind === 'team'
            ? `People in ${site.owner.name} lose access unless they are also in ${destination.name}.`
            : `Everyone in ${destination.name} gets access, as far as their role allows.`}
        </li>
        <li>
          Its group and tags are cleared: they belong to{' '}
          {site.owner.kind === 'team' ? site.owner.name : 'your account'}.
        </li>
        <li>It counts towards the quotas of {destination.name} from now on.</li>
      </ul>
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
    </Dialog>
  );
}
