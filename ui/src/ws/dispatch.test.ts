import { beforeEach, describe, expect, it } from "vitest";
import { dispatch } from "./dispatch";
import type { DeviceEvent } from "./types";
import type { Device, Job, Version, WSEnvelope } from "@/lib/types";
import { useConnectionStore } from "@/stores/connection";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";

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
