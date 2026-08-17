import type { Device, Job, Op, Version, WSEnvelope } from "@/lib/types";
import type { DeviceEvent, HelloEvent, JobLogEvent, SessionLockedEvent } from "./types";
import { useConnectionStore } from "@/stores/connection";
import { useDevicesStore } from "@/stores/devices";
import { useJobsStore } from "@/stores/jobs";
import { useOpsStore } from "@/stores/ops";
import { useSessionStore } from "@/stores/session";
import { useVersionsStore } from "@/stores/versions";

// dispatch routes one envelope into the feature stores. Unknown types are ignored so new
// event kinds from a newer server never break an older client.
export function dispatch(env: WSEnvelope): void {
  // EVERY frame carries the server's clock, so the correction stays fresh rather than being taken
  // once at connect and left to drift. `ts` is on the envelope already (contracts §3), so this
  // needs nothing new on the wire, and the store ignores changes under 2 s.
  const serverMs = Date.parse(env.ts);
  if (!Number.isNaN(serverMs)) {
    useConnectionStore.getState().setServerOffset(serverMs - Date.now());
  }
  switch (env.type) {
    case "hello":
      useConnectionStore.getState().setServerVersion((env.data as HelloEvent).server_version);
      break;
    case "device.attached":
    case "device.updated":
      useDevicesStore.getState().upsert(env.data as Device);
      break;
    case "device.detached": {
      const d = env.data as DeviceEvent;
      useDevicesStore.getState().removeTransport(d.udid, d.transport);
      break;
    }
    case "job.updated":
      useJobsStore.getState().upsert(env.data as Job);
      break;
    case "job.log": {
      const l = env.data as JobLogEvent;
      useJobsStore.getState().appendLog(l.job_id, l.chunk);
      break;
    }
    case "version.created":
      useVersionsStore.getState().upsert(env.data as Version);
      break;
    case "version.deleted":
      useVersionsStore.getState().remove((env.data as Version).id);
      break;
    case "session.locked": {
      const s = env.data as SessionLockedEvent;
      useSessionStore.getState().drop(s.session_id, s.reason);
      break;
    }
    case "op.updated":
      useOpsStore.getState().upsert(env.data as Op);
      break;
    default:
      break;
  }
}
