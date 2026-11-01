// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import { Button, Card, Dialog, Notice, TextField } from '@wardlock/ui';
import { useId, useState, type FormEvent } from 'react';
import { navigate } from '../shell/router.js';
import { setFlash } from '../sites/flash.js';
import { teamRefusal } from './messages.js';
import { ConfirmDialog } from './members.js';
import { useScope, type Team } from './scope.js';

/** Leaving the team, for everyone, and deleting it, for its admins. */
export function DangerCards({
  api,
  team,
  admin,
}: {
  api: ApiClient;
  team: Team;
  admin: boolean;
}) {
  const { ownerId, refresh } = useScope();
  const [leaving, setLeaving] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const leave = async () => {
    const result = await api
      .DELETE('/api/v0/teams/{teamId}/members/{ownerId}', {
        params: { path: { teamId: team.id, ownerId } },
      })
      .catch(() => null);
    if (result?.response.ok || result?.response.status === 404) {
      await refresh();
      setFlash(`You left ${team.name}.`);
      navigate('/sites');
      return null;
    }
    return teamRefusal(result);
  };

  return (
    <>
      <Card title="Leave this team">
        <p>
          You lose access to its sites at once. What you made stays with the
          team.
        </p>
        <div>
          <Button type="button" onClick={() => setLeaving(true)}>
            Leave team
          </Button>
        </div>
      </Card>
      {admin ? (
        <Card title="Delete this team">
          <p>
            Deletes the team with its members, invitations, tokens, groups and
            tags. A team with sites cannot be deleted: move or tear them down
            first. This cannot be undone.
          </p>
          <div>
            <Button
              type="button"
              variant="danger"
              onClick={() => setDeleting(true)}
            >
              Delete team
            </Button>
          </div>
        </Card>
      ) : null}
      {leaving ? (
        <ConfirmDialog
          title={`Leave ${team.name}?`}
          action="Leave team"
          busyLabel="Leaving…"
          onClose={() => setLeaving(false)}
          onConfirm={leave}
        >
          <p>
            You lose access to the team’s sites straight away, and only an admin
            can invite you back.
          </p>
        </ConfirmDialog>
      ) : null}
      {deleting ? (
        <DeleteTeamDialog
          api={api}
          team={team}
          onClose={() => setDeleting(false)}
        />
      ) : null}
    </>
  );
}

/** Asks for the team's name, as tearing down a site asks for the site's. */
function DeleteTeamDialog({
  api,
  team,
  onClose,
}: {
  api: ApiClient;
  team: Team;
  onClose: () => void;
}) {
  const { refresh } = useScope();
  const formId = useId();
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const remove = async (event: FormEvent) => {
    event.preventDefault();
    if (typed !== team.name) return;
    setBusy(true);
    setProblem(null);
    const result = await api
      .DELETE('/api/v0/teams/{teamId}', {
        params: { path: { teamId: team.id } },
      })
      .catch(() => null);
    // 404: it is already gone, which is what was asked for.
    if (result?.response.ok || result?.response.status === 404) {
      await refresh();
      setFlash(`${team.name} was deleted.`);
      navigate('/sites');
      return;
    }
    setBusy(false);
    setProblem(teamRefusal(result));
  };

  return (
    <Dialog
      open
      title={`Delete ${team.name}?`}
      onClose={() => {
        if (!busy) onClose();
      }}
      actions={
        <>
          <Button type="button" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            form={formId}
            variant="danger"
            disabled={busy || typed !== team.name}
          >
            {busy ? 'Deleting…' : 'Delete team'}
          </Button>
        </>
      }
    >
      <form id={formId} className="team-form" onSubmit={remove} noValidate>
        <p>
          Everyone loses access to the team, and its tokens stop working. This
          cannot be undone.
        </p>
        <TextField
          label={`Type ${team.name} to confirm`}
          autoComplete="off"
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
        />
      </form>
      {problem ? <Notice tone="danger">{problem}</Notice> : null}
    </Dialog>
  );
}
