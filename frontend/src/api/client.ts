import { readSession, writeSession } from "../lib/session";
import type { ApiErrorBody, AuthResponse } from "../types/api";

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly detail?: string,
  ) {
    super(message);
  }
}

let refreshInFlight: Promise<boolean> | null = null;

async function refreshAccessToken(): Promise<boolean> {
	const response = await fetch("/api/v1/auth/refresh", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		credentials: "same-origin",
		body: JSON.stringify({}),
  });
  if (!response.ok) {
    writeSession(null);
    return false;
  }

	const refreshed = (await response.json()) as AuthResponse;
	writeSession({ user: refreshed.user, access_token: refreshed.access_token, expires_in: refreshed.expires_in });
  return true;
}

async function parseError(response: Response): Promise<ApiError> {
  let body: ApiErrorBody = {};
  try {
    body = (await response.json()) as ApiErrorBody;
  } catch {
    // The status text remains useful for non-JSON runtime endpoints.
  }
  return new ApiError(body.error || response.statusText || "请求失败", response.status, body.detail);
}

export async function apiFetch<T>(
  path: string,
  init: RequestInit = {},
  allowRefresh = true,
): Promise<T> {
  const session = readSession();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (session?.access_token) headers.set("Authorization", `Bearer ${session.access_token}`);

	const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
	if (response.status === 401 && allowRefresh && !path.includes("/auth/")) {
    refreshInFlight ||= refreshAccessToken().finally(() => {
      refreshInFlight = null;
    });
    if (await refreshInFlight) return apiFetch<T>(path, init, false);
  }
  if (!response.ok) throw await parseError(response);
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}
