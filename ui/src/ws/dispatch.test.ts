import { beforeEach, describe, expect, it, vi } from "vitest";
import { dispatch } from "./dispatch";
import type { DeviceEvent } from "./types";
import type { Device, Job, Version, WSEnvelope } from "@/lib/types";
import { useConnectionStore } from "@/stores/connection";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useMessagesIndexingStore } from "@/stores/messagesIndexing";
import { useVersionsStore } from "@/stores/versions";
import { configKey } from "@/lib/config";
import { queryClient } from "@/lib/queryClient";

function env(type: string, data: unknown): WSEnvelope {
  return { type, ts: "2026-07-18T00:00:00Z", data };
}

function mkDevice(over: Partial<Device> = {}): Device {
  return {
    udid: "u1",
    name: "phone",
    model: "iPhone17,2",
    ios_version: "26.0",
    transports: { wifi: "2026-07-18T00:00:00Z" },
    paired: "yes",
    backup_encryption: "on",
    wifi_sync: "unknown",
    notifications_enabled: true,
    last_seen: "2026-07-18T00:00:00Z",
    last_backup: null,
    ...over,
  };
}

function mkJob(over: Partial<Job> = {}): Job {
  return {
    id: "j1",
    udid: "u1",
    kind: "backup",
    transport: "wifi",
    state: "backing_up",
    progress: {
      phase: "receiving",
      percent: 10,
      bytes_done: 0,
      bytes_total: 100,
      files_received: 0,
      liveness: "active",
    },
    started_at: "2026-07-18T00:00:00Z",
    finished_at: null,
    error: null,
    retry_of: null,
    intent_id: "j1",
    attempt: 1,
    version_id: null,
    storage_id: null,
    ...over,
  };
}

beforeEach(() => {
  useDevicesStore.getState().replaceAll([]);
  useJobsStore.getState().replaceAll([]);
  useVersionsStore.getState().replaceAll([]);
  useConnectionStore.setState({ serverVersion: null });
});

describe("dispatch", () => {
  it("device.attached upserts", () => {
    dispatch(env("device.attached", { ...mkDevice(), transport: "wifi" } as DeviceEvent));
    expect(useDevicesStore.getState().order).toEqual(["u1"]);
  });

  it("device.detached removes the transport and vanishes when empty", () => {
    dispatch(env("device.attached", { ...mkDevice(), transport: "wifi" } as DeviceEvent));
    dispatch(env("device.detached", { ...mkDevice(), transport: "wifi" } as DeviceEvent));
    expect(useDevicesStore.getState().order).toEqual([]);
  });

  it("job.updated upserts and job.log appends with a cap", () => {
    dispatch(env("job.updated", mkJob()));
    for (let i = 0; i < 600; i++) {
      dispatch(env("job.log", { job_id: "j1", chunk: `line ${i}` }));
    }
    expect(useJobsStore.getState().byId["j1"].state).toBe("backing_up");
    expect(useJobsStore.getState().logByJobId["j1"].length).toBe(500);
    expect(useJobsStore.getState().logByJobId["j1"][499]).toBe("line 599");
  });

  it("version.created and version.deleted", () => {
    const v = { id: "v1", udid: "u1" } as Version;
    dispatch(env("version.created", v));
    expect(useVersionsStore.getState().order).toEqual(["v1"]);
    dispatch(env("version.deleted", v));
    expect(useVersionsStore.getState().order).toEqual([]);
  });

  it("hello sets the server version", () => {
    dispatch(env("hello", { server_version: "9.9.9", time: "t" }));
    expect(useConnectionStore.getState().serverVersion).toBe("9.9.9");
  });

  it("ignores unknown event types", () => {
    expect(() => dispatch(env("something.new", {}))).not.toThrow();
  });
});

// `config.updated` IS THE ONLY THING THAT REFRESHES AN OPEN PAGE (quince#1162, Operator ruling
// 2026-08-17 option C). `refetchOnWindowFocus` is false app-wide and `useConfig` sets no interval,
// so if this case stops invalidating, a hand-edit applies on the server and every open tab keeps
// showing the old document until somebody reloads by hand — silently, with every other test green.
describe("dispatch on config.updated", () => {
  it("invalidates the config query", () => {
    const seen: unknown[] = [];
    const spy = vi
      .spyOn(queryClient, "invalidateQueries")
      .mockImplementation(((args: unknown) => {
        seen.push(args);
        return Promise.resolve();
      }) as typeof queryClient.invalidateQueries);

    dispatch(env("config.updated", {}));

    expect(seen).toEqual([{ queryKey: configKey }]);
    spy.mockRestore();
  });

  // THE PAYLOAD IS EMPTY BY RULING and the client must not grow a dependency on it. A dispatcher
  // that read `env.data` would break the moment the server sends `{}` — which is what it sends.
  it("does not read the payload", () => {
    const spy = vi
      .spyOn(queryClient, "invalidateQueries")
      .mockImplementation((() => Promise.resolve()) as typeof queryClient.invalidateQueries);

    expect(() => dispatch(env("config.updated", undefined))).not.toThrow();
    expect(spy).toHaveBeenCalledTimes(1);
    spy.mockRestore();
  });
});

describe("messages.indexing", () => {
  beforeEach(() => {
    useMessagesIndexingStore.setState({ bySession: {} });
  });

  it("records the live count against its own session", () => {
    dispatch(env("messages.indexing", { session_id: "S1", udid: "DEV-A", messages: 40000 }));
    expect(useMessagesIndexingStore.getState().bySession["S1"]).toBe(40000);
  });

  it("keeps two sessions' counts apart", () => {
    // THE KEY IS WHY THIS CANNOT GO WRONG rather than a convention. One quince can hold several
    // unlocked sessions, and a count from another one rendered against this thread would be a
    // number about somebody else's backup.
    dispatch(env("messages.indexing", { session_id: "S1", udid: "DEV-A", messages: 10 }));
    dispatch(env("messages.indexing", { session_id: "S2", udid: "DEV-B", messages: 20 }));
    expect(useMessagesIndexingStore.getState().bySession).toEqual({ S1: 10, S2: 20 });
  });

  it("drops the count when its session locks — story 6", () => {
    dispatch(env("messages.indexing", { session_id: "S1", udid: "DEV-A", messages: 40000 }));
    dispatch(env("messages.indexing", { session_id: "S2", udid: "DEV-B", messages: 5 }));

    dispatch(env("session.locked", { session_id: "S1", reason: "user" }));

    // Only the locked one. A lock is about one session, and taking the other down with it would
    // be a different bug in the same file.
    expect(useMessagesIndexingStore.getState().bySession).toEqual({ S2: 5 });
  });
});
