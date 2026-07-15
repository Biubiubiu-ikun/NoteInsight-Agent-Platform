import type { AuthResponse } from "../types/api";

export const SESSION_EVENT = "noteinsight:session";

export type StoredSession = Omit<AuthResponse, "refresh_token">;

let currentSession: StoredSession | null = null;

export function readSession(): StoredSession | null {
	return currentSession;
}

export function writeSession(next: StoredSession | null): void {
	currentSession = next;
	window.dispatchEvent(new Event(SESSION_EVENT));
}
