import { readCSRFToken } from "./csrf";

// APIError carries the parsed {error:{code,message}} envelope; `details` holds the full
// body for richer errors (e.g. the 422 {errors:[...]} from PUT /api/config).
export class APIError extends Error {
  status: number;
  code: string;
  details: unknown;
  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

// UnauthorizedError is thrown on any 401. It carries the SERVER's code and message rather than a
// fixed string: a 401 is not one thing. An expired session is `unauthorized`/"authentication
// required" from the guard middleware; a wrong password at the login form is
// `bad_password`/"incorrect password". Hard-coding the first meant the login form told users their
// password was fine and their session was not (quince#356).
export class UnauthorizedError extends APIError {
  constructor(code = "unauthorized", message = "authentication required", details?: unknown) {
    super(401, code, message, details);
    this.name = "UnauthorizedError";
  }
}

// onUnauthorized is called when a 401 means THE SESSION IS GONE — not when a credential a user just
// typed was wrong. The app wires it at boot to re-read auth status, which drops the route guard to
// the login screen (main.tsx).
//
// A CALLBACK rather than an import, for two reasons. `lib/auth.ts` imports this module, so importing
// it back is a cycle; and the API client has no business knowing about react-query or the router. It
// reports the fact and lets the app decide what that means.
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn;
}

// notifyUnauthorized reports a lost session discovered somewhere OTHER than an API response — today
// the WebSocket handshake, which the browser rejects before script can read its 401 (quince#374).
// It exists so there is still exactly ONE place that decides what a lost session means: the caller
// reports the fact, `main.tsx` owns the consequence. A second redirect path would be a second thing
// to keep in step with this one.
export function notifyUnauthorized(): void {
  onUnauthorized?.();
}

// The two endpoints where a 401 is about the CREDENTIAL IN THE REQUEST, not about the session: they
// are the ones you can reach without a session at all (the server's authExempt set). A 401 here means
// "that password was wrong", and dropping to the login screen because you are already on it — or
// worse, wiping a half-typed setup — would be nonsense.
const CREDENTIAL_ENDPOINTS = new Set(["/api/auth/login", "/api/auth/setup"]);

type Method = "GET" | "POST" | "PUT" | "DELETE";

// parseErrorBody pulls {error:{code,message}} out of a response, falling back honestly when the body
// is absent or not JSON. Shared by the 401 branch and the general error branch so the two cannot
// drift — the 401 path skipping this is exactly what discarded the server's words.
async function parseErrorBody(
  resp: Response,
  fallbackCode: string,
  fallbackMessage: string,
): Promise<{ code: string; message: string; details: unknown }> {
  let code = fallbackCode;
  let message = fallbackMessage;
  let details: unknown;
  try {
    const parsed: unknown = await resp.json();
    details = parsed;
    if (parsed && typeof parsed === "object" && "error" in parsed) {
      const err = (parsed as { error?: { code?: string; message?: string } }).error;
      if (err?.code) code = err.code;
      if (err?.message) message = err.message;
    }
  } catch {
    // non-JSON or empty error body; keep the fallbacks
  }
  return { code, message, details };
}

async function unauthorized(resp: Response, path: string): Promise<UnauthorizedError> {
  const { code, message, details } = await parseErrorBody(resp, "unauthorized", "authentication required");
  if (!CREDENTIAL_ENDPOINTS.has(path)) onUnauthorized?.();
  return new UnauthorizedError(code, message, details);
}

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const init: RequestInit = { method, headers, credentials: "same-origin" };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  if (method !== "GET") {
    const token = readCSRFToken();
    if (token) headers["X-CSRF-Token"] = token;
  }

  const resp = await fetch(path, init);
  if (resp.status === 401) throw await unauthorized(resp, path);
  if (resp.status === 204) return undefined as T;

  if (!resp.ok) {
    const { code, message, details } = await parseErrorBody(resp, "error", `HTTP ${resp.status}`);
    throw new APIError(resp.status, code, message, details);
  }

  return (await resp.json()) as T;
}

// requestText fetches a text/plain body (e.g. GET /api/jobs/{id}/log). Same 401/error
// handling as request<T>, but returns the raw text rather than parsing JSON.
async function requestText(path: string): Promise<string> {
  const resp = await fetch(path, { method: "GET", credentials: "same-origin" });
  if (resp.status === 401) throw await unauthorized(resp, path);
  if (!resp.ok) throw new APIError(resp.status, "error", `HTTP ${resp.status}`);
  return resp.text();
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  getText: (path: string) => requestText(path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};
