import { useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { addStorage, checkStorageHook, configKey, probeStorage } from "@/lib/config";
import { DocLink } from "@/components/DocLink";
import { APIError } from "@/lib/api";
import type { ConfigFieldError, StorageHookCheck, StorageProbe } from "@/lib/types";

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
          commit — see <DocLink path="deploy/storage.md" />.
        </div>
      ) : null}
    </div>
  );
}

// AddStorageForm is the form ITSELF, with no container.
//
// EXTRACTED SO THE DIALOG AND THE FIRST-RUN PAGE CANNOT DIVERGE (qn.6e PR 9b). The onboarding step
// needs the same probe, the same three branches, the same backend selector and the same helper
// check — and two copies of that would drift, with the drift landing on the first-run path, which
// is the one nobody exercises twice.
//
// It owns all of the state and none of the chrome: the caller supplies the footer, because a dialog
// wants Cancel beside Save and a first-run page wants neither a cancel nor a modal to dismiss.
export function AddStorageForm({
  onSaved,
  footer,
}: {
  onSaved: () => void;
  // footer renders the action row. It receives everything a caller needs to draw its own buttons
  // without reaching into this component's state.
  footer: (a: { save: () => void; canSave: boolean; saving: boolean; adopting: boolean }) => ReactNode;
}) {
  const qc = useQueryClient();
  const [path, setPath] = useState("");
  const [probe, setProbe] = useState<StorageProbe | null>(null);
  const [backend, setBackend] = useState("");
  const [checking, setChecking] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const [parentDataset, setParentDataset] = useState("");
  const [hookCmd, setHookCmd] = useState("");
  const [hookCheck, setHookCheck] = useState<StorageHookCheck | null>(null);
  const [hookChecking, setHookChecking] = useState(false);

  function reset() {
    setPath("");
    setProbe(null);
    setBackend("");
    setError("");
    setParentDataset("");
    setHookCmd("");
    setHookCheck(null);
  }

  async function testHelper() {
    setHookChecking(true);
    setHookCheck(null);
    setError("");
    try {
      const res = await checkStorageHook(parentDataset.trim(), hookCmd.trim());
      setHookCheck(res.check);
    } catch (e) {
      setError(serverSentence(e, "could not run the helper check"));
    } finally {
      setHookChecking(false);
    }
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
        // `mode: hook` because `exec` cannot work in the shipped image (quince#697), and `seed:
        // auto` because in hook mode the host-side `seed` verb does the reflink and the key is
        // moot — the schema's own comment says so. Neither is asked for; both would be a field
        // whose only honest answer is the one quince already knows.
        ...(backend === "zfs"
          ? {
              zfs: {
                parent_dataset: parentDataset.trim(),
                mode: "hook" as const,
                hook_cmd: hookCmd.trim(),
                seed: "auto",
              },
            }
          : {}),
      });
      // Refetch rather than splice: the server owns the resulting document, and the storage list is
      // a separate resource that the applier has just changed. Same rule ForgetStorage follows.
      await qc.invalidateQueries({ queryKey: configKey });
      // The storage LIST is a separate resource with its own hook (not react-query), so its
      // owner is asked to refetch rather than a key being invalidated. The applier has already
      // made the storage live server-side; this is the client catching up.


      reset();
      onSaved();
    } catch (e) {
      setError(serverSentence(e, "could not add that storage"));
    } finally {
      setSaving(false);
    }
  }

  const canAdopt = probe?.outcome === "adopt";
  const isNew = probe?.outcome === "new";
  const needsZFS = isNew && backend === "zfs";

  // A ZFS STORAGE CANNOT BE SAVED UNTIL THE HELPER HAS ANSWERED, and `ok` is not the only answer
  // that clears it.
  //
  // `not_migrated` means the helper WORKS and lacks only the `capacity)` arm: backups, commits,
  // snapshots and retention are untouched, and the cost is a card reading "free space unavailable".
  // Blocking on it would refuse a working configuration over a cosmetic gap, which is a harsher
  // rule than the daemon's own.
  //
  // `parent_mismatch` and `unreachable` block, because both mean the storage would fail at commit
  // time — the exact failure this button exists to move forward from a multi-hour transfer to now.
  const helperUsable = hookCheck?.outcome === "ok" || hookCheck?.outcome === "not_migrated";
  const zfsReady =
    parentDataset.trim() !== "" && hookCmd.trim() !== "" && helperUsable;

  const canSave =
    (canAdopt || (isNew && backend !== "")) && (!needsZFS || zfsReady);
  return (
    <>

        <label className="mt-4 block text-sm font-medium" htmlFor="add-storage-path">
          Path
        </label>
        <div className="mt-1 flex gap-2">
          <Input
            id="add-storage-path"
            className="flex-1"
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
            <Select
              id="add-storage-backend"
              className="mt-1"
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
            </Select>
          </div>
        ) : null}

        {needsZFS ? (
          <div className="mt-3 rounded-card border border-line bg-elevated p-3" data-testid="zfs-fields">
            {/* THE MODE IS NOT A CHOICE, and saying so is more honest than a disabled dropdown.
                `exec` runs `zfs` inside the container, and the runtime image does not contain it
                (quince#697) — so offering the option would be offering something that cannot work.
                This is the ordinary unprivileged-container case, not a degraded one. */}
            <div className="text-sm">
              quince can&apos;t run <code>zfs</code> from inside its container — that&apos;s normal.
              It calls a helper on the host over SSH instead, which you install first: see{" "}
              <DocLink path="deploy/storage.md" />.
            </div>

            <label className="mt-3 block text-sm font-medium" htmlFor="zfs-parent">
              Parent dataset
            </label>
            <Input
              id="zfs-parent"
              className="mt-1"
              value={parentDataset}
              placeholder="pool/backups"
              onChange={(e) => {
                setParentDataset(e.target.value);
                setHookCheck(null);
              }}
            />

            <label className="mt-3 block text-sm font-medium" htmlFor="zfs-hook">
              Helper command
            </label>
            <Input
              id="zfs-hook"
              className="mt-1"
              value={hookCmd}
              placeholder="ssh -i /data/keys/zfs -o BatchMode=yes user@host"
              onChange={(e) => {
                setHookCmd(e.target.value);
                setHookCheck(null);
              }}
            />

            <div className="mt-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void testHelper()}
                disabled={
                  hookChecking || parentDataset.trim() === "" || hookCmd.trim() === ""
                }
                data-testid="test-helper"
              >
                Test helper
              </Button>
            </div>

            {hookCheck !== null ? (
              <div className="mt-3 text-sm" data-testid="hook-result" data-outcome={hookCheck.outcome}>
                {/* THE DAEMON'S SENTENCE, VERBATIM, for all four outcomes. Each has a different
                    remedy — install the helper, add the `capacity)` arm, fix the dataset, fix the
                    key — and a client that re-worded them would drop the half that says what to do. */}
                <div>{hookCheck.reason}</div>
                {hookCheck.detail !== "" ? (
                  // THE TRANSPORT'S OWN OUTPUT. ssh's "Permission denied (publickey)" is the whole
                  // answer to why a key does not work, and quince cannot improve on it. It may name
                  // this operator's host, so it is shown here and nowhere else — never logged,
                  // never in a fixture, never pasted into a PR.
                  <pre className="mt-2 overflow-x-auto rounded bg-card p-2 text-xs text-muted">
                    {hookCheck.detail}
                  </pre>
                ) : null}
              </div>
            ) : (
              <div className="mt-3 text-sm text-muted">
                Test the helper before saving. quince only sends two read-only commands, and it is
                the only way to find out that the key, the forced command and the dataset all agree
                — before a backup does.
              </div>
            )}
          </div>
        ) : null}

        {error !== "" ? (
          <div className="mt-3 text-sm" data-testid="add-storage-error">
            {error}
          </div>
        ) : null}

      {footer({ save: () => void save(), canSave, saving, adopting: canAdopt })}
    </>
  );
}
