import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { BackupControls, BackupControlsStatus } from "./BackupControls";
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
  };
}

const ok = () => Promise.resolve(true);

describe("BackupControls", () => {
  it("starts a backup over auto by default", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t" })} start={start} cancel={ok} busy={false} />);
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("auto");
  });

  // The "and explains" half of this test moved to the status block below, which is where the
  // sentence now renders. The button being disabled is still this component's claim.
  it("disables the button when the device is on no transport", () => {
    render(<BackupControls device={device({})} start={ok} cancel={ok} busy={false} />);
    expect((screen.getByTestId("backup-now") as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers a transport override only when the device is on both transports", () => {
    const { rerender } = render(
      <BackupControls device={device({ usb: "t" })} start={ok} cancel={ok} busy={false} />,
    );
    expect(screen.queryByLabelText(/backup transport/i)).toBeNull();
    rerender(
      <BackupControls device={device({ usb: "t", wifi: "t" })} start={ok} cancel={ok} busy={false} />,
    );
    expect(screen.getByLabelText(/backup transport/i)).toBeTruthy();
  });

  it("passes the selected transport when overridden", () => {
    const start = vi.fn().mockResolvedValue(true);
    render(<BackupControls device={device({ usb: "t", wifi: "t" })} start={start} cancel={ok} busy={false} />);
    fireEvent.change(screen.getByLabelText(/backup transport/i), { target: { value: "wifi" } });
    fireEvent.click(screen.getByTestId("backup-now"));
    expect(start).toHaveBeenCalledWith("wifi");
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
      <BackupControls device={device({})} start={ok} cancel={ok} busy={false} />,
    );
    expect(container.textContent).not.toMatch(/connect the device to back it up/i);
  });

  // No test that a REFUSAL stays out of the row: `error` is no longer a prop of BackupControls, so
  // the exclusion is enforced by the type rather than by an assertion. A test here could only pass a
  // string the component cannot accept, which would assert nothing.

  // The other direction: the lines must still be RENDERED somewhere, or this "fix" is a deletion.
  it("still renders both lines, in the block that sits below the row", () => {
    render(<BackupControlsStatus device={device({})} error="nope" />);
    expect(screen.getByText(/connect the device to back it up/i)).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toBe("nope");
  });

  // While a job runs, "why the button is disabled" is not a thing to say — the button is Cancel.
  it("drops the offline reason while a job is running, but keeps the refusal", () => {
    render(
      <BackupControlsStatus device={device({})} activeJob={runningJob()} error="nope" />,
    );
    expect(screen.queryByText(/connect the device to back it up/i)).toBeNull();
    expect(screen.getByRole("alert").textContent).toBe("nope");
  });
});
