import { describe, it, expect, vi, afterEach } from "vitest";
import { api, APIError, UnauthorizedError, setUnauthorizedHandler } from "./api";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  setUnauthorizedHandler(null);
  vi.unstubAllGlobals();
});

// A 401 IS NOT ONE EVENT. The client collapsed two server answers by throwing a hard-coded error
// before reading the body, which is the root of quince#356: a lost session never reached the login
// screen, and a mistyped password reported a session problem.
describe("a 401 that means the session is gone", () => {
  it("notifies the app so the route guard can redirect", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(401, { error: { code: "unauthorized", message: "authentication required" } }),
      ),
    );

    await expect(api.get("/api/devices")).rejects.toBeInstanceOf(UnauthorizedError);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  // The text endpoint is a separate throw site and had the same hard-coded error. A fix that healed
  // only request<T> would leave the job-log fetch silently swallowing an expired session.
  it("notifies from the text endpoint too", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(401, { error: { code: "unauthorized", message: "authentication required" } }),
      ),
    );

    await expect(api.getText("/api/jobs/J1/log")).rejects.toBeInstanceOf(UnauthorizedError);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});

// THE ACCEPTANCE THE RULING NAMED. A redirect-shaped fix would fire here too, bouncing the user to
// the login page they are already looking at while still showing them the wrong reason.
describe("a 401 that means the password was wrong", () => {
  it("does NOT notify, so nothing navigates away from the login form", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(401, { error: { code: "bad_password", message: "incorrect password" } }),
      ),
    );

    await expect(api.post("/api/auth/login", { password: "nope" })).rejects.toBeInstanceOf(
      UnauthorizedError,
    );
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  // The message the user reads. Hard-coding it at the throw site is what made the login form say
  // "authentication required" when the server had said "incorrect password".
  it("carries the SERVER's code and message, not a client-side constant", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(401, { error: { code: "bad_password", message: "incorrect password" } }),
      ),
    );

    const err = await api.post("/api/auth/login", { password: "nope" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(UnauthorizedError);
    expect((err as APIError).code).toBe("bad_password");
    expect((err as APIError).message).toBe("incorrect password");
    expect((err as APIError).message).not.toBe("authentication required");
  });

  it("treats setup the same way — it is the other endpoint reachable without a session", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(401, { error: { code: "bad_password", message: "no" } })),
    );

    await api.post("/api/auth/setup", { password: "x" }).catch(() => undefined);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});

// The fallbacks matter because the 401 branch now parses a body it previously ignored: a response
// with no usable envelope must still produce the honest generic error rather than throwing inside
// the error path.
describe("a 401 with an unusable body", () => {
  it("falls back to the generic session message and still notifies", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("<html>gateway</html>", { status: 401 })),
    );

    const err = await api.get("/api/devices").catch((e: unknown) => e);
    expect((err as APIError).code).toBe("unauthorized");
    expect((err as APIError).message).toBe("authentication required");
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});

// Regression guard for the refactor itself: non-401 errors kept parsing the envelope, and the shared
// helper must not have changed them.
describe("non-401 errors are unchanged", () => {
  it("still carries the envelope and does not notify", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(409, { error: { code: "already_running", message: "a backup is running" } }),
      ),
    );

    const err = await api.post("/api/jobs", {}).catch((e: unknown) => e);
    expect((err as APIError).status).toBe(409);
    expect((err as APIError).code).toBe("already_running");
    expect((err as APIError).message).toBe("a backup is running");
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
