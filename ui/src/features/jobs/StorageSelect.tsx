import * as React from "react";
import { Link } from "react-router-dom";
import { chosenStorage, type Storages } from "./useStorages";
// StorageSelect is the "where does this backup go" CONTROL on Back up now (qn.6c story 9).
//
// IT RENDERS ONLY THE CONTROL. Every sentence it used to emit — the full-transfer warning, the
// unreachable reasons, the failed-load line — now lives in StorageNotices, below the action row.
//
// THAT SPLIT IS quince#325's DEFECT, WHICH THIS COMPONENT REINTRODUCED. A flex item is as wide as
// its widest child, and this control is an item in the page's action row. "First backup to shuttle
// — this transfers everything, not just what changed." is far wider than a `<select>`, so the
// column took the sentence's width and pushed `Manage encryption` out by the overhang — the exact
// gap quince#325 fixed for "Connect the device to back it up." and documented on
// BackupControlsStatus fifteen lines from here. Reported from a screenshot of the staging stand
// during G9, which is the second time the same lesson arrived the same way.
//
// So the rule the comment on BackupControlsStatus states is now STRUCTURAL for this row: the row
// holds controls, prose goes below it. A sentence added back here will re-break the layout.
export function StorageSelect({
  storages: sub,
  value,
  onChange,
  disabled,
}: {
  storages: Storages;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  const { state } = sub;
  const storages = state.status === "loaded" ? state.storages : [];
  // `chosenStorage`, not a bare `find` by id (quince#647). An empty `value` means NOTHING WAS
  // CHOSEN — and `""` is also the real id of a storage quince has never reached, so the naive match
  // selected THAT storage on an untouched page and the default fallback never ran. The guard now
  // lives in one place instead of three.
  const chosen = chosenStorage(storages, value);
  // `exact` is only "did the user's explicit choice resolve", for the effect below. Same guard: an
  // empty value is not an explicit choice, so it must not be looked up either.
  const exact = value === "" ? undefined : storages.find((s) => s.id === value);

  // TELL THE PARENT WHEN THE FALLBACK FIRES (quince#452 review). If `value` names a storage that is
  // no longer declared — a config edit plus a restart while this page is open — the select would
  // DISPLAY the default while the parent still held the stale id, and the button would submit the
  // stale one. The server refuses that clearly, so it fails safe; but the screen and the request
  // must not disagree in the meantime.
  //
  // Computed BEFORE the early returns because a hook cannot be called after one — the list is empty
  // in every non-loaded state, so the effect is inert there rather than conditional.
  React.useEffect(() => {
    if (!exact && chosen && value !== "") onChange(chosen.id);
  }, [exact, chosen, value, onChange]);

  if (state.status !== "loaded") return null;
  if (storages.length < 2) return null;

  return (
    // 16px ON PHONES, 12px FROM `sm` UP — AND THE LABEL STEPS WITH THE CONTROL (quince#616).
    //
    // iOS Safari zooms the page in when a focused control computes below 16px, and `text-xs` is
    // 12px: WebKit's target scale is `16 / fontSize`, so this select was a 1.33x zoom on tap —
    // worse than the 14px form fields, not better.
    //
    // The label steps too, and that is a VISUAL requirement rather than a technical one. WebKit
    // reads only the focused control's size, so `sm:text-xs` on the select alone would stop the
    // zoom — and would leave a 16px control sitting inside a 12px sentence reading "to <select>".
    // Ruled on quince#616: where an inline select steps up, its surrounding label steps with it.
    //
    // NOT the shared full-width `Select` (quince#623). That is the form-field shape; dropping it
    // into this sentence is the wrong control at the wrong size. Also ruled on quince#616.
    <label className="text-base text-muted sm:text-xs">
      to{" "}
      <select
        className="rounded-md border border-line bg-card px-1.5 py-1 text-base text-fg sm:text-xs"
        value={chosen?.id ?? ""}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        aria-label="Backup storage"
        data-testid="storage-select"
      >
        {storages.map((s) => (
          <option key={s.id} value={s.id} disabled={!s.reachable}>
            {s.name}
            {s.reachable ? ` (${s.backend})` : " — not connected"}
          </option>
        ))}
      </select>
    </label>
  );
}

// StorageNotices is everything StorageSelect used to say, rendered BELOW the action row where a
// full sentence is free to be a full line (quince#325's rule, applied to this rung's sentences).
//
// It renders for a single storage too, unlike the control: the full-transfer cost and an
// unreachable disk are facts worth stating whether or not there is a choice to make. The CONTROL
// is what a single storage does not need — the question does not exist — and that is a different
// question from whether the user should be told what this backup will cost.
export function StorageNotices({
  storages: sub,
  value,
}: {
  storages: Storages;
  value: string;
}) {
  const { state } = sub;
  const storages = state.status === "loaded" ? state.storages : [];
  // Same resolver as the control above, which is the point of it existing (quince#647): the notices
  // and the select must never disagree about which storage this page is aimed at.
  const chosen = chosenStorage(storages, value);

  if (state.status === "failed") {
    return (
      <p className="text-xs text-warn" data-testid="storages-failed">
        couldn&rsquo;t load storages — this backup will go to the default
      </p>
    );
  }
  if (state.status === "loading") return null;

  return (
    <>
      {/* THE FACT, NOT THE DIAGNOSIS — and only about the storage this backup is aimed at
          (quince#627).

          What stood here was the full diagnosis of EVERY unreachable storage in the configuration,
          each with its own `Re-check` button, on a page about one phone. It never referenced the
          chosen storage at all: the screenshot the issue came from showed `shuttle` selected and
          the sentence diagnosing `ghost` — a storage the user was not using, could not reach and
          had not asked about. With N unreachable storages it was N lines and N buttons.

          So this was a DELETION rather than a rescoping: there was no correct chosen-storage-only
          version to keep, because a storage's health belongs on the storage's page, where the
          diagnosis and `Re-check` now live (`StorageProblem`).

          What a device page owes instead is this: one line, naming the storage it is about, saying
          it is unavailable, linking to where the explanation and the remedy are.

          ONE SENTENCE, SHARED. quince#628 disables `Back up now` for exactly this state, and this
          line is what makes that disabled button honest rather than mute. Neither needs its own
          wording, and a second one here would be two things to keep in step.

          The condition matches that button's exactly — unreachable OR never created (`id === ""`).
          A storage quince has never reached cannot be a destination either, and the two states
          coincide in practice; what must NOT happen is a disabled button whose reason is missing
          because the two conditions drifted apart. */}
      {chosen && (!chosen.reachable || chosen.id === "") ? (
        <p className="text-xs text-warn" data-testid="storage-unavailable">
          <Link to={`/storage/${chosen.name}`} className="underline underline-offset-2">
            {chosen.name}
          </Link>{" "}
          is unavailable — backups can&rsquo;t be written to it right now.
        </p>
      ) : null}

      {/* THE COST, STATED BEFORE IT IS PAID (story 8). Proven on hardware during G9 — the staging
          stand's first backup to a second disk showed this line before the transfer started. */}
      {chosen?.reachable && chosen.will_be_full ? (
        <p className="text-xs text-warn" data-testid="storage-will-be-full">
          First backup to {chosen.name} — this transfers everything, not just what changed.
        </p>
      ) : null}
    </>
  );
}
