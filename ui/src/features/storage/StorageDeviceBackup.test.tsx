import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { StorageDeviceBackup } from "./StorageDeviceBackup";
import type { Device, Storage } from "@/lib/types";

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
