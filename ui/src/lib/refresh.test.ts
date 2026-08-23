import { beforeEach, describe, expect, it, vi } from "vitest";
import { refreshAll } from "./refresh";
import { api, APIError } from "./api";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
import type { AuthStatus, Device, Job, Version } from "./types";
import { authStatusKey } from "./auth";
import { queryClient } from "./queryClient";

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
  // `replaceAll` clears `byId` and NOT `logByJobId`, so a bare reset leaks a log into the next
  // test — which is how the per-job backfill case below first passed for the wrong reason.
  useJobsStore.setState({ byId: {}, logByJobId: {} });
  useVersionsStore.getState().replaceAll([]);
  queryClient.clear();
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

// A scoped session must not ask for the route it is refused. `GET /api/devices` is `adminOnly` by
// spec D8; the device page's own route is what this principal holds. quince#1523, second half.
describe("refreshAll under a device-scoped session", () => {
  beforeEach(() => {
    queryClient.setQueryData(authStatusKey, {
      state: "authenticated",
      csrf_token: "t",
      scope: { udid: "DEV-1" },
    } as AuthStatus);
  });

  it("fetches its own device instead of the admin list", async () => {
    answers({ jobs: [DONE], versions: [VERSION] });
    get.mockImplementation((path: string) => {
      if (path === "/api/devices") return forbidden() as never;
      if (path === "/api/devices/DEV-1") return Promise.resolve(DEVICE) as never;
      if (path === "/api/jobs") return Promise.resolve({ jobs: [DONE], next_cursor: null }) as never;
      if (path === "/api/versions") return Promise.resolve({ versions: [VERSION] }) as never;
      return Promise.reject(new Error(`unexpected GET ${path}`)) as never;
    });

    await refreshAll();

    expect(get).not.toHaveBeenCalledWith("/api/devices");
    expect(get).toHaveBeenCalledWith("/api/devices/DEV-1");
    expect(Object.keys(useDevicesStore.getState().byUdid)).toEqual(["DEV-1"]);
    expect(jobIDs()).toEqual(["J1"]);
    expect(useVersionsStore.getState().order).toEqual(["V1"]);
  });

  // The store the admin path fills from a list is filled here from one device, so everything
  // downstream — the device page, the WS upserts that follow — sees the same shape.
  it("leaves the console quiet, because nothing was refused", async () => {
    get.mockImplementation((path: string) => {
      if (path === "/api/devices/DEV-1") return Promise.resolve(DEVICE) as never;
      if (path === "/api/jobs") return Promise.resolve({ jobs: [DONE], next_cursor: null }) as never;
      if (path === "/api/versions") return Promise.resolve({ versions: [VERSION] }) as never;
      return Promise.reject(new Error(`unexpected GET ${path}`)) as never;
    });
    await refreshAll();
    expect(console.warn).not.toHaveBeenCalled();
  });
});

// An unauthenticated or unreadable cache reads as ADMIN, per `scopeOfSession` — the direction that
// fails safe, because over-asking costs a refusal quince now absorbs.
describe("refreshAll when the session's scope is unknown", () => {
  it("asks for the admin list", async () => {
    queryClient.setQueryData(authStatusKey, undefined);
    answers({ devices: [DEVICE], jobs: [DONE], versions: [VERSION] });
    await refreshAll();
    expect(get).toHaveBeenCalledWith("/api/devices");
    expect(jobIDs()).toEqual(["J1"]);
  });
});

// quince#1524's finding, and the FIRST test here is the one that refutes half of it. The verdict
// said one failing log fetch drops the other's backfill; it does not, because `Promise.all`
// aggregates rather than cancels. That test passes against the old code and is kept as a CONTROL —
// the shape it rules out is the one everybody expects to find.
describe("recoverRunningLogs settles per job", () => {
  const OTHER = { id: "J3", udid: "DEV-2", state: "backing_up" } as unknown as Job;

  beforeEach(() => {
    get.mockImplementation((path: string) => {
      if (path === "/api/devices") return Promise.resolve({ devices: [DEVICE] }) as never;
      if (path === "/api/jobs") {
        return Promise.resolve({ jobs: [RUNNING, OTHER], next_cursor: null }) as never;
      }
      if (path === "/api/versions") return Promise.resolve({ versions: [VERSION] }) as never;
      return Promise.reject(new Error(`unexpected GET ${path}`)) as never;
    });
  });

  it("keeps one device's backfill when the other device's log fetch fails", async () => {
    getText.mockImplementation((path: string) =>
      path === "/api/jobs/J2/log"
        ? Promise.reject(new Error("log unavailable"))
        : Promise.resolve("kept\n"),
    );

    await refreshAll();

    expect(useJobsStore.getState().logByJobId.J3).toEqual(["kept"]);
    expect(useJobsStore.getState().logByJobId.J2).toBeUndefined();
    expect(jobIDs()).toEqual(["J2", "J3"]);
  });

  it("names the job whose log is short, not the group", async () => {
    getText.mockImplementation((path: string) =>
      path === "/api/jobs/J2/log"
        ? Promise.reject(new Error("log unavailable"))
        : Promise.resolve("kept\n"),
    );

    await refreshAll();

    const said = vi.mocked(console.warn).mock.calls.map((c) => String(c[0])).join("\n");
    expect(said).toContain("J2");
    expect(said).not.toContain("J3");
  });
});

// THE ORDERING IS THE REAL DEFECT, and it is not the one the review named — see the file comment.
// `refreshAll` must not resolve until every backfill has settled, because `ws/client.ts` replays the
// events it queued during the refresh in `.finally()`. `setLog` replaces a log WHOLESALE, so a
// backfill that lands after the replay discards the chunks that were replayed.
describe("recoverRunningLogs and the replay that follows it", () => {
  const OTHER = { id: "J3", udid: "DEV-2", state: "backing_up" } as unknown as Job;

  it("has not resolved until a slow backfill lands, even with a failing sibling", async () => {
    get.mockImplementation((path: string) => {
      if (path === "/api/devices") return Promise.resolve({ devices: [DEVICE] }) as never;
      if (path === "/api/jobs") {
        return Promise.resolve({ jobs: [RUNNING, OTHER], next_cursor: null }) as never;
      }
      if (path === "/api/versions") return Promise.resolve({ versions: [VERSION] }) as never;
      return Promise.reject(new Error(`unexpected GET ${path}`)) as never;
    });
    // J2 rejects immediately; J3 resolves later. Under `Promise.all` the outer await rejects at J2
    // and `refreshAll` returns with J3 still in flight.
    getText.mockImplementation((path: string) => {
      if (path === "/api/jobs/J2/log") return Promise.reject(new Error("log unavailable"));
      return new Promise<string>((resolve) => setTimeout(() => resolve("slow\n"), 20));
    });

    await refreshAll();

    expect(useJobsStore.getState().logByJobId.J3).toEqual(["slow"]);
  });
});
