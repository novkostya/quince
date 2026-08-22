import { describe, it, expect } from "vitest";
import type { DomainSummary } from "@/lib/types";
import { partitionByApp } from "./appSizes";

// EVERY IDENTIFIER HERE IS INVENTED (spec D8/D10).

const NOTES = "com.example.notes";
const HELPER = "com.example.notes.helper"; // a nested bundle id, on purpose
const READER = "com.example.reader";

function d(domain: string, files: number, bytes: number): DomainSummary {
  return { domain, files, bytes };
}

describe("partitionByApp", () => {
  // G3 — the reconciliation. This is the whole reason the partition is a pure function.
  it("counts every domain exactly once, so apps plus remainder equal the total", () => {
    const rows = [
      d(`AppDomain-${NOTES}`, 10, 1000),
      d(`AppDomainPlugin-${NOTES}.share`, 2, 200),
      d(`AppDomain-${READER}`, 5, 500),
      d("HomeDomain", 100, 9000),
      d("AppDomainGroup-group.example.shared", 3, 300),
      d("AppDomain-com.apple.something", 7, 700),
    ];
    const p = partitionByApp([NOTES, READER], rows);

    const appFiles = p.apps.reduce((n, a) => n + a.files, 0);
    const appBytes = p.apps.reduce((n, a) => n + a.bytes, 0);
    const appDomains = p.apps.reduce((n, a) => n + a.domains, 0);

    expect(appFiles + p.remainder.files).toBe(p.totals.files);
    expect(appBytes + p.remainder.bytes).toBe(p.totals.bytes);
    expect(appDomains + p.remainder.domains).toBe(p.totals.domains);
    expect(p.totals.domains).toBe(rows.length);
  });

  // A plugin container belongs to its app, so the size a user sees is the app's real footprint.
  it("folds an app's plugin container into that app", () => {
    const p = partitionByApp([NOTES], [
      d(`AppDomain-${NOTES}`, 10, 1000),
      d(`AppDomainPlugin-${NOTES}.share`, 2, 200),
    ]);
    const notes = p.apps.find((a) => a.bundleID === NOTES);
    expect(notes).toMatchObject({ files: 12, bytes: 1200, domains: 2 });
    expect(p.remainder.bytes).toBe(0);
  });

  // LONGEST MATCH. Without it the plugin's bytes land on the parent app — a silent
  // misattribution, not a visible failure, and the reason this is not a first-match loop.
  it("attributes a nested bundle id to the nested app, not to its prefix", () => {
    const p = partitionByApp([NOTES, HELPER], [
      d(`AppDomain-${NOTES}`, 1, 100),
      d(`AppDomain-${HELPER}`, 1, 900),
    ]);
    expect(p.apps.find((a) => a.bundleID === NOTES)).toMatchObject({ bytes: 100 });
    expect(p.apps.find((a) => a.bundleID === HELPER)).toMatchObject({ bytes: 900 });
  });

  // A BOUNDARY, NOT A PREFIX. `com.example.notes` must not claim a different app whose id
  // merely starts the same way.
  it("does not let one app claim another whose id starts the same way", () => {
    const p = partitionByApp([NOTES], [d("AppDomain-com.example.notesomething", 4, 400)]);
    expect(p.apps.find((a) => a.bundleID === NOTES)).toMatchObject({ bytes: 0, domains: 0 });
    expect(p.remainder).toMatchObject({ bytes: 400, domains: 1 });
  });

  // A SHARED GROUP CONTAINER IS NOBODY'S. Attributing it to one app would be a guess shown as
  // a measurement; splitting it would double-count and break G3.
  it("puts a shared app-group container in the remainder", () => {
    const p = partitionByApp([NOTES, READER], [
      d("AppDomainGroup-group.example.shared", 3, 300),
    ]);
    expect(p.remainder).toMatchObject({ bytes: 300, domains: 1 });
    for (const a of p.apps) expect(a.bytes).toBe(0);
  });

  // An app domain for a bundle the installed list does not name is REMAINDER, not a new row.
  // D3 rules what "apps" means; inventing a row answers a different question from the label.
  it("does not invent an app row for a bundle Info.plist never listed", () => {
    const p = partitionByApp([NOTES], [d("AppDomain-com.apple.internal.thing", 9, 900)]);
    expect(p.apps).toHaveLength(1);
    expect(p.apps[0].bundleID).toBe(NOTES);
    expect(p.remainder).toMatchObject({ bytes: 900, domains: 1 });
  });

  // An installed app with NO domains is a real state — present in Info.plist, no data here —
  // and it is distinct from a small app. `domains: 0` is what lets the surface say so.
  it("keeps an installed app with no data, marked as having no domains", () => {
    const p = partitionByApp([NOTES, READER], [d(`AppDomain-${NOTES}`, 1, 100)]);
    const reader = p.apps.find((a) => a.bundleID === READER);
    expect(reader).toMatchObject({ bytes: 0, files: 0, domains: 0 });
  });

  // Biggest first — the screen answers "what is taking the space".
  it("orders apps by size", () => {
    const p = partitionByApp([NOTES, READER], [
      d(`AppDomain-${NOTES}`, 1, 100),
      d(`AppDomain-${READER}`, 1, 900),
    ]);
    expect(p.apps.map((a) => a.bundleID)).toEqual([READER, NOTES]);
  });

  // No rows at all — an unlocked backup with an empty manifest reconciles trivially rather
  // than throwing.
  it("reconciles on an empty backup", () => {
    const p = partitionByApp([NOTES], []);
    expect(p.totals).toMatchObject({ files: 0, bytes: 0, domains: 0 });
    expect(p.remainder).toMatchObject({ files: 0, bytes: 0, domains: 0 });
  });
});
