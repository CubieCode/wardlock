// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';

export type Me = components['schemas']['Me'];

// The client's middleware type, without importing openapi-fetch directly.
type Middleware = Parameters<ApiClient['use']>[0];

export type Session =
  | { status: 'loading' }
  | { status: 'unreachable' }
  /** `reason` says why a signed-in owner is now signed out. */
  | { status: 'signed-out'; reason?: 'expired' | 'signed-out' }
  | { status: 'signed-in'; me: Me };

interface SessionContext {
  session: Session;
  api: ApiClient;
  /** Records the owner a sign-in, setup or link redemption returned. */
  signedIn: (me: Me) => void;
  /** Ends the session on the server. Throws when the server is unreachable. */
  signOut: () => Promise<void>;
  /** Checks the session again, for example after the server was unreachable. */
  refresh: () => void;
}

const Context = createContext<SessionContext | null>(null);

export function useSession() {
  const value = use(Context);
  if (!value) throw new Error('useSession outside SessionProvider');
  return value;
}

/** Asks the server who is signed in. */
async function loadSession(api: ApiClient): Promise<Session> {
  try {
    const { data, response } = await api.GET('/api/v0/me');
    if (data) return { status: 'signed-in', me: data };
    return { status: response.status === 401 ? 'signed-out' : 'unreachable' };
  } catch {
    return { status: 'unreachable' };
  }
}

/**
 * Owns the dashboard session. It asks the server who is signed in on load and
 * whenever the window regains focus, and treats any 401 from an authenticated
 * call as the session having ended (expired, revoked or signed out
 * elsewhere), so every screen falls back to sign-in the same way.
 *
 * Every change of session starts a new epoch. A response only acts on the
 * epoch its request started in, so a slow 401 from before a fresh sign-in
 * cannot end the new session.
 */
export function SessionProvider({
  api,
  children,
}: {
  api: ApiClient;
  children: ReactNode;
}) {
  const [session, setSession] = useState<Session>({ status: 'loading' });
  const epoch = useRef(0);
  const status = useRef<Session['status']>('loading');

  const enter = useCallback((next: Session) => {
    epoch.current += 1;
    status.current = next.status;
    setSession(next);
  }, []);

  useEffect(() => {
    // An owner-only call answering 401 means the session is gone. Sign-in
    // calls under /auth/ answer 401 for other reasons and are left alone.
    const startedIn = new Map<string, number>();
    const middleware: Middleware = {
      onRequest({ id }) {
        startedIn.set(id, epoch.current);
      },
      onResponse({ id, request, response }) {
        const started = startedIn.get(id);
        startedIn.delete(id);
        if (
          response.status === 401 &&
          !request.url.includes('/api/v0/auth/') &&
          started === epoch.current &&
          status.current === 'signed-in'
        )
          enter({ status: 'signed-out', reason: 'expired' });
      },
      onError({ id }) {
        startedIn.delete(id);
      },
    };
    api.use(middleware);
    return () => api.eject(middleware);
  }, [api, enter]);

  useEffect(() => {
    const started = epoch.current;
    void loadSession(api).then((loaded) => {
      if (epoch.current === started) enter(loaded);
    });
  }, [api, enter]);

  const signedInNow = session.status === 'signed-in';
  useEffect(() => {
    if (!signedInNow) return;
    // Returning to the tab fires both focus and visibilitychange; one check
    // at a time is enough. A 401 reaches the middleware above.
    let checking = false;
    const recheck = () => {
      if (checking || document.visibilityState !== 'visible') return;
      checking = true;
      api
        .GET('/api/v0/me')
        .catch(() => undefined)
        .finally(() => {
          checking = false;
        });
    };
    window.addEventListener('focus', recheck);
    document.addEventListener('visibilitychange', recheck);
    return () => {
      window.removeEventListener('focus', recheck);
      document.removeEventListener('visibilitychange', recheck);
    };
  }, [signedInNow, api]);

  const signedIn = useCallback(
    (me: Me) => enter({ status: 'signed-in', me }),
    [enter],
  );

  const signOut = useCallback(async () => {
    const { response } = await api.POST('/api/v0/auth/sign-out');
    // 401: the session had already ended, which is what was asked for.
    if (!response.ok && response.status !== 401)
      throw new Error(`sign-out failed: ${response.status}`);
    enter({ status: 'signed-out', reason: 'signed-out' });
  }, [api, enter]);

  const refresh = useCallback(() => {
    enter({ status: 'loading' });
    const started = epoch.current;
    void loadSession(api).then((loaded) => {
      if (epoch.current === started) enter(loaded);
    });
  }, [api, enter]);

  const value = useMemo(
    () => ({ session, api, signedIn, signOut, refresh }),
    [session, api, signedIn, signOut, refresh],
  );
  return <Context value={value}>{children}</Context>;
}
