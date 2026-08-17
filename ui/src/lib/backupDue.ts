import type { Device } from "@/lib/types";

// Is a device due for a backup? (qn.12, spec D5 and D7.2)
//
// THE CARD ALREADY SHOWS *WHEN* THE LAST BACKUP WAS; THIS IS THE JUDGEMENT. A relative timestamp is
// a fact the reader has to evaluate against a threshold they cannot see — and the threshold is
// configurable, so they genuinely cannot. Rendering the verdict is what makes the same rule the
// notifier uses visible to somebody who never gets a notification.
//
// WHICH IS THE POINT, AND IT IS NOT REDUNDANCY. For a Lockdown Mode user this is the WHOLE assisted
// loop — WebKit disables Web Push declaratively on any certificate, so no notification can reach
// them and the in-app surface is all there is (quince#510, spec D7). For everyone else it is a
// second way to see the same thing.
//
// A PURE FUNCTION, TAKING `now`, so the same rule can be asserted at a chosen instant and reused by
// the status surface without either copy drifting from the notifier's.

export type DueState =
  /// Backed up recently enough. No affordance.
  | "fresh"
  /// Past `staleness_days` — the notifier's `backup_available` rank.
  | "due"
  /// Past `overdue_days` — the notifier's `backup_overdue` rank.
  | "overdue"
  /// No committed version at all. RANKED SEPARATELY FROM `overdue`, deliberately: a device paired
  /// ninety seconds ago has unbounded age, and greeting it with a reproach is the same defect the
  /// notifier avoids by inviting rather than scolding.
  | "never"
  /// quince cannot tell — an unreadable timestamp. NOT folded into `never` or `overdue`, because
  /// both are claims and this is the absence of one.
  | "unknown";

export function dueState(
  device: Pick<Device, "last_backup">,
  stalenessDays: number,
  overdueDays: number,
  now: Date,
): DueState {
  if (!device.last_backup) return "never";
  const at = Date.parse(device.last_backup.at);
  if (Number.isNaN(at)) return "unknown";

  const days = (now.getTime() - at) / 86_400_000;
  // `>=` ON BOTH, matching the notifier's `age >= threshold`. A day-boundary disagreement between
  // the badge and the push is the kind of thing that reads as a bug in whichever one you saw second.
  if (days >= overdueDays) return "overdue";
  if (days >= stalenessDays) return "due";
  return "fresh";
}
