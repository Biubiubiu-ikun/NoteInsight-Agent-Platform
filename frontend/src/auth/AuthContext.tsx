import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../api/client";
import { readSession, SESSION_EVENT, writeSession, type StoredSession } from "../lib/session";
import type { AuthResponse, User } from "../types/api";

interface AuthContextValue {
  user: User | null;
  ready: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, password: string, nickname: string) => Promise<void>;
  logout: () => Promise<void>;
  updateProfile: (input: { nickname?: string; bio?: string; avatar_url?: string }) => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

function persistAuth(result: AuthResponse): void {
	writeSession({ user: result.user, access_token: result.access_token, expires_in: result.expires_in });
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [session, setSession] = useState<StoredSession | null>(() => readSession());
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const sync = () => setSession(readSession());
    window.addEventListener(SESSION_EVENT, sync);
    return () => window.removeEventListener(SESSION_EVENT, sync);
  }, []);

	useEffect(() => {
		let active = true;
		apiFetch<AuthResponse>("/api/v1/auth/refresh", { method: "POST", body: JSON.stringify({}) }, false)
			.then((result) => { if (active) persistAuth(result); })
			.catch(() => { if (active) writeSession(null); })
			.finally(() => { if (active) setReady(true); });
		return () => { active = false; };
	}, []);

  const login = useCallback(async (username: string, password: string) => {
    const result = await apiFetch<AuthResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
    persistAuth(result);
  }, []);

  const register = useCallback(async (username: string, password: string, nickname: string) => {
    const result = await apiFetch<AuthResponse>("/api/v1/auth/register", {
      method: "POST",
      body: JSON.stringify({ username, password, nickname }),
    });
    persistAuth(result);
  }, []);

	const logout = useCallback(async () => {
		try {
			await apiFetch<{ status: string }>("/api/v1/auth/logout", {
				method: "POST",
				body: JSON.stringify({}),
			});
    } finally {
      writeSession(null);
    }
  }, []);

  const updateProfile = useCallback(async (input: { nickname?: string; bio?: string; avatar_url?: string }) => {
    const user = await apiFetch<User>("/api/v1/me", {
      method: "PATCH",
      body: JSON.stringify(input),
    });
    const current = readSession();
    if (current) writeSession({ ...current, user });
  }, []);

  const value = useMemo(
    () => ({ user: session?.user ?? null, ready, login, register, logout, updateProfile }),
    [session, ready, login, register, logout, updateProfile],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
