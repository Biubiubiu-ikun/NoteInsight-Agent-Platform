import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch, ApiError } from "./client";
import { readSession, writeSession } from "../lib/session";

describe("apiFetch", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("coalesces concurrent refresh requests and retries with the rotated access token", async () => {
    writeSession({
      user: {
        id: 1,
        username: "tester",
        nickname: "Tester",
        avatar_url: "",
        bio: "",
        role: "normal",
        status: "active",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
      access_token: "expired-token",
      expires_in: 1,
    });

    let protectedCalls = 0;
    let refreshCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path.includes("/auth/refresh")) {
        refreshCalls++;
        return jsonResponse({
          user: readSession()!.user,
          access_token: "rotated-token",
          expires_in: 900,
        });
      }
      protectedCalls++;
      const authorization = new Headers(init?.headers).get("Authorization");
      if (authorization === "Bearer expired-token") {
        return jsonResponse({ error: "unauthorized" }, 401);
      }
      expect(authorization).toBe("Bearer rotated-token");
      return jsonResponse({ ok: true });
    });
    vi.stubGlobal("fetch", fetchMock);

    const [first, second] = await Promise.all([
      apiFetch<{ ok: boolean }>("/api/v1/me"),
      apiFetch<{ ok: boolean }>("/api/v1/me"),
    ]);

    expect(first.ok).toBe(true);
    expect(second.ok).toBe(true);
    expect(refreshCalls).toBe(1);
    expect(protectedCalls).toBe(4);
    expect(readSession()?.access_token).toBe("rotated-token");
  });

  it("returns a stable ApiError for non-JSON failures", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("gateway failed", {
      status: 502,
      statusText: "Bad Gateway",
    })));

    await expect(apiFetch("/backend-runtime/ready", {}, false)).rejects.toEqual(
      expect.objectContaining({ status: 502, message: "Bad Gateway" } satisfies Partial<ApiError>),
    );
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
