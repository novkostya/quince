import { afterEach, describe, expect, it, vi } from "vitest";
import { backoffDelay, close, connect } from "./client";
import { api, notifyUnauthorized } from "@/lib/api";
import { useDevicesStore } from "@/stores/devices";
import type { Device } from "@/lib/types";

// The handshake rejection is SIMULATED rather than driven off a real expiry (quince#374's
// acceptance): the browser gives script no way to see the 401, so what the client actually reacts
// to is a close, and what distinguishes the cases is the answer /api/auth/status gives afterwards.
// Spread the real module rather than replacing it: `lib/refresh` and `lib/auth` are in this import
// graph and pull their own names out of `lib/api`, which a narrow factory would leave undefined.
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, api: { ...actual.api, get: vi.fn() }, notifyUnauthorized: vi.fn() };
});

describe("backoffDelay", () => {
  it("grows exponentially and caps at 30s (with jitter factor 1.0)", () => {
    const noJitter = () => 0.5; // 0.5 + 0.5 = 1.0
    expect(backoffDelay(0, noJitter)).toBe(500);
    expect(backoffDelay(1, noJitter)).toBe(1000);
    expect(backoffDelay(2, noJitter)).toBe(2000);
    expect(backoffDelay(10, noJitter)).toBe(30_000);
  });

  it("applies ±50% jitter", () => {
    expect(backoffDelay(0, () => 0)).toBe(250); // 500 * 0.5
    expect(backoffDelay(0, () => 1)).toBe(750); // 500 * 1.5
  });
});

// A minimal WebSocket stand-in that records every instance and never auto-connects, so the resume
// path can be exercised deterministically.
class FakeWS {
  static instances: FakeWS[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  readyState = FakeWS.CONNECTING;
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor(public url: string) {
    FakeWS.instances.push(this);
  }
  close(): void {
    this.readyState = FakeWS.CLOSED;
  }
}

describe("ws resume reconnect (iOS PWA soak fix)", () => {
  afterEach(() => {
    close();
    FakeWS.instances = [];
    vi.unstubAllGlobals();
  });

  it("replaces a stale (zombie) socket when the app returns to the foreground", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    connect();
    expect(FakeWS.instances).toHaveLength(1);

    // iOS suspended the PWA and killed the socket WITHOUT firing onclose — it's dead but still
    // referenced, so the old code's `if (socket) return` would never reconnect.
    FakeWS.instances[0].readyState = FakeWS.CLOSED;

    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));

    // A fresh socket is opened rather than sitting on "reconnecting…" forever.
    expect(FakeWS.instances).toHaveLength(2);
  });

  it("does not churn a healthy socket on foreground", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    connect();
    FakeWS.instances[0].readyState = FakeWS.OPEN; // live connection

    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    document.dispatchEvent(new Event("visibilitychange"));

    expect(FakeWS.instances).toHaveLength(1); // untouched
  });
});

// quince#374: a handshake the server REJECTED and a server that is not there both arrive as
// onclose(1006). The defect was reporting both as a network fault; the fix must tell them apart
// WITHOUT breaking the retry loop, which is correct behaviour for a genuine outage.
describe("ws handshake rejected for a lost session", () => {
  // Let every pending microtask settle — the close handler asks /api/auth/status and acts on the
  // answer, so the assertion has to come after that promise chain, not after the close.
  const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

  afterEach(() => {
    close();
    vi.useRealTimers();
    FakeWS.instances = [];
    vi.unstubAllGlobals();
    vi.mocked(api.get).mockReset();
    vi.mocked(notifyUnauthorized).mockReset();
  });

  it("reports a lost session when the server answers and says we are logged out", async () => {
    vi.mocked(api.get).mockResolvedValue({ state: "needs_login", csrf_token: "" });
    vi.stubGlobal("WebSocket", FakeWS);
    connect();

    FakeWS.instances[0].onclose?.(); // the 401 rejection, as script actually sees it
    await flush();

    expect(api.get).toHaveBeenCalledWith("/api/auth/status");
    // The whole defect: this is what stops the badge being the only thing the user is told.
    expect(notifyUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("does NOT report a lost session when the server is unreachable, and keeps retrying", async () => {
    vi.useFakeTimers();
    vi.mocked(api.get).mockRejectedValue(new Error("network down"));
    vi.stubGlobal("WebSocket", FakeWS);
    connect();

    FakeWS.instances[0].onclose?.();
    await vi.advanceTimersByTimeAsync(0); // let the auth probe settle without moving the clock

    // A server that cannot answer proves nothing about the session. `RequireAuth` reads an errored
    // status as `needs_login`, so reporting here would bounce a logged-in user to the login screen
    // on every daemon restart — the worse defect this fix must not trade for.
    expect(notifyUnauthorized).not.toHaveBeenCalled();

    // And the retry loop must SURVIVE: backing off forever is correct for a real outage, so this
    // asserts a fresh socket appears rather than assuming the schedule was left alone. 800 ms is
    // past the longest first backoff (500 ms base, +50% jitter).
    await vi.advanceTimersByTimeAsync(800);
    expect(FakeWS.instances).toHaveLength(2);
  });

  it("does NOT report a lost session when the socket dropped but the session is alive", async () => {
    vi.mocked(api.get).mockResolvedValue({ state: "authenticated", csrf_token: "t" });
    vi.stubGlobal("WebSocket", FakeWS);
    connect();

    FakeWS.instances[0].onclose?.();
    await flush();

    expect(notifyUnauthorized).not.toHaveBeenCalled();
  });

  it("asks nothing once the client has been stopped (logout / unmount)", async () => {
    vi.mocked(api.get).mockResolvedValue({ state: "needs_login", csrf_token: "" });
    vi.stubGlobal("WebSocket", FakeWS);
    connect();
    // Capture the handler BEFORE close() detaches it. Invoking it afterwards is what exercises the
    // `stopped` guard; calling `ws.onclose?.()` post-close would be a no-op against a nulled field
    // and would pass without proving anything.
    const onclose = FakeWS.instances[0].onclose!;
    close(); // logout tears the socket down; that close is ours, not a rejection

    onclose();
    await flush();

    expect(api.get).not.toHaveBeenCalled();
  });
});

// quince#948: a `device.attached` that arrives while the hello-triggered snapshot GET is still in
// flight was applied and then UNDONE by that snapshot, permanently — `refreshAll` ends in
// `replaceAll`, and the list it replaces with was read before the event happened. Nothing refetches
// until the next reconnect, so the device simply stays missing.
//
// Driven through the real `refreshAll` rather than a mock of it: the defect is the ORDER of two
// real effects on one store, and a stubbed refresh cannot have an order.
describe("ws refresh does not clobber events that raced it (quince#948)", () => {
  const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

  const pad = (): Device => ({
    udid: "UDID-PAD",
    name: "the-pad",
    model: "iPad13,4",
    ios_version: "18.5",
    transports: { wifi: "2026-08-14T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "on",
    notifications_enabled: true,
    last_seen: "2026-08-14T00:00:00Z",
    last_backup: null,
  });

  afterEach(() => {
    close();
    FakeWS.instances = [];
    vi.unstubAllGlobals();
    vi.mocked(api.get).mockReset();
    useDevicesStore.getState().replaceAll([]);
  });

  it("replays an attach that landed mid-refresh, instead of losing it to the snapshot", async () => {
    // The snapshot the server had BEFORE the pad attached — the whole point: it is not wrong, it is
    // merely older than the event, which is the one thing `replaceAll` cannot express.
    let releaseDevices: (v: { devices: Device[] }) => void = () => {};
    const devicesGet = new Promise<{ devices: Device[] }>((resolve) => {
      releaseDevices = resolve;
    });
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path === "/api/devices") return devicesGet as never;
      if (path === "/api/jobs") return Promise.resolve({ jobs: [], next_cursor: null }) as never;
      return Promise.resolve({ versions: [] }) as never;
    });

    vi.stubGlobal("WebSocket", FakeWS);
    connect();
    const ws = FakeWS.instances[0];
    ws.onmessage?.({ data: JSON.stringify({ type: "hello", data: { server_version: "t" } }) });

    // The pad attaches while the GET is still out.
    ws.onmessage?.({
      data: JSON.stringify({ type: "device.attached", data: { ...pad(), transport: "wifi" } }),
    });

    releaseDevices({ devices: [] });
    await flush();

    // Against the old ordering this is 0: the attach was applied and the empty snapshot wiped it.
    expect(useDevicesStore.getState().order).toEqual(["UDID-PAD"]);
  });

  it("does not replay a queue across a reconnect", async () => {
    let releaseDevices: (v: { devices: Device[] }) => void = () => {};
    const devicesGet = new Promise<{ devices: Device[] }>((resolve) => {
      releaseDevices = resolve;
    });
    vi.mocked(api.get).mockImplementation((path: string) => {
      if (path === "/api/devices") return devicesGet as never;
      if (path === "/api/jobs") return Promise.resolve({ jobs: [], next_cursor: null }) as never;
      if (path === "/api/versions") return Promise.resolve({ versions: [] }) as never;
      return Promise.resolve({ state: "authenticated", csrf_token: "t" }) as never;
    });

    vi.stubGlobal("WebSocket", FakeWS);
    connect();
    const ws = FakeWS.instances[0];
    ws.onmessage?.({ data: JSON.stringify({ type: "hello", data: { server_version: "t" } }) });
    ws.onmessage?.({
      data: JSON.stringify({ type: "device.attached", data: { ...pad(), transport: "wifi" } }),
    });

    // The socket dies before the snapshot lands. Whatever was queued belonged to THAT session: the
    // reconnect fetches its own, and replaying a pre-disconnect attach on top of it would put back
    // a device the refresh may have just correctly removed.
    ws.onclose?.();
    releaseDevices({ devices: [] });
    await flush();

    expect(useDevicesStore.getState().order).toEqual([]);
  });
});
