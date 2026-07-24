import { afterEach, describe, expect, it, vi } from "vitest";
import { backoffDelay, close, connect } from "./client";

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
