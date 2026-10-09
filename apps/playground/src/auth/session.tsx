import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { api, ApiError, type Session } from '../api/client';

type SessionState = {
  session: Session | null;
  /** False until the first `/auth/me` answer (or failure). */
  ready: boolean;
  /** False when the API cannot be reached; the playground then works locally only. */
  apiAvailable: boolean;
  refresh: () => Promise<void>;
  setSession: (s: Session | null) => void;
  logout: () => Promise<void>;
};

const SessionContext = createContext<SessionState | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null);
  const [ready, setReady] = useState(false);
  const [apiAvailable, setApiAvailable] = useState(true);

  const refresh = useCallback(async () => {
    try {
      setSession(await api.me());
      setApiAvailable(true);
    } catch (e) {
      setSession(null);
      setApiAvailable(!(e instanceof ApiError && e.status === 0));
    } finally {
      setReady(true);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const logout = useCallback(async () => {
    await api.logout().catch(() => undefined);
    setSession(null);
  }, []);

  const value = useMemo(
    () => ({ session, ready, apiAvailable, refresh, setSession, logout }),
    [session, ready, apiAvailable, refresh, logout],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionState {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error('useSession must be used inside <SessionProvider>');
  return ctx;
}
