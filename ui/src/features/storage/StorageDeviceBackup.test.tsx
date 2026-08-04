import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StorageDeviceBackup } from "./StorageDeviceBackup";
import type { Device, Job, Storage } from "@/lib/types";
import { useJobsStore } from "@/stores/jobs";

// qn.6d story 6 / G3 — `Back up now` on a storage page means back up to THIS storage.
//
// G3's claim is about **the job the button creates, not what is rendered**, so these assert on the
// arguments `start` receives. The end-to-end half — that the daemon stores that storage on the job —
// is `story7` (PR 7); this pins the client's half of the same claim.

const start = vi.fn(async () => true);
vi.mock("@/features/jobs/useBackup", () => ({
  useBackup: () => ({ start, cancel: vi.fn(), busy: false, error: null }),
}));

function device(over: Partial<Device> = {}): Device {
  return {
    udid: "UDID-A",
    name: "spare-iphone",
    model: "iPhone17,2",
    ios_version: "18.5",
    transports: { usb: { since: "2026-08-01T10:00:00Z" } },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "unknown",
    last_seen: "2026-08-03T10:00:00Z",
    last_backup: null,
    ...over,
  } as Device;
}

function storage(over: Partial<Storage> = {}): Storage {
  return {
    id: "01JSTORAGE-A",
    name: "shuttle",
    path: "/mnt/shuttle",
    backend: "reflink",
    default: false,
    reachable: true,
    unreachable_code: null,
    unreachable_reason: null,
    will_be_full: null,
    filesystem_free_bytes: 1_000,
    filesystem_total_bytes: 2_000,
    backup_count: 0,
    device_count: 0,
    ...over,
  };
}

beforeEach(() => start.mockClear());

describe("StorageDeviceBackup", () => {
  // THE CLAIM. The destination is the page, not a selection — story 9's dropdown answered by
  // context. If this ever passes a different id, or none, the user backs up somewhere they did not
  // choose, and the UI would look identical.
  it("starts a job bound to THIS storage", () => {
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    fireEvent.click(screen.getByTestId("storage-device-backup"));

    expect(start).toHaveBeenCalledTimes(1);
    expect(start).toHaveBeenCalledWith("auto", { storageID: "01JSTORAGE-A" });
  });

  // REFUSES BEFORE THE SERVER HAS TO. A storage whose resolution did not succeed never accepts a job
  // (Slot.Usable), so offering the action would earn a 409 and teach the user the button is
  // unreliable rather than that the disk is unplugged.
  it("is disabled with a reason when the storage is not connected", () => {
    render(
      <StorageDeviceBackup
        device={device()}
        storage={storage({ reachable: false, unreachable_code: "missing_medium" })}
      />,
    );
    expect(screen.getByTestId("storage-device-backup")).toBeDisabled();
    expect(screen.getByTestId("storage-device-backup-blocked")).toHaveTextContent("plug this storage in");

    fireEvent.click(screen.getByTestId("storage-device-backup"));
    expect(start).not.toHaveBeenCalled();
  });

  // A storage that was never created has no id, so it cannot be a job's destination — and sending
  // `storageID: ""` would ask the daemon to resolve the DEFAULT, silently backing up somewhere else.
  it("is disabled when the storage has never been created", () => {
    render(<StorageDeviceBackup device={device()} storage={storage({ id: "" })} />);
    expect(screen.getByTestId("storage-device-backup")).toBeDisabled();
    expect(screen.getByTestId("storage-device-backup-blocked")).toHaveTextContent("never reached");
    fireEvent.click(screen.getByTestId("storage-device-backup"));
    expect(start).not.toHaveBeenCalled();
  });

  it("is disabled with a reason when the device is on no transport", () => {
    render(<StorageDeviceBackup device={device({ transports: {} })} storage={storage()} />);
    expect(screen.getByTestId("storage-device-backup")).toBeDisabled();
    expect(screen.getByTestId("storage-device-backup-blocked")).toHaveTextContent("USB or Wi-Fi");
  });
});

// THE CARD MUST KNOW ABOUT THE JOB, NOT THE REQUEST — quince#639, ruled.
//
// `Back up now` started a backup and showed nothing. `busy` is "the POST is in flight", not "a
// backup is running", so the button disabled for a few hundred milliseconds and came back with the
// same label while the job ran invisibly. The only feedback the page could produce was the 409 from
// pressing it a second time — the component teaching that the button is unreliable, which is the
// exact lesson its own header comment exists to prevent.
//
// The ruled table, pinned:
//
//   running job's storage_id | this page shows
//   -------------------------|--------------------------------------
//   THIS storage             | JobProgressInline, as Home does
//   ANOTHER storage          | button disabled, with the reason
//
// A progress bar for the second case would assert "a backup is arriving HERE" on the one page whose
// whole premise is that the destination is the page. That is a state-honesty violation, not a
// layout preference.
function runningJob(over: Partial<Job> = {}): Job {
  return {
    id: "J-RUN",
    udid: "UDID-A",
    kind: "backup",
    transport: "usb",
    state: "backing_up",
    progress: {
      phase: "receiving",
      percent: 42,
      bytes_done: 1,
      bytes_total: 2,
      files_received: 7,
      liveness: "active",
    },
    started_at: "2026-08-04T10:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "J-RUN",
    attempt: 1,
    version_id: null,
    storage_id: "01JSTORAGE-A",
    ...over,
  } as Job;
}

describe("StorageDeviceBackup reflects the JOB, not the request", () => {
  beforeEach(() => {
    useJobsStore.setState({ byId: {}, logByJobId: {} });
  });

  it("shows progress when the running job is aimed at THIS storage", () => {
    useJobsStore.getState().upsert(runningJob());
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    expect(screen.getByTestId("storage-device-progress")).toBeTruthy();
    // And the button is gone entirely — a progress bar and an action offering to start another
    // would be two controls for one thing.
    expect(screen.queryByTestId("storage-device-backup")).toBeNull();
  });

  // THE CASE THAT COULD NOT BE COPIED FROM HOME. Home has one destination; this page is one
  // destination among several, so a job aimed elsewhere must NOT render as arriving here.
  it("does not claim progress for a job aimed at ANOTHER storage", () => {
    useJobsStore.getState().upsert(runningJob({ storage_id: "01JSTORAGE-OTHER" }));
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    expect(screen.queryByTestId("storage-device-progress")).toBeNull();
    expect((screen.getByTestId("storage-device-backup") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("storage-device-backup-blocked")).toHaveTextContent(
      /already backing up/,
    );
  });

  // An unresolvable destination says "already backing up" rather than naming a storage: "to <the
  // default>" would be a claim the job never made. Same restraint DeviceCard shows for a
  // storage_id it cannot resolve.
  it("refuses without naming a destination when the job carries no storage_id", () => {
    useJobsStore.getState().upsert(runningJob({ storage_id: null }));
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    expect(screen.queryByTestId("storage-device-progress")).toBeNull();
    expect((screen.getByTestId("storage-device-backup") as HTMLButtonElement).disabled).toBe(true);
  });

  // A TERMINAL job is not a running one. Without this, a finished backup would keep the button
  // disabled forever — the mirror-image defect of the one being fixed.
  it("re-enables once the job is no longer running", () => {
    useJobsStore.getState().upsert(runningJob({ state: "succeeded", finished_at: "2026-08-04T10:05:00Z" }));
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    expect(screen.queryByTestId("storage-device-progress")).toBeNull();
    expect((screen.getByTestId("storage-device-backup") as HTMLButtonElement).disabled).toBe(false);
  });

  // A job for a DIFFERENT DEVICE must not touch this row. Each card is one device, and single-flight
  // is per device — reading "is anything running" would disable every card on the page.
  it("ignores a job belonging to another device", () => {
    useJobsStore.getState().upsert(runningJob({ id: "J-OTHER", udid: "UDID-B" }));
    render(<StorageDeviceBackup device={device()} storage={storage()} />);
    expect((screen.getByTestId("storage-device-backup") as HTMLButtonElement).disabled).toBe(false);
  });

  // An unreachable storage still wins: the ladder is ordered, and "plug the disk in" is more
  // actionable than "already backing up" when both are true.
  it("keeps the storage reason ahead of the job reason", () => {
    useJobsStore.getState().upsert(runningJob({ storage_id: "01JSTORAGE-OTHER" }));
    render(
      <StorageDeviceBackup
        device={device()}
        storage={storage({ reachable: false, unreachable_reason: "not mounted" })}
      />,
    );
    expect(screen.getByTestId("storage-device-backup-blocked")).toHaveTextContent(/plug this storage in/);
  });
});
