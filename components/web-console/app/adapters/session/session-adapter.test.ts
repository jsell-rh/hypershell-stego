import { describe, expect, it, vi } from "vitest";
import { createSessionAdapter } from "./session-adapter";

describe("session view mapping", () => {
  it("maps public identity and passes the cancellation signal", async () => {
    const session = vi
      .fn()
      .mockResolvedValue({
        authenticated: true,
        roles: ["user"],
        expires_at: 42,
        user: {
          sub: "u",
          name: "User",
          preferred_username: "user",
          email: "user@example.test",
        },
      });
    const signal = new AbortController().signal;
    await expect(
      createSessionAdapter({ session }).getSession(signal),
    ).resolves.toEqual({
      authenticated: true,
      roles: ["user"],
      expiresAt: 42,
      user: {
        sub: "u",
        name: "User",
        preferredUsername: "user",
        email: "user@example.test",
      },
    });
    expect(session).toHaveBeenCalledWith({ signal });
  });
  it("does not convert transport failure into a valid unauthenticated session", async () => {
    const failure = new Error("unavailable");
    const session = vi.fn().mockRejectedValue(failure);
    await expect(createSessionAdapter({ session }).getSession()).rejects.toBe(
      failure,
    );
  });
  it("keeps an unauthenticated session", async () => {
    const session = vi
      .fn()
      .mockResolvedValue({ authenticated: false, roles: [] });
    await expect(
      createSessionAdapter({ session }).getSession(),
    ).resolves.toEqual({ authenticated: false, roles: [] });
  });
});
