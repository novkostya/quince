import { describe, it, expect, beforeEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { DeviceCard } from "./DeviceCard";
import { useJobsStore } from "@/stores/jobs";
import type { Device, Job } from "@/lib/types";

// qn.4c findings (iv)+(v): the dashboard card must narrate the phase it is actually in — NOT
// linger on "Backing up 100%" through verify+commit — and must show a device's real last backup
// instead of "No backups yet" once the server tells the truth (`last_backup` derived from the
// newest committed version, contracts §2 (bz)).

function device(over: Partial<Device> = {}): Device {
  return {
    udid: "DEV-1",
    name: "test-iphone",
    model: "iPhone16,1",
    ios_version: "26.0.1",
    transports: { usb: "2026-07-20T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    last_seen: "2026-07-20T00:00:00Z",
    last_backup: null,
    ...over,
  };
}

function job(state: Job["state"], percent: number | null): Job {
  return {
    id: "J1",
    udid: "DEV-1",
    kind: "backup",
    transport: "usb",
    state,
    progress: {
      phase: state,
      percent,
      bytes_done: 2,
      bytes_total: 2,
      files_received: 9,
      liveness: "active",
    },
    started_at: "2026-07-20T00:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "J1",
    attempt: 1,
    version_id: null,
  };
}

function renderCard(dev: Device) {
  return render(
    <MemoryRouter>
      <DeviceCard device={dev} />
    </MemoryRouter>,
  );
}

describe("DeviceCard", () => {
  beforeEach(() => {
    useJobsStore.setState({ byId: {}, logByJobId: {} });
  });

  it("narrates verify and commit instead of lingering on 'Backing up' at 100% (finding iv)", () => {
    useJobsStore.getState().upsert(job("backing_up", 100));
    renderCard(device());
    expect(screen.getByText("Backing up")).toBeTruthy();

    // The live WS path: a job.updated arrives, the store changes, the card re-renders itself.
    act(() => useJobsStore.getState().upsert(job("verifying", 100)));
    expect(screen.queryByText("Backing up")).toBeNull();
    expect(screen.getByText("Verifying")).toBeTruthy();

    act(() => useJobsStore.getState().upsert(job("committing", 100)));
    expect(screen.getByText("Committing")).toBeTruthy();
  });

  it("shows the real last backup once the job is done, not 'No backups yet' (finding v)", () => {
    useJobsStore.getState().upsert({ ...job("succeeded", 100), finished_at: "2026-07-20T01:00:00Z" });
    renderCard(device({ last_backup: { at: "2026-07-20T01:00:00Z", job_id: "J1", status: "succeeded" } }));
    expect(screen.queryByText(/no backups yet/i)).toBeNull();
    expect(screen.getByText(/last backup/i)).toBeTruthy();
    expect(screen.queryByText("Backing up")).toBeNull();
  });

  it("says 'No backups yet' only when the device really has none", () => {
    renderCard(device());
    expect(screen.getByText(/no backups yet/i)).toBeTruthy();
  });

  it("renders a last backup derived from an adopted version (null job_id, contracts §2)", () => {
    renderCard(device({ last_backup: { at: "2026-07-19T00:00:00Z", job_id: null, status: "succeeded" } }));
    expect(screen.getByText(/last backup/i)).toBeTruthy();
  });
});
