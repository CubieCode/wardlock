// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { failure, unreachable } from '../auth/messages.js';
import type { TeamRole } from './scope.js';

export const roleNames: Record<TeamRole, string> = {
  admin: 'Admin',
  editor: 'Editor',
  viewer: 'Viewer',
};

/** What each role may do, in the order they are offered. */
export const roles: { role: TeamRole; hint: string }[] = [
  { role: 'viewer', hint: 'Reads everything, changes nothing' },
  { role: 'editor', hint: 'Deploys and changes the team’s sites' },
  { role: 'admin', hint: 'Also members, tokens and the team itself' },
];

const refusals: Record<string, string> = {
  already_member:
    'That address is already a member. Change their role in the list instead.',
  forbidden_role:
    'Your role in this team no longer allows this. Reload the page to see what you can do.',
  invites_restricted:
    'That address has no account here, and only the operator of this server can invite new owners.',
  site_unavailable:
    'This site cannot be moved while it is taken down. Ask the operator of this server about it.',
  team_has_sites:
    'This team still has sites. Move or tear them down first, then delete it.',
  team_needs_admin:
    'A team must keep an admin who is not suspended. Make someone else an admin first.',
  team_suspended:
    'The operator of this server has suspended this team, so it takes no new sites, members or tokens and cannot be deleted.',
};

/** What an apiclient call answers, as far as these messages need it. */
interface Answer {
  error?: { code: string; message: string; target?: string | undefined };
  response: Response;
}

/**
 * A refused call in words: the team rule it broke, the server's own message
 * for anything else it explains, otherwise what to do next. Null is a call
 * that never reached the server.
 */
export function teamRefusal(answer: Answer | null) {
  if (!answer) return unreachable;
  const { error, response } = answer;
  if (error && error.code in refusals) return refusals[error.code]!;
  if (response.status === 404)
    return 'This is no longer here. Reload the page to see what is.';
  if (error?.message && [400, 409, 413].includes(response.status))
    return error.message;
  return failure(response);
}
