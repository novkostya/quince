import { describe, it, expect, vi, beforeAll } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfigEditor } from "./ConfigEditor";
import type { Config, StorageEntry } from "@/lib/types";

// `updateConfig` is mocked so the post-save branch is reachable. The notice this rung removes lived
// ONLY in that branch, so a suite that never submits cannot see it — which is how the first version
// of the test below passed against the unfixed code.
const saveResult = vi.fn();
vi.mock("@/lib/config", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/config")>();
  return { ...actual, updateConfig: (c: unknown) => saveResult(c) as Promise<unknown> };
});

// `window.matchMedia` IS MISSING IN JSDOM AND THE SUCCESS PATH NEEDS IT. `onSuccess` calls
// `setTheme`, which calls `apply`, which reads `matchMedia("(prefers-color-scheme: dark)")` — so
// without this stub the save handler throws and the confirmation never renders. It cost a run to
// find, because the symptom is "the Saved span is absent", which is indistinguishable from the
// thing the test below is asserting.
//
// A STUB RATHER THAN A MOCK OF `setTheme`: the real theme code then runs, so a save that would
// break theming in a browser still breaks here.
beforeAll(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

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
    zfs: {
      parent_dataset: "pool/quince",
      mode: "hook",
      hook_cmd: "",
      ssh_user: "zfsuser",
      ssh_host: "zfshost",
      ssh_port: 22,
      ssh_key: "/data/keys/zfs",
      seed: "auto",
    },
    retention: { keep_recent: 10, keep_daily: 30, keep_weekly: 12 },
    ...over,
  };
}

function config(storage: StorageEntry[] | null): Config {
  return {
    backup: { preferred_transport: "usb", require_encryption: true },
    storage,
    devices: { usbmuxd_socket: "/var/run/usbmuxd", netmuxd_addr: "127.0.0.1:27015" },
    sessions: { ttl_minutes: 30 },
    reconcile: { interval_minutes: 360 },
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

// quince#629 — EVERY FIELD'S VISIBLE LABEL IS ASSOCIATED WITH ITS CONTROL.
//
// The <Label> had no `htmlFor` and the controls had no `aria-label`, so nothing connected them: a
// screen reader reaching this form announced a combobox with its options and no indication of what
// it sets. The issue named the two selects; every field in the form had it.
//
// ASSERTED THROUGH getByLabelText, WHICH IS THE THING THE USER GETS. A snapshot of `htmlFor` would
// pass on an id that matches nothing. jsdom computes the accessible name from the `htmlFor`/`id`
// pair, so this is reachable at the unit layer and needs no browser (architect ruling 2026-08-04).
describe("ConfigEditor labels are associated with their controls", () => {
  it("resolves every labelled control by its visible label", () => {
    renderEditor(config([entry()]));
    // The two the issue named…
    expect(screen.getByLabelText("Preferred transport")).toBeInTheDocument();
    expect(screen.getByLabelText("Theme")).toBeInTheDocument();
    // …and the Input, which had the same gap and which the issue did not name.
    expect(screen.getByLabelText("Session TTL (minutes)")).toBeInTheDocument();
  });

  // The association must reach the RIGHT element, not merely resolve. An id pointing at a wrapper
  // would still satisfy getByLabelText in some shapes while leaving the control unnamed.
  it("names the control itself, not a wrapper", () => {
    renderEditor(config([entry()]));
    expect(screen.getByLabelText("Preferred transport").tagName).toBe("SELECT");
    expect(screen.getByLabelText("Theme").tagName).toBe("SELECT");
    expect(screen.getByLabelText("Session TTL (minutes)").tagName).toBe("INPUT");
  });

  // Each field mints its OWN id. `useId` guarantees it, and asserting it here is what stops a later
  // "simplify" from hoisting one id into the parent — which would silently point three labels at one
  // control and read as fixed.
  it("gives each field a distinct id", () => {
    renderEditor(config([entry()]));
    const ids = [
      screen.getByLabelText("Preferred transport").id,
      screen.getByLabelText("Theme").id,
      screen.getByLabelText("Session TTL (minutes)").id,
    ];
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids.every((i) => i !== "")).toBe(true);
  });
});

// qn.6g G8's cheap half. The e2e in `story9-settings-copy.spec.ts` walks a browser through a real
// save; this pins the same claim without one, because a gate that needs Playwright to notice a
// removed sentence is a gate that runs on a fraction of the pushes.
describe("ConfigEditor promises no restart", () => {
  // THE FORM'S OWN FIELDS ARE WHAT DECIDE THIS, so they are asserted rather than assumed. The
  // notice was deleted rather than made conditional, on the argument that nothing here is
  // restart-required — and that argument holds only while the form renders these four. A field
  // from contracts §6's restart bin (`sessions.allow_insecure_transport`, `devices.*`) appearing
  // here would make the deletion wrong, and this test is what notices.
  it("renders only fields that are live or unread — the premise of deleting the notice", () => {
    renderEditor(config([entry()]));

    expect(screen.getByText(/^Preferred transport$/)).toBeInTheDocument();
    expect(screen.getByText(/Require encryption/)).toBeInTheDocument();
    expect(screen.getByText(/^Session TTL \(minutes\)$/)).toBeInTheDocument();
    expect(screen.getByText(/^Theme$/)).toBeInTheDocument();

    // The two restart-bin keys, neither of which this form has ever edited. If either arrives, the
    // unconditional deletion stops being honest and the notice belongs on THAT field.
    expect(screen.queryByText(/insecure transport/i)).toBeNull();
    expect(screen.queryByText(/netmuxd|usbmuxd/i)).toBeNull();
  });

  // THE TEST HAS TO SAVE, and the first version of it did not — which made it a test that could
  // not fail.
  //
  // It rendered the form and asserted `queryByText(/restart/i)` was null. That passes on the
  // UNFIXED code too: the notice only ever rendered inside the `saved` branch, so a form that has
  // not been submitted does not contain it either way. **Mutation testing caught this** — the old
  // string was put back, the whole suite stayed green, and the assertion was asserting the absence
  // of something that was never present.
  //
  // So `updateConfig` is mocked and the form is actually submitted. What is asserted is the state a
  // user reaches, which is the only state the notice ever occupied.
  it("says nothing about restarting quince, after an actual save", async () => {
    saveResult.mockResolvedValue({
      config: config([entry()]),
      warnings: [],
      source: { path: "/data/config.yml", mtime: null },
    });

    const { container } = renderEditor(config([entry()]));
    const form = container.querySelector("form");
    expect(form).not.toBeNull();
    fireEvent.submit(form!);

    // THE SUBMIT ACTUALLY HAPPENED. Asserted separately so a harness that stops submitting fails
    // here, loudly, rather than at the absence-assertion below — where it would read as "the notice
    // is gone" and be indistinguishable from success.
    await waitFor(() => expect(saveResult).toHaveBeenCalled());

    // Reaching the confirmation is the precondition for the assertion after it.
    expect(await screen.findByText(/^Saved$/)).toBeInTheDocument();
    expect(screen.queryByText(/restart/i)).toBeNull();
    // And NOT "Saved · applied", which was the first draft of the replacement and is a different
    // lie: `sessions.ttl_minutes` is read by nothing (quince#656), so a save of it neither applies
    // nor fails. Trading a false restart promise for a false apply promise is not a fix.
    expect(screen.queryByText(/applied/i)).toBeNull();
  });
});

// THE DRAFT FOLLOWS THE SERVER (quince#764), and the absence of this test is why the defect shipped.
//
// `useState(config)` captures its argument on FIRST MOUNT and ignores every later value. React Query
// serves a cached document instantly and refetches behind it, so the editor could seed from a stale
// document while the rest of the page re-rendered with the fresh one — and `PUT /api/config` is a
// full-document replace, so saving one field shipped the whole stale draft and reverted whatever had
// changed server-side.
//
// Observed on the staging stand: two storages hand-edited to `backend: hardlink`, quince restarted,
// one unrelated Settings save, and both were back to `auto` on disk. The preview panel was showing
// the CORRECT file at the same moment — two documents on one screen, disagreeing.
//
// NO TEST IN THIS FILE EVER RE-RENDERED WITH A NEW PROP, which is the whole gap: every case mounts
// once and asserts against that mount.
describe("ConfigEditor follows the server when the config changes underneath it", () => {
  it("re-syncs a clean form to a new config prop", () => {
    const { rerender } = renderEditor(config([entry()]));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    expect((screen.getByLabelText(/preferred transport/i) as HTMLSelectElement).value).toBe("usb");

    const next = config([entry()]);
    next.backup.preferred_transport = "wifi";
    rerender(
      <QueryClientProvider client={qc}>
        <ConfigEditor config={next} />
      </QueryClientProvider>,
    );

    expect((screen.getByLabelText(/preferred transport/i) as HTMLSelectElement).value).toBe("wifi");
  });

  // THE SAVE MUST SHIP WHAT THE SERVER LAST SERVED, not what it served when the page opened. This is
  // the assertion closest to the reported defect: the storage list was never touched in the UI and
  // came back changed on disk.
  it("saves the storages from the CURRENT config, not the one it mounted with", async () => {
    saveResult.mockClear();
    saveResult.mockResolvedValue({ config: config([entry()]), warnings: [], source: {}, file_text: "" });

    const { rerender } = renderEditor(config([entry({ backend: "auto" })]));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    // The server now reports `hardlink` — a hand-edit quince has adopted.
    rerender(
      <QueryClientProvider client={qc}>
        <ConfigEditor config={config([entry({ backend: "hardlink" })])} />
      </QueryClientProvider>,
    );

    fireEvent.submit(screen.getByRole("button", { name: /save/i }).closest("form")!);

    await waitFor(() => expect(saveResult).toHaveBeenCalled());
    const sent = saveResult.mock.calls[0][0] as Config;
    expect(sent.storage?.[0].backend).toBe("hardlink");
  });
});

// A DIRTY FORM IS NOT OVERWRITTEN, AND THE USER IS TOLD (quince#764 PR 2).
//
// PR 1 re-synced only a CLEAN form, which is the right conservative half: React Query refetches on
// window focus, so an unconditional re-sync would wipe an edit in progress. It left a window open —
// a dirty form still saved its stale sections silently. This closes it by SAYING SO rather than by
// choosing for the user.
describe("ConfigEditor when the config changes while the form is dirty", () => {
  function rerenderWith(rerender: (ui: React.ReactElement) => void, c: Config) {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    rerender(
      <QueryClientProvider client={qc}>
        <ConfigEditor config={c} />
      </QueryClientProvider>,
    );
  }

  it("keeps the edit and warns instead of silently overwriting either side", () => {
    const { rerender } = renderEditor(config([entry({ backend: "auto" })]));

    // The user edits one field…
    fireEvent.change(screen.getByLabelText(/preferred transport/i), { target: { value: "wifi" } });
    // …and the server document moves underneath them.
    rerenderWith(rerender, config([entry({ backend: "hardlink" })]));

    // The edit survives — this is what an unconditional re-sync would have destroyed.
    expect((screen.getByLabelText(/preferred transport/i) as HTMLSelectElement).value).toBe("wifi");
    // And the divergence is visible rather than silent.
    expect(screen.getByText(/configuration changed elsewhere/i)).toBeTruthy();
  });

  it("offers taking the server version, and says that it discards the edit", () => {
    const { rerender } = renderEditor(config([entry({ backend: "auto" })]));
    fireEvent.change(screen.getByLabelText(/preferred transport/i), { target: { value: "wifi" } });
    rerenderWith(rerender, config([entry({ backend: "hardlink" })]));

    fireEvent.click(screen.getByRole("button", { name: /discard my edits/i }));

    // The form is now the server's document, and the notice is gone with its cause.
    expect((screen.getByLabelText(/preferred transport/i) as HTMLSelectElement).value).toBe("usb");
    expect(screen.queryByText(/configuration changed elsewhere/i)).toBeNull();
  });

  // A CLEAN form must NOT warn — the notice exists for the case where something has to be chosen,
  // and firing it on every background refetch would train the reader to ignore it.
  it("stays silent when the form is clean", () => {
    const { rerender } = renderEditor(config([entry({ backend: "auto" })]));
    rerenderWith(rerender, config([entry({ backend: "hardlink" })]));
    expect(screen.queryByText(/configuration changed elsewhere/i)).toBeNull();
  });

  // THE NOTICE MUST NOT FIRE AFTER THE USER'S OWN SAVE, and nothing else in this file covers it.
  //
  // `dirty` compares `draft` against `synced` — NOT against the incoming config. So after any save:
  // the mutation invalidates, the refetch delivers a new object, and without `onSuccess` adopting
  // the response `synced` is still the PRE-SAVE document while `draft` carries the edit. `dirty` is
  // true and the form announces "the configuration changed elsewhere" immediately after you saved
  // it yourself, every single time.
  //
  // MEASURED: with the two adoption lines deleted, all 246 tests still passed. That is the exact
  // failure the clean-form test above exists to prevent — a notice that fires on every refetch
  // trains the reader to ignore it — sitting one line away with no gate in between.
  it("does not warn after the user's own save", async () => {
    const afterSave = config([entry({ backend: "auto" })]);
    afterSave.backup.preferred_transport = "wifi";
    saveResult.mockClear();
    saveResult.mockResolvedValue({ config: afterSave, warnings: [], source: {}, file_text: "" });

    const { rerender } = renderEditor(config([entry({ backend: "auto" })]));
    fireEvent.change(screen.getByLabelText(/preferred transport/i), { target: { value: "wifi" } });
    fireEvent.submit(screen.getByRole("button", { name: /^save$/i }).closest("form")!);
    await waitFor(() => expect(saveResult).toHaveBeenCalled());

    // The invalidation's refetch: a NEW object carrying what the save just wrote.
    const refetched = config([entry({ backend: "auto" })]);
    refetched.backup.preferred_transport = "wifi";
    rerenderWith(rerender, refetched);

    expect(screen.queryByText(/configuration changed elsewhere/i)).toBeNull();
  });
});

// qn.6i — THE RECONCILIATION INTERVAL IS EDITABLE, AND `0` IS A VALUE RATHER THAN A FLOOR.
//
// D12 requires every setting to be editable in the UI, so a key that exists in config.yml and not
// here is half-shipped. The `min` assertion is the one with a real failure behind it: `min={1}`
// would make the OFF state unreachable from the only surface most users have, while the config file
// still accepted it — a setting that means something the UI cannot express.
describe("the reconciliation interval", () => {
  it("renders the current value and allows 0", () => {
    renderEditor(config([entry()]));
    const input = screen.getByLabelText(/reconciliation interval/i) as HTMLInputElement;
    expect(input.value).toBe("360");
    expect(input.min).toBe("0");
  });

  // The helper text is load-bearing, not decoration: "0 turns the schedule off" reads as "quince
  // stops noticing anything" unless it also says which triggers survive.
  it("says which triggers survive turning the schedule off", () => {
    renderEditor(config([entry()]));
    expect(screen.getByText(/0 turns the schedule off/i)).toBeInTheDocument();
    expect(screen.getByText(/adding a storage/i)).toBeInTheDocument();
  });
});
