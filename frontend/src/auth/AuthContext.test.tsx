import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as client from "../api/client";
import type { AuthResponse, User } from "../types/api";
import { AuthProvider, useAuth } from "./AuthContext";

describe("AuthProvider", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("refreshes, logs in, updates the profile, and clears logout state", async () => {
    const user = authUser();
    const api = vi.spyOn(client, "apiFetch").mockImplementation(async (path) => {
      if (path === "/api/v1/me") return { ...user, nickname: "Updated" } as User;
      if (path === "/api/v1/auth/logout") return { status: "logged_out" };
      return { user, access_token: "access", expires_in: 900 } as AuthResponse;
    });
    render(<AuthProvider><AuthProbe /></AuthProvider>);

    await waitFor(() => expect(screen.getByText("ready:true")).toBeInTheDocument());
    expect(screen.getByText("user:auth_user")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "login" }));
    await waitFor(() => expect(api).toHaveBeenCalledWith("/api/v1/auth/login", expect.anything()));
    fireEvent.click(screen.getByRole("button", { name: "update" }));
    await waitFor(() => expect(screen.getByText("user:Updated")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "logout" }));
    await waitFor(() => expect(screen.getByText("user:none")).toBeInTheDocument());
  });
});

function AuthProbe() {
  const auth = useAuth();
  return <div>
    <span>ready:{String(auth.ready)}</span>
    <span>user:{auth.user?.nickname || auth.user?.username || "none"}</span>
    <button onClick={() => auth.login("auth_user", "password_123")}>login</button>
    <button onClick={() => auth.updateProfile({ nickname: "Updated" })}>update</button>
    <button onClick={() => auth.logout()}>logout</button>
  </div>;
}

function authUser(): User {
  return {
    id: 5,
    username: "auth_user",
    nickname: "",
    avatar_url: "",
    bio: "",
    role: "normal",
    status: "active",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}
