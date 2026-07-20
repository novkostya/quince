import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { BackupControls } from "./BackupControls";
import type { Device, Job, Transports } from "@/lib/types";

function device(transports: Transports): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone16,1",
    ios_version: "26.0.1",
    transports,
    paired: "yes",
    backup_encryption: "on",
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
  };
}

const ok = () => Promise.resolve(true);

describe("BackupControls", () => {
  it("starts a backup over auto by default", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t" })} start={start} cancel={ok} busy={false} error={null} />);
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("auto");
  });

  it("disables the button and explains when the device is on no transport", () => {
    render(<BackupControls device={device({})} start={ok} cancel={ok} busy={false} error={null} />);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/connect the device/i)).toBeTruthy();
  });

  it("offers a transport override only when the device is on both transports", () => {
    const { rerender } = render(
      <BackupControls device={device({ usb: "t" })} start={ok} cancel={ok} busy={false} error={null} />,
    );
    expect(screen.queryByLabelText(/backup transport/i)).toBeNull();
    rerender(
      <BackupControls device={device({ usb: "t", wifi: "t" })} start={ok} cancel={ok} busy={false} error={null} />,
    );
    expect(screen.getByLabelText(/backup transport/i)).toBeTruthy();
  });

  it("passes the selected transport when overridden", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t", wifi: "t" })} start={start} cancel={ok} busy={false} error={null} />);
    fireEvent.change(screen.getByLabelText(/backup transport/i), { target: { value: "wifi" } });
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("wifi");
  });

  it("shows cancel for a running job and surfaces the shared error", () => {
    const cancel = vi.fn().mockResolvedValue(true);
    render(
      <BackupControls
        device={device({ wifi: "t" })}
        activeJob={runningJob()}
        start={ok}
        cancel={cancel}
        busy={false}
        error="a backup is already running for this device"
      />,
    );
    fireEvent.click(screen.getByTestId("cancel-backup"));
    expect(cancel).toHaveBeenCalledWith("J1");
    expect(screen.getByRole("alert").textContent).toContain("already running");
  });
});
