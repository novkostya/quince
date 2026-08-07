import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { addStorage, configKey, probeStorage } from "@/lib/config";
import { APIError } from "@/lib/api";
import type { ConfigFieldError, StorageProbe } from "@/lib/types";

// serverSentence pulls the daemon's own words out of a 422. Same rule ForgetStorage states: the
// refusal names the field AND the remedy, and re-wording it client-side drops the half that tells
// the user what to do.
function serverSentence(err: unknown, fallback: string): string {
  if (err instanceof APIError) {
    const details = err.details as { errors?: ConfigFieldError[] } | undefined;
    const errs = details?.errors;
    if (errs !== undefined && errs.length > 0) return errs[0].message;
    return err.message;
  }
  return fallback;
}

function bytes(n: number): string {
  if (n <= 0) return "";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let v = n;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i += 1;
  }
  return `${v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

// ProbeResult renders the daemon's answer. THREE BRANCHES, and which one you are in is `outcome`
// — never the HTTP status, which is 200 for all of them (contracts §1).
function ProbeResult({ probe }: { probe: StorageProbe }) {
  if (probe.outcome === "adopt") {
    return (
      <div className="mt-3 rounded-card border border-line bg-elevated p-3 text-sm">
        <div className="font-medium">This is already a quince storage</div>
        <div className="mt-1 text-muted">{probe.reason}</div>
        {/* NO BACKEND SELECTOR ON AN ADOPT. A storage's backend is written at its creation moment
            and is immutable; a later probe that disagrees is a remount, not a re-selection. Showing
            a dropdown here would offer a choice quince would then refuse to honour. */}
        <div className="mt-2 text-muted">
          Backend <span className="text-fg">{probe.backend}</span>
          {probe.marker ? <> · created {probe.marker.created_at.slice(0, 10)}</> : null}
        </div>
      </div>
    );
  }

  if (probe.outcome !== "new") {
    return (
      <div
        className="mt-3 rounded-card border border-line bg-elevated p-3 text-sm"
        data-testid="probe-refusal"
      >
        {/* THE DAEMON'S SENTENCE, VERBATIM. It names the path and, for a missing one, says the path
            must be visible INSIDE the container — which is the thing quince cannot fix for you and
            the only thing worth reading at that moment. */}
        {probe.reason}
      </div>
    );
  }

  return (
    <div className="mt-3 rounded-card border border-line bg-elevated p-3 text-sm">
      <div className="font-medium">
        {probe.clean_path}
        {probe.filesystem_free_bytes > 0 ? (
          <span className="ml-2 font-normal text-muted">
            {bytes(probe.filesystem_free_bytes)} free
          </span>
        ) : null}
      </div>
      <div className="mt-1 text-muted">{probe.backend_reason}</div>
      {probe.non_empty ? (
        <div className="mt-2 text-muted">
          This folder already has something in it. quince keeps its backups in per-device
          subfolders and will not touch anything else.
        </div>
      ) : null}
      {/* TIER 2 ONLY. `zfs: "none"` renders NOTHING — it means no signal, not "unsupported", and in
          hook mode a negative reading is a guaranteed false negative for the ordinary containerised
          setup. Silence is the honest answer. */}
      {probe.zfs === "host" ? (
        <div className="mt-2 text-muted">
          This host has ZFS. A storage on a ZFS dataset gets snapshot versioning with no copy at
          commit — see deploy/storage.md.
        </div>
      ) : null}
    </div>
  );
}

// AddStorage is qn.6e: one field, a probe, then facts.
//
// PROBE-FIRST, NOT FORM-FIRST. The user types a path and quince answers before anything is
// declared — and the answer arrives without changing the path, which is the rung's central
// guarantee (quince#415: "NOBODY CREATES A STORAGE ROOT").
//
// THE ZFS SUB-FORM IS NOT HERE. When the probe recommends zfs the form says so and refuses to save,
// naming the two keys a zfs storage needs. That is a SURFACED limitation rather than a silent one:
// a zfs storage needs `parent_dataset` and a helper command, and the control that makes those
// safe to type — `Test helper`, which fires the helper's two read-only verbs — is its own
// interaction with four outcomes and lands next. Offering a zfs save without it would let a user
// commit a configuration whose helper nobody has checked, which is the failure the whole branch
// exists to prevent.
export function AddStorage({ onAdded }: { onAdded: () => void }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState("");
  const [probe, setProbe] = useState<StorageProbe | null>(null);
  const [backend, setBackend] = useState("");
  const [checking, setChecking] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  function reset() {
    setPath("");
    setProbe(null);
    setBackend("");
    setError("");
  }

  async function check() {
    setChecking(true);
    setError("");
    setProbe(null);
    try {
      const res = await probeStorage(path.trim());
      setProbe(res.probe);
      setBackend(res.probe.backend);
    } catch (e) {
      setError(serverSentence(e, "could not check that path"));
    } finally {
      setChecking(false);
    }
  }

  async function save() {
    if (probe === null) return;
    setSaving(true);
    setError("");
    try {
      await addStorage({
        path: probe.clean_path,
        backend: backend as "zfs" | "reflink" | "hardlink" | "copy",
      });
      // Refetch rather than splice: the server owns the resulting document, and the storage list is
      // a separate resource that the applier has just changed. Same rule ForgetStorage follows.
      await qc.invalidateQueries({ queryKey: configKey });
      // The storage LIST is a separate resource with its own hook (not react-query), so its
      // owner is asked to refetch rather than a key being invalidated. The applier has already
      // made the storage live server-side; this is the client catching up.
      onAdded();
      setOpen(false);
      reset();
    } catch (e) {
      setError(serverSentence(e, "could not add that storage"));
    } finally {
      setSaving(false);
    }
  }

  const canAdopt = probe?.outcome === "adopt";
  const isNew = probe?.outcome === "new";
  // zfs needs keys this form does not yet collect — see the note on this component.
  const zfsUnsupportedHere = isNew && backend === "zfs";
  const canSave = (canAdopt || isNew) && !zfsUnsupportedHere && backend !== "";

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o);
        if (!o) reset();
      }}
    >
      <DialogTrigger asChild>
        {/* Ghost at a section foot, matching Rescan's move and WifiSyncControl's quiet arm. `-ml-3`
            cancels the size's px-3 inset so the text starts at the column margin — a ghost button
            has no background, so without it the label reads as a stray indent. */}
        <Button variant="ghost" size="sm" className="-ml-3" data-testid="add-storage">
          <Plus size={14} />
          Add storage
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>Add a storage</DialogTitle>
        <DialogDescription>
          Type the path as quince sees it inside its container. Nothing is created or changed until
          you save.
        </DialogDescription>

        <label className="mt-4 block text-sm font-medium" htmlFor="add-storage-path">
          Path
        </label>
        <div className="mt-1 flex gap-2">
          <input
            id="add-storage-path"
            className="h-9 flex-1 rounded-lg border border-line bg-card px-3 text-sm"
            value={path}
            placeholder="/backups"
            onChange={(e) => {
              setPath(e.target.value);
              setProbe(null);
            }}
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => void check()}
            disabled={checking || path.trim() === ""}
            data-testid="probe-check"
          >
            Check
          </Button>
        </div>

        {probe !== null ? <ProbeResult probe={probe} /> : null}

        {isNew ? (
          <div className="mt-3">
            <label className="block text-sm font-medium" htmlFor="add-storage-backend">
              Backend
            </label>
            <select
              id="add-storage-backend"
              className="mt-1 h-9 w-full rounded-lg border border-line bg-card px-2 text-sm"
              value={backend}
              onChange={(e) => setBackend(e.target.value)}
              data-testid="backend-select"
            >
              {/* The probe's answer is preselected and labelled as the recommendation; the others
                  stay choosable, because an operator may know something the probe cannot. */}
              {["zfs", "reflink", "hardlink", "copy"].map((b) => (
                <option key={b} value={b}>
                  {b}
                  {b === probe?.backend ? " — recommended" : ""}
                </option>
              ))}
            </select>
          </div>
        ) : null}

        {zfsUnsupportedHere ? (
          <div className="mt-3 text-sm text-muted" data-testid="zfs-not-here">
            A ZFS storage also needs a parent dataset and a helper command on the host, and quince
            cannot check those from this form yet. Declare this one in <code>config.yml</code> for
            now — see <code>deploy/storage.md</code>.
          </div>
        ) : null}

        {error !== "" ? (
          <div className="mt-3 text-sm" data-testid="add-storage-error">
            {error}
          </div>
        ) : null}

        <div className="mt-5 flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => void save()}
            disabled={!canSave || saving}
            data-testid="add-storage-save"
          >
            {canAdopt ? "Add this storage" : "Add storage"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
