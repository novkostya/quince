import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfigEditor } from "./ConfigEditor";
import type { Config, StorageEntry } from "@/lib/types";

// THE CRASH THIS PINS: quince#473 flattened `storage:` from an object to a LIST in Go, and the TS
// `Config` type kept the old `{ storages, backend, zfs, retention }` shape for a day. The editor
// read `draft.storage.backend`, which on a null `storage` — the demo's genuine state — threw
// `null is not an object` and took the whole Settings route down with it.
//
// `make gates-ui` was GREEN throughout. The type was internally consistent and NOTHING
// CROSS-CHECKS IT AGAINST THE GO SCHEMA, which is quince#493, filed before this happened and
// describing it exactly. Reported from a demo deploy by the Operator, not by a gate.
//
// So these render the two shapes the server actually serves. They are cheap and they are the only
// thing standing between the next schema move and the same crash.

function entry(over: Partial<StorageEntry> = {}): StorageEntry {
  return {
    name: "local",
    path: "/backups",
    default: true,
    backend: "zfs",
    zfs: { parent_dataset: "pool/quince", mode: "hook", hook_cmd: "", seed: "auto" },
    retention: { keep_recent: 10, keep_daily: 30, keep_weekly: 12 },
    ...over,
  };
}

function config(storage: StorageEntry[] | null): Config {
  return {
    backup: { transport: "auto", require_encryption: true },
    storage,
    devices: { usbmuxd_socket: "/var/run/usbmuxd", netmuxd_addr: "127.0.0.1:27015" },
    sessions: { ttl_minutes: 30 },
    automation: { staleness_days: 3, reminder_cooldown_hours: 24 },
    ui: { theme: "system" },
  } as Config;
}

function renderEditor(c: Config) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ConfigEditor config={c} />
    </QueryClientProvider>,
  );
}

describe("ConfigEditor with a flattened storage list", () => {
  // THE EXACT CRASH. `--demo` never runs the storage requirement, so a demo config genuinely
  // serves `storage: null` — and the old code dereferenced it.
  it("renders when storage is null instead of throwing", () => {
    renderEditor(config(null));
    expect(screen.getByText(/none declared/i)).toBeInTheDocument();
  });

  it("lists the declared storages with their per-entry backends", () => {
    renderEditor(config([entry(), entry({ name: "shuttle", path: "/mnt/shuttle", default: false, backend: "hardlink" })]));
    expect(screen.getByText(/2 declared/)).toBeInTheDocument();
    expect(screen.getByText(/local \(zfs\)/)).toBeInTheDocument();
    expect(screen.getByText(/shuttle \(hardlink\)/)).toBeInTheDocument();
  });

  // An unnamed entry is legal since quince#504 — `name` defaults to the path at config load, but a
  // client reading a hand-written file before that defaulting must not render "undefined".
  it("falls back to the path when an entry has no name", () => {
    renderEditor(config([entry({ name: "", path: "/backups" })]));
    expect(screen.getByText(/\/backups \(zfs\)/)).toBeInTheDocument();
  });

  // THE GLOBAL BACKEND SELECT IS GONE, not moved. A control editing a key that no longer exists
  // would PUT it back and `unknownKeys` would warn about it forever.
  it("offers no global storage-backend control", () => {
    renderEditor(config([entry()]));
    expect(screen.queryByText(/^Storage backend$/)).toBeNull();
  });
});
