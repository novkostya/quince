import { describe, expect, it } from "vitest";
import { dueState } from "./backupDue";

// qn.12 D5/D7.2 — THE BADGE AND THE PUSH MUST AGREE.
//
// This rule exists twice by necessity: `core/internal/notify` decides what to send, this decides
// what to draw, and they run in different languages on different sides of the wire. A day-boundary
// disagreement between them reads as a bug in whichever one you saw second — so the thresholds are
// pinned here in the same terms the Go side uses (`age >= threshold`, both ranks).

const at = (iso: string) => ({ last_backup: { at: iso, job_id: null, status: "succeeded" } });
const NOW = new Date("2026-08-17T12:00:00Z");

describe("dueState", () => {
  it("says nothing about a device backed up inside the threshold", () => {
    expect(dueState(at("2026-08-16T12:00:00Z"), 3, 14, NOW)).toBe("fresh");
  });

  it("is due at exactly the staleness threshold, not a day later", () => {
    // `>=`, MATCHING THE NOTIFIER. An off-by-one here is invisible in isolation and shows up as the
    // badge and the push disagreeing for one day per lapse.
    expect(dueState(at("2026-08-14T12:00:00Z"), 3, 14, NOW)).toBe("due");
    expect(dueState(at("2026-08-14T12:00:01Z"), 3, 14, NOW)).toBe("fresh");
  });

  it("is overdue at exactly the overdue threshold", () => {
    expect(dueState(at("2026-08-03T12:00:00Z"), 3, 14, NOW)).toBe("overdue");
    expect(dueState(at("2026-08-03T12:00:01Z"), 3, 14, NOW)).toBe("due");
  });

  // A DEVICE WITH NO BACKUPS IS NOT OVERDUE. Its age is unbounded, so folding it into `overdue`
  // would greet a phone paired ninety seconds ago with a reproach — the same distinction the
  // notifier draws by inviting rather than scolding.
  it("ranks a never-backed-up device separately from an overdue one", () => {
    expect(dueState({ last_backup: null }, 3, 14, NOW)).toBe("never");
  });

  // AN UNREADABLE TIMESTAMP IS THE ABSENCE OF A CLAIM, not a claim of its own. Treating it as very
  // old would mark a device overdue forever on data quince cannot read; treating it as fresh would
  // hide a device that may genuinely be due.
  it("does not guess at an unreadable timestamp", () => {
    expect(dueState(at("not a timestamp"), 3, 14, NOW)).toBe("unknown");
  });

  // THE THRESHOLDS ARE CONFIGURABLE, so the rule must follow them rather than the defaults.
  it("follows the configured thresholds rather than baked-in numbers", () => {
    const iso = "2026-08-10T12:00:00Z"; // seven days before NOW
    expect(dueState(at(iso), 3, 14, NOW)).toBe("due");
    expect(dueState(at(iso), 10, 30, NOW)).toBe("fresh");
    expect(dueState(at(iso), 1, 5, NOW)).toBe("overdue");
  });
});
