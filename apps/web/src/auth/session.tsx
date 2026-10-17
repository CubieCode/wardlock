// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient, components } from '@wardlock/apiclient-ts';
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

export type Me = components['schemas']['Me'];

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
 */
export function SessionProvider({
  api,
  children,
}: {
  api: ApiClient;
  children: ReactNode;
}) {
  const [session, setSession] = useState<Session>({ status: 'loading' });

  useEffect(() => {
    // An owner-only call answering 401 means the session is gone. Sign-in
    // calls under /auth/ answer 401 for other reasons and are left alone.
    const middleware = {
      onResponse({
        request,
        response,
      }: {
        request: Request;
        response: Response;
      }) {
        if (response.status === 401 && !request.url.includes('/api/v0/auth/'))
          setSession((current) =>
            current.status === 'signed-in'
              ? { status: 'signed-out', reason: 'expired' }
              : current,
          );
        return undefined;
      },
    };
    api.use(middleware);
    return () => api.eject(middleware);
  }, [api]);

  useEffect(() => {
    let live = true;
    void loadSession(api).then((loaded) => {
      if (live) setSession(loaded);
    });
    return () => {
      live = false;
    };
  }, [api]);

  const signedInNow = session.status === 'signed-in';
  useEffect(() => {
    if (!signedInNow) return;
    // A 401 here reaches the middleware above, which marks it expired.
    const recheck = () => {
      if (document.visibilityState === 'visible')
        api.GET('/api/v0/me').catch(() => undefined);
    };
    window.addEventListener('focus', recheck);
    document.addEventListener('visibilitychange', recheck);
    return () => {
      window.removeEventListener('focus', recheck);
      document.removeEventListener('visibilitychange', recheck);
    };
  }, [signedInNow, api]);

  const signedIn = useCallback(
    (me: Me) => setSession({ status: 'signed-in', me }),
    [],
  );

  const signOut = useCallback(async () => {
    const { response } = await api.POST('/api/v0/auth/sign-out');
    // 401: the session had already ended, which is what was asked for.
    if (!response.ok && response.status !== 401)
      throw new Error(`sign-out failed: ${response.status}`);
    setSession({ status: 'signed-out', reason: 'signed-out' });
  }, [api]);

  const refresh = useCallback(() => {
    setSession({ status: 'loading' });
    void loadSession(api).then(setSession);
  }, [api]);

  const value = useMemo(
    () => ({ session, api, signedIn, signOut, refresh }),
    [session, api, signedIn, signOut, refresh],
  );
  return <Context value={value}>{children}</Context>;
}
