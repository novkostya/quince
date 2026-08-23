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

// messages.indexing reports how far the Messages projection scan has got (qn.10 D2/D3, quince#1515).
//
// NO TOTAL, AND ITS ABSENCE IS THE DESIGN. The parser does not count rows before streaming them, so
// a percentage would be invented — a surface renders an indeterminate indicator carrying this live
// count. Throttled server-side to <=2/s, the same promise job.updated makes.
export interface MessagesIndexingEvent {
  session_id: string;
  udid: string;
  messages: number;
}
