import * as React from "react";
import type { Device, Job } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { StorageSelect, StorageNotices } from "./StorageSelect";
import { chosenStorage, type Storages } from "./useStorages";
import type { RequestTransport } from "./useBackup";

interface BackupControlsProps {
  device: Device;
  activeJob?: Job;
  start: (transport: RequestTransport, opts?: { storageID?: string; retryOf?: string }) => Promise<boolean>;
  cancel: (jobId: string) => Promise<boolean>;
  busy: boolean;
  storages: Storages;
  storageID: string;
  setStorageID: (id: string) => void;
  // encryptionBlocks: this quince requires encrypted backups and this device's encryption is off,
  // so a press can only fail at preflight (quince#889). A plain boolean rather than the page's
  // tri-state: "we have not read the policy yet" and "the policy permits it" both mean *do not
  // disable the button*, and the difference only matters to the sentence the page renders.
  encryptionBlocks?: boolean;
}

// BackupControls is the assisted "Back up now" action on a device's details page. It starts a backup
// over the chosen transport, offers a transport override only when the device is present on both,
// and cancels a running job.
//
// The transport it sends is `auto` — the engine resolves it, design §4/(bp) — EXCEPT where the
// override renders, which is exactly where `auto` had no meaning left to carry. See
// `effectiveTransport` below for why that is one derived value rather than two (quince#653). The
// started/cancelled job renders from the WS job.updated stream; this never fabricates progress
// (ui.design.md). start/cancel/busy are lifted to the page so Retry shares the same state.
//
// It renders BUTTONS ONLY. Everything block-level that used to stack underneath — the reason the
// button is disabled, the shared refusal — lives in BackupControlsStatus below, because this
// component is an item in the page's action row and a flex item is as wide as its widest child.
// Dropping the `error` prop rather than merely not rendering it is deliberate: it makes the rule
// structural, so the row cannot regain a text line without a type error (quince#325).
export function BackupControls({
  device,
  activeJob,
  start,
  cancel,
  busy,
  storages,
  storageID,
  setStorageID,
  encryptionBlocks = false,
}: BackupControlsProps) {
  const [transport, setTransport] = React.useState<RequestTransport>("auto");
  // storages is LIFTED to the page (see DeviceDetailsPage) so the control and the notices below
  // the row share one fetch. Same reason start/cancel/busy are lifted: two components, one truth.

  const onUSB = Boolean(device.transports.usb);
  const onWifi = Boolean(device.transports.wifi);
  const present = onUSB || onWifi;
  const onBoth = onUSB && onWifi;

  // THE TRANSPORT THE SELECTOR SHOWS AND THE TRANSPORT A PRESS SENDS ARE ONE VALUE (quince#653).
  //
  // `Auto` is gone from the option list because it cannot mean a third thing where that list is
  // rendered: the selector only mounts when the device is on BOTH, and `resolveTransport` resolves
  // `auto` to USB whenever USB is present. So `Auto` was a second label for `USB`, and "Back up now
  // over Auto" is not a sentence.
  //
  // THE STATE DEFAULT STAYS "auto", and that is the trap. It is load-bearing for the case where the
  // selector does NOT render: a device on one transport shows no selector, and `auto` is what must
  // be sent so the engine resolves it to whichever transport the device is actually on. Defaulting
  // the state to "usb" would make a Wi-Fi-only device send `usb`, which `resolveTransport` returns
  // UNCHECKED — the job starts and then waits out the window for a device that was never going to
  // appear. An immediate, actionable state becomes a job that hangs.
  //
  // So the concrete value is DERIVED rather than stored, once, for both uses:
  //
  //   - not on both → "auto", whatever the state holds. This is also the mitigation for the one
  //     thing dropping `Auto` could have cost: presence can change while the page is open, and a
  //     user who explicitly picked USB before the cable came out would otherwise send `usb` to a
  //     device that is now Wi-Fi-only. Derived from CURRENT presence rather than reset by an
  //     effect, so there is no window in which the stale choice is still sendable.
  //   - on both, untouched → "usb", which is what `auto` already resolved to here. Behaviour-
  //     preserving, and the control now names the transport it will actually use.
  //   - on both, chosen → the choice.
  const effectiveTransport: RequestTransport = !onBoth
    ? "auto"
    : transport === "auto"
      ? "usb"
      : transport;

  // The storage a RUNNING job is writing to, by name. Only named when the list is loaded and the
  // job carries an id we recognise — an unresolvable id says nothing rather than guessing, because
  // "to <the default>" would be a claim the job never made.
  const activeStorageName =
    activeJob?.storage_id && storages.state.status === "loaded"
      ? (storages.state.storages.find((s) => s.id === activeJob.storage_id)?.name ?? null)
      : null;

  // NO BUTTON AIMED AT A REFUSAL (quince#628, ruled shape 2).
  //
  // The selector keeps showing the DECLARED DEFAULT even when it is unreachable — deliberately.
  // Falling back to the first reachable storage would make the UI quietly disagree with the server
  // about what `default` means, and `default` is a real semantic: it is where an omitted
  // `storage_id` goes on `POST /api/jobs`. A UI that silently redirects a backup somewhere the user
  // did not choose is worse than one that shows an unusable selection.
  //
  // So the selection stays honest and the ACTION becomes impossible instead of doomed. `POST
  // /api/jobs` answers 409 for an unreachable storage, so the old button was not dangerous — it was
  // pre-loaded with a failure, and the user's first act on the page was aimed at a refusal.
  //
  // THIS IS THE PATTERN THE PRODUCT ALREADY USES, twice: the offline DEVICE case on `DeviceCard`
  // (a disabled button carrying its reason, never a dead one), and `StorageDeviceBackup` on the
  // storage page, which has refused an unreachable storage since story 6. This is the third, so a
  // user learns one rule rather than three.
  //
  // The REASON is not duplicated here. `StorageNotices` renders one short line naming the storage
  // and linking to it (quince#627), below the action row where prose belongs (quince#325). That
  // sentence is what makes this disabled button honest rather than mute, and a second copy in a
  // `title` would be two strings to keep in step — plus a hover title is invisible on a phone,
  // which is the primary client.
  // THE SHARED RESOLVER, not a third copy of the same two lines (quince#647). This is where the
  // third copy lived: an empty `storageID` means nothing was chosen, and `""` is also the id of a
  // storage quince has never reached, so a bare `find` selected that storage on an untouched page.
  // The button must be asking about the SAME storage the selector displays and the notice names.
  const chosen =
    storages.state.status === "loaded"
      ? chosenStorage(storages.state.storages, storageID)
      : undefined;
  // An id of "" means quince has never reached that storage, so it cannot be a destination yet —
  // the same refusal `StorageDeviceBackup` already makes, for the same reason.
  const storageUnusable =
    chosen !== undefined && (!chosen.reachable || chosen.id === "");


  if (activeJob) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" onClick={() => void cancel(activeJob.id)} data-testid="cancel-backup">
          Cancel backup
        </Button>
        {/* WHERE, not just HOW (Operator, 2026-08-02, from the G9 run). This said "backing up over
            wifi" and stopped there — which was the whole truth while there was one storage and is
            half of it now. `Job.storage_id` has been on the wire since story 6 and nothing
            rendered it, so a user with two disks watching a transfer could not tell which one was
            filling. Resolved through the storage list rather than shown as an id: the id is
            stable identity, the name is what the user wrote. */}
        <span className="text-xs text-muted" data-testid="active-job-line">
          backing up over {activeJob.transport}
          {activeStorageName ? ` to ${activeStorageName}` : ""}
        </span>
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
        {/* THE THIRD MEMBER OF THE SAME PATTERN, not a new one: a press that cannot succeed is a
            disabled button carrying its reason, as with an offline device and an unreachable
            storage. Encryption off under `require_encryption` was the one knowable-in-advance
            refusal still offered — it started a job, failed at preflight, and left a row reading
            "Backup needs attention" beside real transfer failures (quince#889). */}
        <Button
          onClick={() => void start(effectiveTransport, { storageID: storageID || undefined })}
          disabled={!present || busy || storageUnusable || encryptionBlocks}
          title={
            !present
              ? "Connect the device over USB or Wi-Fi to back it up"
              : encryptionBlocks
                ? "Encrypted backups are required here — turn on encryption for this device to back it up"
                : storageUnusable
                  ? `${chosen?.name ?? "That storage"} is unavailable — backups can't be written to it right now`
                  : undefined
          }
          data-testid="backup-now"
        >
          {busy ? "Starting…" : "Back up now"}
        </Button>
        {onBoth ? (
          // 16px on phones, 12px from `sm` up, label stepping with the control — the same shape as
          // `StorageSelect`, which carries the full reasoning (quince#616). At 12px this was a
          // 1.33x page zoom on tap, and it sits directly beside "Back up now" on the phone.
          <label className="text-base text-muted sm:text-xs">
            over{" "}
            <select
              className="rounded-md border border-line bg-card px-1.5 py-1 text-base text-fg sm:text-xs"
              value={effectiveTransport}
              onChange={(e) => setTransport(e.target.value as RequestTransport)}
              aria-label="Backup transport"
            >
              <option value="usb">USB</option>
              <option value="wifi">Wi-Fi</option>
            </select>
          </label>
        ) : null}
        <StorageSelect
          storages={storages}
          value={storageID}
          onChange={setStorageID}
          disabled={busy}
        />
    </div>
  );
}

// BackupControlsStatus is the block-level status under the action row: why the button is disabled,
// and any refusal from the last attempt.
//
// It is a SEPARATE component because of where it must render, not because of what it says. These
// lines used to sit inside BackupControls' own flex column, which is an item in the page's action
// row — and a flex item is as wide as its widest child. "Connect the device to back it up." is
// wider than "Back up now", so the column took the sentence's width and pushed "Manage encryption"
// out by the overhang, leaving a large gap between two buttons that should sit side by side.
//
// That is why the gap only ever appeared on an OFFLINE device: the sentence is the only thing that
// renders it, so a connected device had nothing to widen the column with (quince#325, reported from
// a screenshot). Keeping the text inside the row and constraining its width would trade one layout
// bug for a wrapped sentence under a narrow button; below the row it is free to be a full line, and
// the row is left holding only buttons.
export function BackupControlsStatus({
  storages,
  storageID,
  device,
  activeJob,
  error,
  encryptionBlocks = false,
}: {
  device: Device;
  activeJob?: Job;
  error: string | null;
  storages: Storages;
  storageID: string;
  encryptionBlocks?: boolean;
}) {
  const present = Boolean(device.transports.usb || device.transports.wifi);
  return (
    <>
      {!activeJob && !present ? (
        <p className="text-xs text-muted">Connect the device to back it up.</p>
      ) : null}
      {/* The reason lives here rather than only in the button's `title`, for the same reason every
          other disabled-button sentence does: a hover title is invisible on a phone, and the phone
          is the primary client (quince#325). It names the remedy — the Enable encryption button in
          the banner above — because "encryption is required" without one is a dead end. */}
      {!activeJob && present && encryptionBlocks ? (
        <p className="text-xs text-muted">
          Backups here have to be encrypted. Turn on encryption for this device to back it up.
        </p>
      ) : null}
      {/* The storage sentences render HERE, under the row, not beside the select — quince#325's
          rule, which StorageSelect had reintroduced a breach of. While a job runs they are
          suppressed: the row already says which storage is filling, and a full-transfer warning
          about a transfer in progress is a cost being reported after it started. */}
      {!activeJob ? <StorageNotices storages={storages} value={storageID} /> : null}
      {error ? (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      ) : null}
    </>
  );
}
