import { Link } from "react-router-dom";
import { ShieldAlert, Usb, Wifi, WifiOff } from "lucide-react";
import type { Device, Job } from "@/lib/types";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { modelLine } from "./modelName";
import { RelativeTime } from "@/components/RelativeTime";
import { newestRunningJob, useJobsStore } from "@/stores/jobs";
import { useVersionsStore } from "@/stores/versions";
import { JobProgressInline } from "@/features/jobs/JobProgress";
import { useBackup } from "@/features/jobs/useBackup";
import type { DueState } from "@/lib/backupDue";

// THE CARD IS SILENT WHEN THINGS ARE FINE AND SPEAKS WHEN THEY ARE NOT (quince#625, Operator).
//
// `Encrypted` used to render here as an `ok` badge — the loudest element on the card, in the slot
// the eye reaches first, reporting the EXPECTED state of a standing setting. On a screen whose job
// is "which of my devices are backed up, and can I back one up now", the most prominent position
// went to a fact that is true of every healthy device and never changes between visits.
//
// `Not encrypted` is the opposite and is KEPT, unchanged: it is the one encryption state worth
// interrupting for, and the card is where a user would want to be told.
//
// WHAT IS LOST, deliberately: Home no longer distinguishes `on` from `unknown` — both render
// nothing. `unknown` is the muxd-minimal, pre-qn.3-lockdown state, so the remaining ambiguity is
// between "encrypted" and "we could not read lockdown yet", never between encrypted and NOT. The
// positive state still shows on the device DETAILS page (`encryption: on`), checked rather than
// assumed.
//
// Same rule qn.6d gives storage cards — quiet when healthy, loud and self-explaining when a disk is
// out (quince#443) — so the two card families now agree rather than differing.
function EncryptionBadge({ state }: { state: Device["backup_encryption"] }) {
  if (state === "off") {
    return (
      <Badge tone="warn">
        <ShieldAlert size={12} /> Not encrypted
      </Badge>
    );
  }
  // "on", and "unknown" (muxd-minimal, before qn.3 lockdown) — no badge either way.
  return null;
}

// BackupStatus is the one line under the transports: real history if any (with a live, hover-exact
// timestamp), else a state-appropriate placeholder.
function BackupStatus({ device }: { device: Device }) {
  if (device.last_backup) {
    return (
      <>
        Last backup <RelativeTime iso={device.last_backup.at} /> · {device.last_backup.status}
      </>
    );
  }
  return <>{device.paired === "yes" ? "No backups yet" : "Not set up yet"}</>;
}

// DueBadge is the assisted model's in-app half (qn.12, spec D7.2): the same judgement the notifier
// makes, on the screen, for the person whose phone can never receive a push.
//
// IT FOLLOWS THE CARD'S OWN RULE — silent when things are fine, speaking when they are not. `fresh`
// renders nothing, which is why this is not another always-on badge in the slot the eye reaches
// first (quince#625).
//
// `never` IS NOT `overdue`, and the distinction is the notifier's: a device paired ninety seconds
// ago has unbounded age, so the naive rule greets it with a reproach.
//
// THE STATE ARRIVES AS A PROP AND IS NOT FETCHED HERE. The first version called `useConfig` inside
// this component, which broke all eighteen of the card's own tests — `No QueryClient set` — and the
// breakage was the honest signal: `DeviceCard` is presentational, rendered N times in a grid, and a
// query per card is both a new dependency for every test that renders one and N subscriptions for
// one answer. The list reads the config once (`DashboardPage`) and passes the verdict down.
//
// `undefined` RENDERS NOTHING, which is what a caller with no config yet should produce. Inventing a
// threshold to judge against would make this a claim quince does not have the inputs for.
function DueBadge({ due }: { due?: DueState }) {
  switch (due) {
    case "overdue":
      return <Badge tone="danger">Overdue</Badge>;
    case "due":
      return <Badge tone="warn">Due</Badge>;
    case "never":
      // NOT a warning tone. Never-backed-up is the ordinary state of a device somebody just paired,
      // and colouring it as a problem would make first run look broken.
      return <Badge tone="accent">Not backed up</Badge>;
    // `fresh`, `unknown` and absent render nothing — the first because there is nothing to say, the
    // second because quince does not know and must not guess. `BackupStatus` still shows whatever
    // timestamp exists, so an unreadable one stays visible without being judged.
    default:
      return null;
  }
}

// isFailed marks a terminal attempt the user should act on (assisted model — a failed newest attempt
// must be visible or the soak is worthless, gate-11 finding #6, (cj)).
function isFailed(state: Job["state"]): boolean {
  return state === "failed" || state === "connection_lost";
}

export function DeviceCard({ device, due }: { device: Device; due?: DueState }) {
  const jobsForDevice = (s: { byId: Record<string, Job> }) =>
    Object.values(s.byId).filter((j) => j.udid === device.udid);
  const activeJob = useJobsStore((s) => newestRunningJob(jobsForDevice(s)));
  // The newest attempt for the device (by start time) — its failure drives the "needs attention" line.
  const newestJob = useJobsStore((s) =>
    jobsForDevice(s).reduce<Job | undefined>(
      (newest, j) => (!newest || j.started_at > newest.started_at ? j : newest),
      undefined,
    ),
  );
  // Non-missing versions this device actually holds (the card's "N backups" count, qn.6a).
  const versionCount = useVersionsStore(
    (s) => s.order.filter((id) => s.byId[id]?.udid === device.udid && !s.byId[id]?.missing).length,
  );

  const { start, busy, error } = useBackup(device.udid);
  const present = Boolean(device.transports.usb || device.transports.wifi);
  const subtitle = modelLine(device.model, device.ios_version);
  // Surface a failed newest attempt only when nothing is currently running (a running job already
  // narrates itself) and last_backup (last SUCCESS) doesn't cover it.
  const attention = !activeJob && newestJob && isFailed(newestJob.state) ? newestJob : undefined;

  return (
    // h-full + flex column so the primary action pins to the BOTTOM (mt-auto below): grid rows
    // stretch cards to equal height, so the buttons then line up across cards even when one has an
    // extra "needs attention" line (qn.6a soak fix).
    //
    // min-w-0 closes the same chain quince#631 closed on Settings, one level up from the `min-w-0`
    // on the name column below: a grid item defaults to `min-width: auto`, so this card would not
    // shrink below the intrinsic width of its longest line — and a marketing name is arbitrary-
    // length text off the wire — so the grid column, and every card in it, grew to match. The
    // inner `truncate` is dead code until the card itself can shrink.
    <Card data-testid="device-card" className="flex h-full min-w-0 flex-col">
      <CardContent className="flex flex-1 flex-col p-4 sm:p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <Link
              to={`/devices/${device.udid}`}
              className="text-sm font-semibold tracking-tight hover:text-accent"
            >
              {device.name || device.udid}
            </Link>
            {subtitle ? <div className="truncate text-xs text-muted">{subtitle}</div> : null}
          </div>
          {/* Both badges sit in the card's "speaks when things are not fine" slot, and both are
              silent in the healthy case — so a card with nothing wrong is unchanged. */}
          <div className="flex shrink-0 items-center gap-1.5">
            <DueBadge due={due} />
            <EncryptionBadge state={device.backup_encryption} />
          </div>
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {device.transports.usb ? (
            <Badge tone="ok">
              <Usb size={12} /> USB
            </Badge>
          ) : null}
          {device.transports.wifi ? (
            <Badge tone="ok">
              <Wifi size={12} /> Wi-Fi
            </Badge>
          ) : null}
          {!present ? (
            <Badge tone="neutral">
              <WifiOff size={12} /> Offline
            </Badge>
          ) : null}
        </div>

        <div className="mt-3 text-xs text-muted">
          <BackupStatus device={device} />
        </div>
        <div className="mt-1 flex flex-wrap gap-x-3 text-xs text-subtle">
          <span>
            {versionCount} {versionCount === 1 ? "backup" : "backups"}
          </span>
          {!present && device.last_seen ? (
            <span>
              last seen <RelativeTime iso={device.last_seen} />
            </span>
          ) : null}
        </div>

        {/* Exactly ONE primary action per card (qn.6a soak fix — a "needs attention" line PLUS a
            separate "Back up now" was two buttons doing the same thing). When the newest attempt
            failed, Retry IS that action and replaces Back up now, with the failure as context.

            THE BUTTON IS THE LAST CHILD OF EVERY BRANCH, AND THAT IS THE ALIGNMENT (quince#512).
            `mt-auto` pins the WRAPPER to the card bottom; it says nothing about where the button
            sits inside it. The branches used to disagree — offline was `[Button, reason]`, attention
            `[message, Button, error?]`, default `[Button, error?]` — so the wrapper's bottom edge
            aligned and the button's did not, across three distinct baselines. Two cards side by
            side put the same control at visibly different heights.

            Ending every branch with its button makes alignment INDEPENDENT of the caption: the
            button's bottom edge is the wrapper's bottom edge however tall the text above it is,
            however many lines it wraps to, and whether or not it is there at all. A reserved
            fixed-height caption slot was the alternative, and it is a guess about that caption's
            height — correct until a longer string, a bigger font or a translation silently breaks
            it back into this bug.

            THIS IS THE INVARIANT A FUTURE BRANCH WILL BREAK, so it is stated rather than implied:
            `mt-auto` pins the wrapper; the buttons align because each branch ENDS with its button.

            The two conditional `error` spans are the easy half to miss — a card only misaligned
            AFTER a failed click, which is exactly when the user is least served by things moving. */}
        <div className="mt-auto pt-4">
          {activeJob ? (
            // Out of scope for that rule, deliberately: a card actively backing up has no button at
            // all and is in a different state. It must not acquire one to satisfy an alignment rule.
            <JobProgressInline job={activeJob} />
          ) : !present ? (
            // Offline: a disabled "Back up now" WITH a reason (never a dead button), so the card
            // keeps its shape (Operator ruling, (ch)/(bq)). The reason is shown inline as well as in
            // the title — a hover title alone is invisible on a phone.
            //
            // THE REASON SITS ABOVE THE BUTTON NOW, and that RESTORES the ruling rather than
            // altering it. The comment here used to say "same shape as an online card so the layout
            // stays aligned" — right about the ruling and wrong about the code, which is the more
            // dangerous way to be wrong, because it reads as authority. The caption underneath was
            // precisely what pushed this branch's button a line higher than every other card's.
            <div className="flex flex-col gap-1">
              <span className="text-xs text-muted">Connect it over USB or Wi-Fi to back it up.</span>
              <Button
                size="sm"
                disabled
                title="Connect the device to back it up"
                data-testid="card-backup-now"
              >
                Back up now
              </Button>
            </div>
          ) : device.paired !== "yes" ? (
            // Pairing is USB-only and narrated (Trust + passcode), so it lives on the device's
            // details page (qn.3); the card routes there carrying a pair INTENT (router state) so the
            // details page auto-opens the dialog — the click delivers on its label (qn.4b fix, (bq)).
            <Button asChild size="sm" variant="outline">
              <Link to={`/devices/${device.udid}`} state={{ pair: true }}>
                Pair
              </Link>
            </Button>
          ) : attention ? (
            // This branch already led with its message, which is the precedent (a) finishes
            // applying rather than introduces. What moved is the `error` span: it followed the
            // button, so a retry that failed dropped this card's baseline below every other one.
            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-danger">Last attempt needs attention</span>
              {error ? (
                <span className="text-xs text-danger" role="alert">
                  {error}
                </span>
              ) : null}
              <Button
                size="sm"
                onClick={() => void start("auto", { retryOf: attention.id })}
                disabled={busy}
                data-testid="card-retry"
              >
                {busy ? "Starting…" : "Retry backup"}
              </Button>
            </div>
          ) : (
            // The common case, and the one whose `error` span is easiest to overlook because it is
            // almost always absent: a healthy card was already aligned, and only fell out of line
            // once a click had failed.
            <div className="flex flex-col gap-1">
              {error ? (
                <span className="text-xs text-danger" role="alert">
                  {error}
                </span>
              ) : null}
              <Button
                size="sm"
                onClick={() => void start("auto")}
                disabled={busy}
                data-testid="card-backup-now"
              >
                {busy ? "Starting…" : "Back up now"}
              </Button>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
