import { describe, it, expect } from "vitest";
import { versionsOn } from "./StorageDetailsPage";
import type { Version } from "@/lib/types";

// qn.6d story 7 — the version list is scoped to ONE storage.
//
// This pins the RULE rather than mounting the page, which is the convention
// `DeviceDetailsWifiSync.test.tsx` sets: the page's own render is covered by e2e, and dragging the
// store/router surface in for a filter would test the framework instead of the decision.
//
// The decision worth pinning is the EMPTY-ID case. `Version.storage_id` is null until attributed and
// a never-created storage's id is `""` (quince#582), so a naive `v.storage_id === s.id` filter is
// correct for every storage that exists and quietly wrong for the one that does not.

function version(over: Partial<Version> = {}): Version {
  return {
    id: "01V1",
    udid: "UDID-A",
    backend: "reflink",
    zfs_snapshot: null,
    browse_root: "/backups/UDID-A/latest",
    created_at: "2026-08-01T10:00:00Z",
    job_id: null,
    kind: "full",
    encrypted: true,
    is_latest: true,
    structure_verified_at: null,
    content_verified_at: null,
    logical_bytes: 1000,
    physical_bytes: 1000,
    missing: false,
    storage_id: "01JSTORAGE-A",
    ...over,
  };
}

describe("versionsOn", () => {
  it("keeps only the versions attributed to this storage", () => {
    const all = [
      version({ id: "01V1", storage_id: "01JSTORAGE-A" }),
      version({ id: "01V2", storage_id: "01JSTORAGE-B" }),
      version({ id: "01V3", storage_id: "01JSTORAGE-A" }),
    ];
    expect(versionsOn("01JSTORAGE-A", all).map((v) => v.id)).toEqual(["01V1", "01V3"]);
    expect(versionsOn("01JSTORAGE-B", all).map((v) => v.id)).toEqual(["01V2"]);
  });

  it("never matches an UNATTRIBUTED version, whose storage is not yet known", () => {
    const all = [version({ id: "01V1", storage_id: null })];
    expect(versionsOn("01JSTORAGE-A", all)).toEqual([]);
  });

  // THE CASE THIS FUNCTION EXISTS FOR. A storage that was never created has id "" and genuinely has
  // no backups. Without the guard, `"" === null` is false in JS so unattributed rows would not match
  // either — but a future loosening (a `??  ""` anywhere upstream) would pair every unattributed
  // version with every never-created storage and invent a history for both.
  it("returns nothing for a storage that was never created, even with unattributed versions present", () => {
    const all = [
      version({ id: "01V1", storage_id: null }),
      version({ id: "01V2", storage_id: "" }),
      version({ id: "01V3", storage_id: "01JSTORAGE-A" }),
    ];
    expect(versionsOn("", all)).toEqual([]);
  });

  it("counts MISSING versions — a vanished artifact is still history on this storage", () => {
    const all = [version({ id: "01V1", storage_id: "01JSTORAGE-A", missing: true })];
    expect(versionsOn("01JSTORAGE-A", all).map((v) => v.id)).toEqual(["01V1"]);
  });
});
