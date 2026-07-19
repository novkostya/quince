import type { Device } from "@/lib/types";

// device.attached / device.detached carry the Device plus the transport edge that changed.
export interface DeviceEvent extends Device {
  transport: string;
}

export interface JobLogEvent {
  job_id: string;
  chunk: string;
}

export interface SessionLockedEvent {
  session_id: string;
  reason: string;
}

export interface HelloEvent {
  server_version: string;
  time: string;
}
