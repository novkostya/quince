import { beforeEach, describe, expect, it, vi } from "vitest";
import { refreshAll } from "./refresh";
import { api, APIError } from "./api";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
import type { Device, Job, Version } from "./types";

// Spread the real module: `refresh` pulls `api` out of it and the error classes are used as
// rejection values, so a narrow factory would leave `APIError` undefined.
vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return { ...actual, api: { ...actual.api, get: vi.fn(), getText: vi.fn() } };
});

const get = vi.mocked(api.get);
const getText = vi.mocked(api.getText);

const DEVICE = { udid: "DEV-1", name: "phone", paired: "yes" } as unknown as Device;
const DONE = { id: "J1", udid: "DEV-1", state: "succeeded" } as unknown as Job;
const RUNNING = { id: "J2", udid: "DEV-1", state: "backing_up" } as unknown as Job;
const VERSION = { id: "V1", udid: "DEV-1" } as unknown as Version;

// forbidden is the refusal `GET /api/devices` gives a device-scoped principal — `adminOnly` in
// scope_routes.go, by spec D8. It is a guaranteed answer for that principal, not a flake.
const forbidden = () => Promise.reject(new APIError(403, "forbidden", "this is not your device"));

// answers wires one reply per collection. A collection whose entry is omitted rejects, which is how
// each test states what quince could NOT read.
function answers(r: { devices?: unknown; jobs?: Job[]; versions?: Version[] }) {
  get.mockImplementation((path: string) => {
    if (path === "/api/devices") {
      return (r.devices === undefined ? forbidden() : Promise.resolve({ devices: r.devices })) as never;
    }
    if (path === "/api/jobs") {
      return (r.jobs === undefined
        ? Promise.reject(new Error("jobs unavailable"))
        : Promise.resolve({ jobs: r.jobs, next_cursor: null })) as never;
    }
    if (path === "/api/versions") {
      return (r.versions === undefined
        ? Promise.reject(new Error("versions unavailable"))
        : Promise.resolve({ versions: r.versions })) as never;
    }
    return Promise.reject(new Error(`unexpected GET ${path}`)) as never;
  });
}

const jobIDs = () => Object.keys(useJobsStore.getState().byId);

beforeEach(() => {
  get.mockReset();
  getText.mockReset();
  getText.mockResolvedValue("");
  vi.spyOn(console, "warn").mockImplementation(() => {});
  useDevicesStore.getState().replaceAll([]);
  useJobsStore.getState().replaceAll([]);
  useVersionsStore.getState().replaceAll([]);
});

describe("refreshAll", () => {
  it("fills all three stores when every collection answers", async () => {
    answers({ devices: [DEVICE], jobs: [DONE], versions: [VERSION] });
    await refreshAll();
    expect(Object.keys(useDevicesStore.getState().byUdid)).toEqual(["DEV-1"]);
    expect(jobIDs()).toEqual(["J1"]);
    expect(useVersionsStore.getState().order).toEqual(["V1"]);
  });

  // quince#1523, the reported bug: a scoped holder's Home rendered two empty lists over rows that
  // had already arrived. The devices refusal is the ONLY failure here, and it is guaranteed.
  it("keeps the jobs and versions it read when the devices list is refused", async () => {
    answers({ jobs: [DONE], versions: [VERSION] });
    await refreshAll();
    expect(jobIDs()).toEqual(["J1"]);
    expect(useVersionsStore.getState().order).toEqual(["V1"]);
  });

  // The other direction, so the guard is not a special case for one endpoint.
  it("keeps the devices and versions it read when the jobs list fails", async () => {
    answers({ devices: [DEVICE], versions: [VERSION] });
    await refreshAll();
    expect(Object.keys(useDevicesStore.getState().byUdid)).toEqual(["DEV-1"]);
    expect(useVersionsStore.getState().order).toEqual(["V1"]);
    expect(jobIDs()).toEqual([]);
  });

  it("leaves a collection's last state alone rather than emptying it", async () => {
    useVersionsStore.getState().replaceAll([VERSION]);
    answers({ devices: [DEVICE], jobs: [DONE] });
    await refreshAll();
    expect(useVersionsStore.getState().order).toEqual(["V1"]);
  });

  it("says which collection went stale rather than failing silently", async () => {
    answers({ jobs: [DONE], versions: [VERSION] });
    await refreshAll();
    expect(vi.mocked(console.warn).mock.calls[0]?.[0]).toContain("devices");
  });

  it("still backfills a running job's log", async () => {
    getText.mockResolvedValue("line one\nline two\n");
    answers({ devices: [DEVICE], jobs: [RUNNING], versions: [VERSION] });
    await refreshAll();
    expect(getText).toHaveBeenCalledWith("/api/jobs/J2/log");
    expect(useJobsStore.getState().logByJobId.J2).toEqual(["line one", "line two"]);
  });

  // A log backfill is a repair of one pane. Losing it must not cost the rows the same response
  // carried — the failure this whole file is about, one level down.
  it("keeps the jobs it read when a log backfill fails", async () => {
    getText.mockRejectedValue(new Error("log unavailable"));
    answers({ devices: [DEVICE], jobs: [RUNNING], versions: [VERSION] });
    await expect(refreshAll()).resolves.toBeUndefined();
    expect(jobIDs()).toEqual(["J2"]);
  });
});
