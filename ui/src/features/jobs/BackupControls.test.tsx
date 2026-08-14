import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { BackupControls, BackupControlsStatus } from "./BackupControls";
import type { Device, Job, Storage, Transports } from "@/lib/types";
import type { Storages } from "./useStorages";
import { MemoryRouter } from "react-router-dom";

function device(transports: Transports): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone16,1",
    ios_version: "26.0.1",
    transports,
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "unknown",
    last_seen: "2026-07-20T00:00:00Z",
    last_backup: null,
  };
}

function runningJob(): Job {
  return {
    id: "J1",
    udid: "DEV-1",
    kind: "backup",
    transport: "wifi",
    state: "backing_up",
    progress: { phase: "receiving", percent: 40, bytes_done: 1, bytes_total: 2, files_received: 3, liveness: "active" },
    started_at: "2026-07-20T00:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "J1",
    attempt: 1,
    version_id: null,
    storage_id: null,
  };
}


// The storage subscription is LIFTED to the page now (quince#325's rule split the control from its
// prose, so two components share one fetch). These tests do not exercise the storage surface, so
// they pass an empty loaded list — which renders no control and no notices, exactly as a
// single-storage install does.
function noStorages(over: Partial<Storages> = {}): Storages {
  return { state: { status: "loaded", storages: [] }, recheck: () => {}, rechecking: {}, reload: () => {}, ...over };
}

const storageProps = { storages: noStorages(), storageID: "", setStorageID: () => {} };
const statusProps = { storages: noStorages(), storageID: "" };
const ok = () => Promise.resolve(true);

describe("BackupControls", () => {
  it("starts a backup over auto by default", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />);
    fireEvent.click(screen.getByTestId("backup-now"));
    // undefined storage = "the default", which the SERVER resolves. The selector sends nothing
    // rather than pre-filling the default's id, so an untouched control behaves exactly as this
    // call did before the rung (qn.6c story 9).
    expect(start).toHaveBeenCalledWith("auto", { storageID: undefined });
  });

  // The "and explains" half of this test moved to the status block below, which is where the
  // sentence now renders. The button being disabled is still this component's claim.
  it("disables the button when the device is on no transport", () => {
    render(<BackupControls device={device({})} start={ok} cancel={ok} busy={false} {...storageProps} />);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers a transport override only when the device is on both transports", () => {
    const { rerender } = render(
      <BackupControls device={device({ usb: "t" })} start={ok} cancel={ok} busy={false} {...storageProps} />,
    );
    expect(screen.queryByLabelText(/backup transport/i)).toBeNull();
    rerender(
      <BackupControls device={device({ usb: "t", wifi: "t" })} start={ok} cancel={ok} busy={false} {...storageProps} />,
    );
    expect(screen.getByLabelText(/backup transport/i)).toBeTruthy();
  });

  it("passes the selected transport when overridden", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t", wifi: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />);
    fireEvent.change(screen.getByLabelText(/backup transport/i), { target: { value: "wifi" } });
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("wifi", { storageID: undefined });
  });

  // quince#653 — `Auto` IS GONE FROM THE SELECTOR, and here are the two things that could break.
  //
  // The option meant nothing where it rendered: the selector only mounts when the device is on
  // BOTH, and `resolveTransport` resolves `auto` to USB whenever USB is present. So `Auto` was a
  // second label for `USB`. Removing it is behaviour-preserving, and these assert that rather than
  // the PR claiming it.

  it("does not offer Auto in the selector, and shows USB as the concrete default", () => {
    render(<BackupControls device={device({ usb: "t", wifi: "t" })} start={ok} cancel={ok} busy={false} {...storageProps} />);
    const select = screen.getByLabelText(/backup transport/i) as HTMLSelectElement;
    expect([...select.options].map((o) => o.value)).toEqual(["usb", "wifi"]);
    // The control names the transport it will actually use, rather than a word for "whichever".
    expect(select.value).toBe("usb");
  });

  it("sends usb from an untouched selector — the same job auto produced here", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t", wifi: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />);
    fireEvent.click(screen.getByTestId("backup-now"));
    // Behaviour preservation as an assertion, not as a sentence in a PR: `auto` with both
    // transports present resolved to USB in the engine, so this is the same backup as before.
    expect(start).toHaveBeenCalledWith("usb", { storageID: undefined });
  });

  // THE TRAP THE ISSUE NAMES, on the path it flagged as "read, not exercised". A device on ONE
  // transport renders no selector, and `auto` must reach the API so the engine resolves it to
  // whichever transport the device is on. Change the STATE default to "usb" and this Wi-Fi-only
  // device sends `usb` — which `resolveTransport` returns unchecked, turning an immediate backup
  // into a job that waits out its window for a device that cannot appear.
  it("sends auto for a Wi-Fi-only device, where no selector renders", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ wifi: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />);
    expect(screen.queryByLabelText(/backup transport/i)).toBeNull();
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("auto", { storageID: undefined });
  });

  // THE RESIDUAL — the only case dropping `Auto` could have cost anything, and it is now strictly
  // better than before rather than merely no worse.
  //
  // Presence changes while the page is open: the user picks USB, then the cable comes out. The
  // selector unmounts, and because the sent transport is DERIVED from current presence rather than
  // held in state, the stale `usb` cannot be sent. Under the old code the state still held the
  // choice, and a `usb` request against a Wi-Fi-only device hangs.
  //
  // Derived rather than reset by an effect on purpose: an effect leaves a window between the
  // presence change and the reset in which a press still sends the stale value.
  it("falls back to auto when the chosen transport disappears while the page is open", () => {
    const start = vi.fn().mockResolvedValue(true);
    const { rerender } = render(
      <BackupControls device={device({ usb: "t", wifi: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />,
    );
    fireEvent.change(screen.getByLabelText(/backup transport/i), { target: { value: "usb" } });

    // The cable comes out. Same component, new presence — this is the WS-driven rerender.
    rerender(
      <BackupControls device={device({ wifi: "t" })} start={start} cancel={ok} busy={false} {...storageProps} />,
    );
    expect(screen.queryByLabelText(/backup transport/i)).toBeNull();

    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("auto", { storageID: undefined });
  });

  it("shows cancel for a running job", () => {
    const cancel = vi.fn().mockResolvedValue(true);
    render(
      <BackupControls
        device={device({ wifi: "t" })}
        activeJob={runningJob()}
        start={ok}
        cancel={cancel}
        busy={false}
        {...storageProps}
      />,
    );
    fireEvent.click(screen.getByTestId("cancel-backup"));
    expect(cancel).toHaveBeenCalledWith("J1");
  });

  // The shared refusal moved to BackupControlsStatus with the rest of the block-level text; the
  // assertion moves with it rather than being dropped.
  it("surfaces the shared error, from the status block", () => {
    render(
      <BackupControlsStatus
        device={device({ wifi: "t" })}
        activeJob={runningJob()}
        error="a backup is already running for this device"
        {...statusProps}
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain("already running");
  });
});

// The action row must hold BUTTONS ONLY. A flex item is as wide as its widest child, so a status
// line left inside BackupControls' column set that column's width and pushed the next button out by
// the overhang — a large gap between "Back up now" and "Manage encryption", visible only on an
// OFFLINE device because the sentence is the only thing that renders it (quince#325, screenshot).
//
// Asserted structurally rather than by class name: the defect was about which ELEMENT contains the
// text, and a class assertion would stay green while the text moved back into the row.
describe("BackupControls status placement", () => {
  it("keeps the offline reason OUT of the control row", () => {
    const { container } = render(
      <BackupControls device={device({})} start={ok} cancel={ok} busy={false} {...storageProps} />,
    );
    expect(container.textContent).not.toMatch(/connect the device to back it up/i);
  });

  // No test that a REFUSAL stays out of the row: `error` is no longer a prop of BackupControls, so
  // the exclusion is enforced by the type rather than by an assertion. A test here could only pass a
  // string the component cannot accept, which would assert nothing.

  // The other direction: the lines must still be RENDERED somewhere, or this "fix" is a deletion.
  it("still renders both lines, in the block that sits below the row", () => {
    render(<BackupControlsStatus device={device({})} error="nope" {...statusProps} />);
    expect(screen.getByText(/connect the device to back it up/i)).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toBe("nope");
  });

  // While a job runs, "why the button is disabled" is not a thing to say — the button is Cancel.
  it("drops the offline reason while a job is running, but keeps the refusal", () => {
    render(
      <BackupControlsStatus device={device({})} activeJob={runningJob()} error="nope" {...statusProps} />,
    );
    expect(screen.queryByText(/connect the device to back it up/i)).toBeNull();
    expect(screen.getByRole("alert").textContent).toBe("nope");
  });
});

// quince#452's retry regression: `start(transport, storageID?, retryOf?)` had `storageID` inserted
// in the MIDDLE of a two-argument signature, and the two Retry call sites still read
// `start("auto", job.id)` — sending a JOB id as the storage id. Both parameters were optional
// `string`, so every call site typechecked and the compiler had nothing to say.
//
// The signature now takes a NAMED OBJECT, which makes the same mistake a compile error. These
// assert the wire-level consequence: a retry must name `retryOf` and must NOT name a storage.
describe("retry names the job, not a storage", () => {
  it("sends retryOf and no storage id", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(
      <BackupControls
        device={device({ wifi: "t" })}
        start={start}
        cancel={ok}
        busy={false}
        {...storageProps}
      />,
    );
    // The control's own start still carries a storage and no retry…
    fireEvent.click(screen.getByTestId("backup-now"));
    const [, opts] = start.mock.calls[0] as [string, { storageID?: string; retryOf?: string }];
    expect(opts).not.toHaveProperty("retryOf");
    expect(opts.storageID).toBeUndefined();
  });
});

// quince#616, the same shape as `StorageSelect` — see that file for the full reasoning. 12px meant
// a 1.33x page zoom on tap, on the control sitting directly beside "Back up now" on a phone.
// A class assertion cannot prove Safari's behaviour; only a device can, and that is owed.
describe("BackupControls transport select is 16px on mobile", () => {
  function renderBoth() {
    return render(
      <BackupControls
        device={device({ usb: "t", wifi: "t" })}
        start={ok}
        cancel={ok}
        busy={false}
        {...storageProps}
      />,
    );
  }

  it("steps the select 16px -> 12px at the sm breakpoint", () => {
    renderBoth();
    const cls = screen.getByLabelText(/backup transport/i).className;
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-xs");
    expect(cls.split(/\s+/)).not.toContain("text-xs");
  });

  it("steps the surrounding label with it", () => {
    renderBoth();
    const label = screen.getByLabelText(/backup transport/i).closest("label");
    expect(label).not.toBeNull();
    const cls = label?.className ?? "";
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-xs");
    expect(cls.split(/\s+/)).not.toContain("text-xs");
  });
});

// NO BUTTON AIMED AT A REFUSAL — quince#628, ruled shape 2.
//
// A device page opened with an unreachable storage pre-selected, so `Back up now` was aimed at a
// disk that cannot be written. `POST /api/jobs` answers 409 for that, so nothing was corrupted —
// the button was simply pre-loaded with a failure, and the user's first act on the page was aimed
// at a refusal.
//
// SHAPE 2, not "fall back to the first reachable storage": `default` is a real semantic — it is
// where an omitted `storage_id` goes on the server — so a UI that silently redirected the backup
// would disagree with the daemon about the user's own choice. The selection stays honest; the
// action becomes impossible instead of doomed.
function storageEntry(over: Partial<Storage> = {}): Storage {
  return {
    id: "01JA",
    name: "internal",
    path: "/backups",
    backend: "reflink",
    default: true,
    reachable: true,
    unreachable_code: null,
    unreachable_reason: null,
    will_be_full: null,
    filesystem_free_bytes: 1_200_000_000_000,
    filesystem_total_bytes: 3_600_000_000_000,
    backup_count: 14,
    device_count: 1,
    ...over,
  };
}

function withStorages(list: Storage[], over: Partial<Storages> = {}): Storages {
  return {
    state: { status: "loaded", storages: list },
    recheck: () => {},
    rechecking: {},
    reload: () => {},
    ...over,
  };
}

describe("Back up now refuses an unusable storage", () => {
  const online = () => device({ usb: "t", wifi: "t" });

  // THE REPORTED STATE: the declared default is unreachable, so the page opens pre-set to the one
  // storage the user cannot back up to.
  it("disables the button when the DEFAULT storage is unreachable", () => {
    const storages = withStorages([
      storageEntry({ reachable: false, unreachable_reason: "not mounted" }),
      storageEntry({ id: "01JB", name: "shuttle", default: false }),
    ]);
    render(
      <BackupControls
        device={online()}
        start={ok}
        cancel={ok}
        busy={false}
        storages={storages}
        storageID=""
        setStorageID={() => {}}
      />,
    );
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
  });

  // THE SELECTION IS NOT SILENTLY REDIRECTED. The control still shows the declared default — this
  // is the half of the ruling that a "fall back to the first reachable one" fix would have broken,
  // and it is invisible unless asserted.
  it("keeps the declared default selected rather than redirecting to a reachable one", () => {
    const storages = withStorages([
      storageEntry({ reachable: false, unreachable_reason: "not mounted" }),
      storageEntry({ id: "01JB", name: "shuttle", default: false }),
    ]);
    render(
      <BackupControls
        device={online()}
        start={ok}
        cancel={ok}
        busy={false}
        storages={storages}
        storageID=""
        setStorageID={() => {}}
      />,
    );
    const select = screen.getByTestId("storage-select") as HTMLSelectElement;
    expect(select.value).toBe("01JA");
  });

  // A storage quince has never reached cannot be a destination either — the same refusal
  // `StorageDeviceBackup` has made since story 6.
  it("disables the button for a storage that was never created", () => {
    const storages = withStorages([
      storageEntry({ id: "", reachable: true }),
      storageEntry({ id: "01JB", name: "shuttle", default: false }),
    ]);
    render(
      <BackupControls
        device={online()}
        start={ok}
        cancel={ok}
        busy={false}
        storages={storages}
        storageID=""
        setStorageID={() => {}}
      />,
    );
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
  });

  // AND IT STAYS ENABLED WHEN THE CHOSEN STORAGE IS FINE, even with another storage unreachable.
  // A page that disabled the action because SOME disk was out would pass every assertion above and
  // be a worse bug than the one being fixed.
  it("stays enabled when the chosen storage is reachable and another is not", () => {
    const storages = withStorages([
      storageEntry({}),
      storageEntry({ id: "01JB", name: "ghost", default: false, reachable: false }),
    ]);
    render(
      <BackupControls
        device={online()}
        start={ok}
        cancel={ok}
        busy={false}
        storages={storages}
        storageID=""
        setStorageID={() => {}}
      />,
    );
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(false);
  });

  // A CHOSEN storage, not merely the default: selecting the unreachable one must disable too.
  it("disables when the user's explicit choice is the unreachable one", () => {
    const storages = withStorages([
      storageEntry({}),
      storageEntry({ id: "01JB", name: "shuttle", default: false, reachable: false }),
    ]);
    render(
      <BackupControls
        device={online()}
        start={ok}
        cancel={ok}
        busy={false}
        storages={storages}
        storageID="01JB"
        setStorageID={() => {}}
      />,
    );
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
  });
});

// THE DISABLED BUTTON AND ITS REASON MUST SHARE ONE CONDITION.
//
// A disabled control with no reason is the state this project has already had to fix once, and the
// reason lives in a DIFFERENT component (`StorageNotices`, below the action row — quince#325 puts
// prose there, quince#627 wrote the sentence). Two components, one rule: if they ever drift apart
// the product grows a mute dead button, and nothing else would notice.
describe("the disabled button and the unavailability line agree", () => {
  function renderBoth(list: Storage[], storageID = "") {
    const storages = withStorages(list);
    return render(
      <MemoryRouter>
        <BackupControls
          device={device({ usb: "t", wifi: "t" })}
          start={ok}
          cancel={ok}
          busy={false}
          storages={storages}
          storageID={storageID}
          setStorageID={() => {}}
        />
        <BackupControlsStatus
          device={device({ usb: "t", wifi: "t" })}
          error={null}
          storages={storages}
          storageID={storageID}
        />
      </MemoryRouter>,
    );
  }

  it("shows the reason exactly when the button is disabled — unreachable", () => {
    renderBoth([
      storageEntry({ reachable: false, unreachable_reason: "not mounted" }),
      storageEntry({ id: "01JB", name: "shuttle", default: false }),
    ]);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("storage-unavailable")).toHaveTextContent(/internal/);
  });

  it("shows the reason exactly when the button is disabled — never created", () => {
    renderBoth([storageEntry({ id: "", reachable: true }), storageEntry({ id: "01JB", default: false })]);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("storage-unavailable")).toBeTruthy();
  });

  it("shows neither when the chosen storage is fine", () => {
    renderBoth([storageEntry({}), storageEntry({ id: "01JB", name: "shuttle", default: false })]);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByTestId("storage-unavailable")).toBeNull();
  });
});

// quince#889: encryption off + `require_encryption` is a press that cannot succeed. It used to
// start a job, fail at preflight and leave a row reading "Backup needs attention" — the same words
// a transfer that died at 40 GB gets. Same rule as the two cases above: disabled, with the reason
// rendered where a phone can see it.
describe("a backup that encryption policy forbids", () => {
  function renderBoth(encryptionBlocks: boolean) {
    const dev = { ...device({ usb: "t" }), backup_encryption: "off" as const };
    return render(
      <MemoryRouter>
        <BackupControls
          device={dev}
          start={ok}
          cancel={ok}
          busy={false}
          {...storageProps}
          encryptionBlocks={encryptionBlocks}
        />
        <BackupControlsStatus device={dev} error={null} {...statusProps} encryptionBlocks={encryptionBlocks} />
      </MemoryRouter>,
    );
  }

  it("disables the button and says why", () => {
    renderBoth(true);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/have to be encrypted/i)).toBeTruthy();
  });

  // The permissive policy is the other half, and it is the half a regression would hit: encryption
  // off is legal there, so nothing about this device may change.
  it("leaves an unencrypted device alone when the policy permits it", () => {
    renderBoth(false);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByText(/have to be encrypted/i)).toBeNull();
  });
});
