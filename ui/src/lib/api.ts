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

// UnauthorizedError is thrown on any 401 so callers can drop to the login screen.
export class UnauthorizedError extends APIError {
  constructor() {
    super(401, "unauthorized", "authentication required");
    this.name = "UnauthorizedError";
  }
}

type Method = "GET" | "POST" | "PUT" | "DELETE";

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
  if (resp.status === 401) throw new UnauthorizedError();
  if (resp.status === 204) return undefined as T;

  if (!resp.ok) {
    let code = "error";
    let message = `HTTP ${resp.status}`;
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
      // non-JSON error body; keep defaults
    }
    throw new APIError(resp.status, code, message, details);
  }

  return (await resp.json()) as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};
