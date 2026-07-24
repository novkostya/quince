import { describe, it, expect, beforeEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { DeviceCard } from "./DeviceCard";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
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
    useVersionsStore.setState({ byId: {}, order: [] });
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

  // qn.6a offline devices: a device with no transports shows an "Offline" badge, a last-seen line,
  // and a DISABLED "Back up now" that still explains why (never a dead button, Operator ruling (ch)).
  it("an offline device shows a disabled 'Back up now' with a reason and a last-seen line", () => {
    renderCard(
      device({ transports: {}, last_seen: "2026-07-19T00:00:00Z", last_backup: { at: "2026-07-19T00:00:00Z", job_id: "J0", status: "succeeded" } }),
    );
    expect(screen.getByText("Offline")).toBeTruthy();
    expect(screen.getByText(/last seen/i)).toBeTruthy();
    const btn = screen.getByTestId("card-backup-now") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(screen.getByText(/connect it over usb or wi-fi/i)).toBeTruthy();
  });

  // qn.6a #6 (CORE): a failed newest attempt must be visible — the card shows a "needs attention"
  // line with a Retry, while last_backup still shows the older SUCCESS (a soak whose failures are
  // invisible is worthless, (cj)).
  it("surfaces a needs-attention Retry when the newest attempt failed", () => {
    useJobsStore.getState().upsert({
      ...job("connection_lost", 41),
      id: "J9",
      started_at: "2026-07-21T00:00:00Z",
      finished_at: "2026-07-21T00:01:00Z",
    });
    renderCard(device({ last_backup: { at: "2026-07-20T00:00:00Z", job_id: "J1", status: "succeeded" } }));
    expect(screen.getByText(/needs attention/i)).toBeTruthy();
    expect(screen.getByTestId("card-retry")).toBeTruthy();
    // Retry is the SINGLE primary action — it REPLACES "Back up now", not sits beside it (soak fix).
    expect(screen.queryByTestId("card-backup-now")).toBeNull();
    // last_backup (last SUCCESS) is still shown — the failure line is context, not a mutation.
    expect(screen.getByText(/last backup/i)).toBeTruthy();
  });

  // No needs-attention line when the newest attempt succeeded.
  it("shows no needs-attention line when the newest attempt succeeded", () => {
    useJobsStore.getState().upsert({ ...job("succeeded", 100), id: "J8", finished_at: "2026-07-21T00:01:00Z" });
    renderCard(device({ last_backup: { at: "2026-07-21T00:01:00Z", job_id: "J8", status: "succeeded" } }));
    expect(screen.queryByText(/needs attention/i)).toBeNull();
  });

  // The card's "N backups" count comes from the versions store (non-missing versions for this udid).
  it("counts the device's non-missing versions", () => {
    act(() => {
      useVersionsStore.getState().replaceAll([
        { ...ver("V1", "DEV-1"), is_latest: true },
        ver("V2", "DEV-1"),
        { ...ver("V3", "DEV-1"), missing: true }, // a dead version doesn't count as a backup you have
        ver("VX", "OTHER"),
      ]);
    });
    renderCard(device());
    expect(screen.getByText(/^2 backups$/)).toBeTruthy();
  });
});

function ver(id: string, udid: string) {
  return {
    id,
    udid,
    backend: "reflink" as const,
    zfs_snapshot: null,
    browse_root: `/backups/${udid}/latest`,
    created_at: "2026-07-20T00:00:00Z",
    job_id: "J1",
    kind: "full" as const,
    encrypted: true,
    is_latest: false,
    structure_verified_at: "2026-07-20T00:00:00Z",
    content_verified_at: null,
    logical_bytes: 100,
    physical_bytes: 10,
    missing: false,
  };
}
