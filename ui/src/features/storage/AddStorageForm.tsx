import { useEffect, useState, type KeyboardEvent, type ReactNode } from "react";
import { AlertCircle, Check } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  addStorage,
  checkStorageHook,
  configKey,
  ensureZFSKey,
  fetchZFSHelper,
  scanZFSHostKey,
  trustZFSHostKey,
  probeStorage,
} from "@/lib/config";
import { DocLink } from "@/components/DocLink";
import { CopyButton } from "@/components/CopyButton";
import { APIError, UnreachableError } from "@/lib/api";
import type {
  ConfigFieldError,
  StorageHookCheck,
  StorageProbe,
  StorageZFSHelperResponse,
  StorageZFSHostKey,
  StorageZFSKey,
} from "@/lib/types";

// serverSentence pulls the daemon's own words out of a 422. Same rule ForgetStorage states: the
// refusal names the field AND the remedy, and re-wording it client-side drops the half that tells
// the user what to do.
function serverSentence(err: unknown, fallback: string): string {
  // A REQUEST THAT NEVER REACHED THE DAEMON CARRIES ITS OWN SENTENCE, and it must survive rather
  // than fall through to the caller's fallback. Every fallback here names the operation — "could not
  // check that path", "could not ask that host for its key" — which is exactly the wrong thing to
  // tell someone whose server is down: it blames the input they typed correctly.
  if (err instanceof UnreachableError) return err.message;
  if (err instanceof APIError) {
    const details = err.details as { errors?: ConfigFieldError[] } | undefined;
    const errs = details?.errors;
    if (errs !== undefined && errs.length > 0) return errs[0].message;
    return err.message;
  }
  return fallback;
}

// onEnter runs a field's own action when Enter is pressed, and only while that action is available.
//
// A TEXT INPUT BESIDE A BUTTON IS A FORM TO ANYBODY WHO TYPES. On the first-run screen *Path* +
// *Check* is the only control on the page, so Enter is the natural next keystroke and it did
// nothing at all — no check, no error, no movement (Operator, from a phone, 2026-08-13).
//
// NOT `<form onSubmit>`, which is the obvious answer and the wrong one here: this component has
// three independent actions — Check, Test helper, and Save — and one submit handler fires whichever
// the browser picks, which depends on which button is first in the DOM rather than on which field
// has focus. Save is the least recoverable of the three and would be the one it picked.
//
// `enabled` MIRRORS THE BUTTON'S OWN `disabled`, passed in from the same expression rather than
// re-derived, so Enter can never reach an action a click could not. Re-deriving it is how the two
// drift.
function onEnter(run: () => void, enabled: boolean) {
  return (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter" || !enabled) return;
    // Stops an implicit submit if this form is ever nested in one, and stops the keypress
    // reaching a parent handler that might navigate.
    e.preventDefault();
    run();
  };
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
  // onSaved CARRIES THE NEW STORAGE'S NAME, because one caller navigates to it (quince#846) and the
  // other does not. `""` means the saved entry could not be found in the document that came back —
  // see `save()` — and a caller must treat it as "somewhere sensible", never as a name.
  onSaved: (name: string) => void;
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
  const [sshUser, setSSHUser] = useState("");
  const [sshHost, setSSHHost] = useState("");
  const [hookCheck, setHookCheck] = useState<StorageHookCheck | null>(null);
  const [hookChecking, setHookChecking] = useState(false);
  const [zfsKey, setZFSKey] = useState<StorageZFSKey | null>(null);
  const [keyError, setKeyError] = useState("");
  // THE HELPER IS FETCHED ON REQUEST, NOT ON MOUNT (quince#818 piece C). It is rendered for whatever
  // `parentDataset` currently says, and that field changes on every keystroke — so fetching as they
  // type would be a request per character, each answering about a half-typed dataset. A press is
  // also the honest moment: the operator is telling us they are ready to install it.
  const [helper, setHelper] = useState<StorageZFSHelperResponse | null>(null);
  const [helperError, setHelperError] = useState("");
  const [helperLoading, setHelperLoading] = useState(false);

  // THE HOST KEY, WHICH IS THE LAST THING BETWEEN A CORRECT SETUP AND A WORKING ONE (quince#912).
  // quince composes `StrictHostKeyChecking=yes` — the right choice, argued in `config/zfsssh.go` —
  // so `Test helper` answers `unreachable` with "Host key verification failed" until something puts
  // an entry in the container's `known_hosts`. Nothing did, and the file is inside the container, so
  // the only remedy was `docker exec`.
  //
  // TWO STEPS, AND THE SPLIT IS THE POINT: scan shows a FINGERPRINT and writes nothing; trust sends
  // that exact line back. The operator checks it against `ssh-keygen -lf` on the host in between,
  // which is the human half of trust-on-first-use and the reason `accept-new` was refused.
  const [hostKey, setHostKey] = useState<StorageZFSHostKey | null>(null);
  const [hostKeyError, setHostKeyError] = useState("");
  const [hostKeyBusy, setHostKeyBusy] = useState(false);
  const [hostKeyTrusted, setHostKeyTrusted] = useState("");

  async function scanHostKey() {
    setHostKeyBusy(true);
    setHostKeyError("");
    setHostKeyTrusted("");
    try {
      const res = await scanZFSHostKey(sshHost.trim());
      if (res.found && res.host_key) setHostKey(res.host_key);
      else setHostKeyError(res.reason);
    } catch (e) {
      setHostKeyError(serverSentence(e, "could not ask that host for its key"));
    } finally {
      setHostKeyBusy(false);
    }
  }

  async function trustHostKey() {
    if (hostKey === null) return;
    setHostKeyBusy(true);
    setHostKeyError("");
    try {
      // THE LINE FROM THE SCAN, unchanged. Never the host — see the comment above.
      const res = await trustZFSHostKey(hostKey.line);
      setHostKeyTrusted(res.path);
      setHostKey(null);
      // The helper check is the thing this unblocks, so its stale answer must not survive.
      setHookCheck(null);
    } catch (e) {
      setHostKeyError(serverSentence(e, "could not record that host key"));
    } finally {
      setHostKeyBusy(false);
    }
  }

  async function showHelper() {
    setHelperLoading(true);
    setHelperError("");
    try {
      setHelper(await fetchZFSHelper(parentDataset.trim()));
    } catch (e) {
      // THE DAEMON'S OWN SENTENCE, through the same extractor every other refusal on this form uses.
      // A 422 here means the dataset name is one quince will not put into a script it hands over,
      // and its message says why — re-wording it would drop the half that explains how a name that
      // looks fine was refused.
      setHelperError(serverSentence(e, "could not render the helper"));
      setHelper(null);
    } finally {
      setHelperLoading(false);
    }
  }

  function reset() {
    setPath("");
    setProbe(null);
    setBackend("");
    setError("");
    setParentDataset("");
    setSSHUser("");
    setSSHHost("");
    setHookCheck(null);
    // CLEARED SO THE NEXT ZFS STORAGE RE-ASKS, and `created` is why. After one add the key exists,
    // so a second one must read "quince found an ssh key it made earlier" — keeping the old answer
    // would tell the operator quince had just made them a key it had not, and invite them to paste
    // a line that is already installed.
    setZFSKey(null);
    setKeyError("");
    // Same reason one line up, pointed at the other artifact: a helper rendered for the storage that
    // was just saved carries that storage's `PARENT=`, and leaving it on screen for the next one is
    // a correct-looking script for the wrong dataset.
    setHelper(null);
    setHelperError("");
  }

  async function testHelper() {
    setHookChecking(true);
    setHookCheck(null);
    setError("");
    try {
      const res = await checkStorageHook(parentDataset.trim(), sshUser.trim(), sshHost.trim());
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
      const saved = await addStorage({
        path: probe.clean_path,
        backend: backend as "zfs" | "reflink" | "hardlink" | "copy",
        // `mode: hook` because it is the only mode — `exec` was removed for not working in the
        // shipped image (quince#697, quince#793) — and `seed:
        // auto` because in hook mode the host-side `seed` verb does the reflink and the key is
        // moot — the schema's own comment says so. Neither is asked for; both would be a field
        // whose only honest answer is the one quince already knows.
        ...(backend === "zfs"
          ? {
              zfs: {
                parent_dataset: parentDataset.trim(),
                mode: "hook" as const,
                ssh_user: sshUser.trim(),
                ssh_host: sshHost.trim(),
                seed: "auto",
              },
            }
          : {}),
      });
      // THE RESPONSE IS WRITTEN INTO THE CACHE, NOT INVALIDATED — and the difference is a redirect
      // loop rather than a preference.
      //
      // `invalidateQueries` marks the config STALE and refetches; it does not make the cache correct
      // BEFORE this returns. `RequireStorage` then mounts on `/`, react-query hands it the cached
      // value synchronously — still the pre-add `storage: null` — and it bounces the user straight
      // back to this page. Operator-reported: "added my first storage but still on
      // /onboarding/storage". The form had already reset, so it looked like nothing happened.
      //
      // The add endpoint RETURNS the resulting document, so there is nothing to re-fetch and no
      // race to lose: write it in and every reader is correct on the next render.
      qc.setQueryData(configKey, saved);
      // The storage LIST is a separate resource with its own hook (not react-query), so its
      // owner is asked to refetch rather than a key being invalidated. The applier has already
      // made the storage live server-side; this is the client catching up.

      // WHICH ENTRY IS THE NEW ONE — matched on the path just saved, which is the probe's
      // `clean_path` and is unique across entries by the schema's own rule. Not "the last one":
      // the response is the config as RE-LOADED, so its order is the file's rather than an append.
      //
      // `name` is populated even when the user never wrote one — it defaults to `path` at load
      // (quince#504), and this document went through a load. An empty answer therefore means the
      // entry was not found at all, which the caller lands somewhere honest for rather than
      // building a URL out of.
      const created = (saved.config.storage ?? []).find((s) => s.path === probe.clean_path);
      reset();
      onSaved(created?.name ?? "");
    } catch (e) {
      setError(serverSentence(e, "could not add that storage"));
    } finally {
      setSaving(false);
    }
  }

  const canAdopt = probe?.outcome === "adopt";
  const isNew = probe?.outcome === "new";
  const needsZFS = isNew && backend === "zfs";

  // THE BUTTONS' `disabled` AND THE ENTER KEY READ THE SAME EXPRESSION. Written once here so a
  // later edit to one cannot leave the keyboard reaching an action the mouse cannot — see onEnter.
  const canCheck = !checking && path.trim() !== "";
  const canTestHelper =
    !hookChecking &&
    parentDataset.trim() !== "" &&
    sshUser.trim() !== "" &&
    sshHost.trim() !== "";

  // THE KEY IS FETCHED WHEN THE ZFS BRANCH OPENS, not when the form mounts (quince#818 piece B).
  // The endpoint GENERATES on its first call, so asking earlier would leave a keypair on disk for
  // every copy-backend storage anybody ever added.
  //
  // ONCE — `zfsKey !== null` in the guard means switching backend away and back does not re-ask. A
  // second call would find the same key by construction, so the only thing a repeat buys is noise.
  useEffect(() => {
    if (!needsZFS || zfsKey !== null) return;
    let live = true;
    void (async () => {
      try {
        const res = await ensureZFSKey();
        if (live) setZFSKey(res.key);
      } catch (e) {
        // SURFACED, NEVER SWALLOWED. Both reachable failures — a `/data` quince cannot write, and
        // something at that path that is not a key — need the operator to act, and an empty panel
        // would read as "no key is needed here".
        if (live) setKeyError(serverSentence(e, "could not prepare the ssh key"));
      }
    })();
    return () => {
      live = false;
    };
  }, [needsZFS, zfsKey]);

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
    parentDataset.trim() !== "" && sshUser.trim() !== "" && sshHost.trim() !== "" && helperUsable;

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
            onKeyDown={onEnter(() => void check(), canCheck)}
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => void check()}
            disabled={!canCheck}
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
          // NOT A CARD. These are the same kind of thing as *Path* and *Backend* directly above —
          // fields you fill in — and boxing them indented everything one level deeper than the two
          // fields it continues, so the form read as a form containing a different form (Operator,
          // 2026-08-13). The zfs branch is more of the same question, not a subsection of it.
          //
          // The KEY and the HELPER panels below stay boxed, and the distinction is what they are:
          // those are artifacts to copy out and install elsewhere, not inputs. A border around a
          // thing you take away is doing work; a border around three more text fields is not.
          <div className="mt-3" data-testid="zfs-fields">
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
              placeholder="rpool/quince"
              onChange={(e) => {
                setParentDataset(e.target.value);
                setHookCheck(null);
                // THE RENDERED HELPER IS ABOUT *THIS* DATASET, so it is dropped with the answer it
                // was rendered for. A script left on screen after the field beneath it changed is
                // worse than none: it is a correct-looking file with somebody else's `PARENT=`, and
                // the operator has no way to tell by looking.
                setHelper(null);
                setHelperError("");
              }}
              onKeyDown={onEnter(() => void testHelper(), canTestHelper)}
            />

            {/* THE ARGV IS GONE (quince#818). This asked for a whole command line — the least
                self-explanatory field in the product, on the onboarding path. To fill it in
                correctly you had to already know that a key was needed, where to put it so the
                container could see it, that `BatchMode=yes` mattered, and that a forced command had
                to be installed first. None of that was on screen.

                QUINCE COMPOSES IT NOW, including every host-key option, so what is left is the two
                things only the operator knows: who the helper runs as, and where it runs. */}
            <label className="mt-3 block text-sm font-medium" htmlFor="zfs-ssh-host">
              ZFS host
            </label>
            <Input
              id="zfs-ssh-host"
              className="mt-1"
              value={sshHost}
              placeholder="nas.local"
              onChange={(e) => {
                setSSHHost(e.target.value);
                setHookCheck(null);
              }}
              onKeyDown={onEnter(() => void testHelper(), canTestHelper)}
            />

            <label className="mt-3 block text-sm font-medium" htmlFor="zfs-ssh-user">
              Remote user
            </label>
            <Input
              id="zfs-ssh-user"
              className="mt-1"
              value={sshUser}
              /* THE USER WHOSE `authorized_keys` CARRIES THE FORCED COMMAND — which is the thing
                 that bounds what quince can do on that host, so it is worth naming as itself rather
                 than as part of a `user@host` string. */
              placeholder="quince"
              onChange={(e) => {
                setSSHUser(e.target.value);
                setHookCheck(null);
              }}
              onKeyDown={onEnter(() => void testHelper(), canTestHelper)}
            />

            {/* THE KEY, AND THE LINE THAT CONSTRAINS IT (quince#818 piece B). Before this, a user had
                to know a key was needed at all, where to put it so the container could see it, and
                that a forced command had to be installed first — none of it on screen.

                THE `authorized_keys` LINE IS THE ARTIFACT, not the public key. `command="…"` is what
                pins the helper regardless of what the client asks for, so showing a bare key would
                invite pasting one — an unconstrained shell login on the operator's storage host.
                The public key is shown too, but second, and labelled as the thing this is built
                from. */}
            {keyError !== "" ? (
              <div className="mt-3 text-sm" data-testid="zfs-key-error">
                {keyError}
              </div>
            ) : null}

            {zfsKey !== null ? (
              <div className="mt-3 rounded-card border border-line bg-card p-3" data-testid="zfs-key">
                <div className="text-sm font-medium">
                  {/* WHICH ONE IT IS MATTERS. An existing key's public half may already be installed
                      on the host, so "quince found" means *you may be done*, where "quince made"
                      means *this still has to be pasted*. Guessing wrong invites replacing a working
                      entry. */}
                  {zfsKey.created
                    ? "quince made an ssh key for this"
                    : "quince found an ssh key it made earlier"}
                </div>
                <div className="mt-1 text-sm text-muted">
                  Add this one line to <code className="font-mono text-xs">~{"/"}.ssh/authorized_keys</code>{" "}
                  for <span className="text-fg">{sshUser.trim() === "" ? "the remote user" : sshUser.trim()}</span> on{" "}
                  <span className="text-fg">{sshHost.trim() === "" ? "the ZFS host" : sshHost.trim()}</span> — it
                  restricts the key to the helper, so it cannot be used for anything else.
                </div>
                <pre
                  className="mt-2 overflow-x-auto rounded bg-elevated p-2 text-xs whitespace-pre-wrap break-all"
                  data-testid="zfs-authorized-keys"
                >
                  {zfsKey.authorized_keys}
                </pre>
                {/* COPYING THIS BY HAND IS THE STEP MOST LIKELY TO GO WRONG. It is one line, it
                    wraps across three on a phone, and a selection that clips the leading
                    `command="…"` leaves a WORKING key with no constraint — an unrestricted shell
                    login on the storage host rather than a helper pinned to one dataset. The button
                    copies the whole line or says it could not. */}
                <div className="mt-2">
                  <CopyButton value={zfsKey.authorized_keys} label="Copy the line" />
                </div>
                <div className="mt-2 text-xs text-muted">
                  The private half stays in{" "}
                  <code className="font-mono">{zfsKey.path}</code> and never leaves this machine.
                </div>
              </div>
            ) : null}

            {/* THE SECOND HALF OF THE INSTALL, and until quince#818 piece C it was the half the
                screen said nothing about. The `authorized_keys` line above pins a forced command;
                this is the script that command runs. A key installed without it reaches a host that
                refuses everything, which presents as `unreachable` — indistinguishable from a wrong
                key unless you already know the helper is missing.

                IT IS RENDERED WITH THEIR OWN `PARENT=`, which is the whole point of the piece: the
                one line an operator had to edit by hand is the one line that decides where every
                backup goes, and a wrong value produces a script that works and writes to the wrong
                dataset. */}
            {parentDataset.trim() !== "" ? (
              <div className="mt-3">
                {helper === null ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void showHelper()}
                    disabled={helperLoading}
                    data-testid="show-helper"
                  >
                    {helperLoading ? "Rendering…" : "Show the helper script"}
                  </Button>
                ) : (
                  <div className="rounded-card border border-line bg-card p-3" data-testid="zfs-helper">
                    <div className="text-sm font-medium">The helper script, ready to install</div>
                    <div className="mt-1 text-sm text-muted">
                      Save this on{" "}
                      <span className="text-fg">{sshHost.trim() === "" ? "the ZFS host" : sshHost.trim()}</span> as{" "}
                      <code className="font-mono text-xs">{helper.path}</code> and make it
                      executable. Its <code className="font-mono text-xs">PARENT=</code> is already
                      set to <span className="text-fg">{parentDataset.trim()}</span> — nothing in it
                      needs editing.
                    </div>
                    {/* CAPPED AND SCROLLABLE. It is ~70 lines: rendered full-height it would bury
                        the rest of the form on a phone, and this is a thing to COPY rather than to
                        read. */}
                    <pre
                      className="mt-2 max-h-64 overflow-auto rounded bg-elevated p-2 text-xs"
                      data-testid="zfs-helper-script"
                    >
                      {helper.script}
                    </pre>
                    <div className="mt-2">
                      <CopyButton value={helper.script} label="Copy the script" />
                    </div>
                  </div>
                )}
                {helperError !== "" ? (
                  <div className="mt-2 text-sm" data-testid="zfs-helper-error">
                    {helperError}
                  </div>
                ) : null}
              </div>
            ) : null}


            {/* THE HOST KEY, ABOVE `Test helper` BECAUSE IT GATES IT (quince#912). Until an entry
                exists, the check answers `unreachable` with ssh's "Host key verification failed",
                and the operator's only remedy was to edit a file inside the container.

                THE FINGERPRINT IS SHOWN AND NOT ACTED ON. quince will not trust a key it merely
                fetched — that is `accept-new`, which trusts whatever answers on the first connect,
                exactly when an attacker is positioned. The operator compares it against
                `ssh-keygen -lf` on the host, which is one command they can actually run, and then
                presses the second button. Two steps with a human in the middle. */}
            {sshHost.trim() !== "" ? (
              <div className="mt-3" data-testid="zfs-hostkey">
                {hostKey === null ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void scanHostKey()}
                    disabled={hostKeyBusy}
                    data-testid="hostkey-scan"
                  >
                    {hostKeyBusy ? "Asking…" : "Check this host's key"}
                  </Button>
                ) : (
                  <div className="rounded-card border border-line bg-card p-3">
                    <div className="text-sm font-medium">Is this the right host?</div>
                    <div className="mt-1 text-sm text-muted">
                      <span className="text-fg">{hostKey.host}</span> offered a{" "}
                      <span className="text-fg">{hostKey.key_type}</span> key. Check it against the
                      host itself before trusting it — quince cannot tell a rebuilt machine from
                      something impersonating one.
                    </div>
                    {/* IT WRAPS RATHER THAN SCROLLING. This is a single unbroken token the operator
                        compares character by character, and half of it behind a scrollbar is worse
                        than useless. */}
                    <pre
                      className="mt-2 rounded bg-elevated p-2 text-xs whitespace-pre-wrap break-all"
                      data-testid="hostkey-fingerprint"
                    >
                      {hostKey.fingerprint}
                    </pre>
                    {/* IT ASKS THE RUNNING sshd, AND THEREFORE CARRIES NO PATH AT ALL (Operator +
                        architect, 2026-08-13).

                        Three attempts, each wrong in a smaller way. First it derived the filename
                        from the key type — `ssh-ed25519` → `ssh_host_ed25519_key.pub` — right for
                        two of the three common types and WRONG for ecdsa, because the type carries
                        the curve (`ecdsa-sha2-nistp256`) and the filename does not. It printed a
                        path that cannot exist, in the one instruction whose whole job is to be
                        runnable. Then a `/etc/ssh` glob, which needs no mapping but still assumes
                        the directory — `sshd_config`'s `HostKey` can point anywhere.

                        `ssh-keyscan` removes the assumption instead of narrowing it, and does
                        something no file-reading form can: it reports what sshd is actually
                        PRESENTING. A key on disk that sshd does not serve is a fingerprint that can
                        never match however carefully the operator compares — so the file-reading
                        forms fail in a way that LOOKS like a mismatch and is not.

                        It is also the same question quince just asked, from the other side: quince
                        scanned this host, this scans loopback on it. Same mechanism, same answer, so
                        a mismatch means something real rather than a lookup gone wrong.

                        `-lf -` READS A STREAM, and the `-` is why there is no loop: `ssh-keygen -lf`
                        takes exactly one file (`Too many arguments`), so several keys have to arrive
                        on stdin. */}
                    <div className="mt-2 text-xs text-muted">
                      Run this on <span className="text-fg">{hostKey.host}</span> and look for the
                      same fingerprint:
                      <pre
                        className="mt-1 rounded bg-elevated p-2 whitespace-pre-wrap break-all"
                        data-testid="hostkey-compare-command"
                      >
                        ssh-keyscan localhost | ssh-keygen -lf -
                      </pre>
                    </div>
                    <div className="mt-2 flex gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void trustHostKey()}
                        disabled={hostKeyBusy}
                        data-testid="hostkey-trust"
                      >
                        It matches — trust it
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setHostKey(null);
                          setHostKeyError("");
                        }}
                        disabled={hostKeyBusy}
                        data-testid="hostkey-cancel"
                      >
                        Cancel
                      </Button>
                    </div>
                  </div>
                )}
                {hostKeyTrusted !== "" ? (
                  <div className="mt-2 text-sm text-muted" data-testid="hostkey-trusted">
                    Recorded in <code className="font-mono text-xs">{hostKeyTrusted}</code>.
                  </div>
                ) : null}
                {hostKeyError !== "" ? (
                  /* THE DAEMON'S SENTENCE. The one that matters most is the changed-key refusal,
                     which names both explanations and the file — quince will not choose between
                     "rebuilt" and "impersonated" on the operator's behalf. */
                  <div className="mt-2 text-sm" data-testid="hostkey-error">
                    {hostKeyError}
                  </div>
                ) : null}
              </div>
            ) : null}
            <div className="mt-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void testHelper()}
                disabled={!canTestHelper}
                data-testid="test-helper"
              >
                Test helper
              </Button>
            </div>

            {hookCheck !== null ? (
              <div className="mt-3 text-sm" data-testid="hook-result" data-outcome={hookCheck.outcome}>
                {/* THE DAEMON'S SENTENCE, VERBATIM, for all four outcomes. Each has a different
                    remedy — install the helper, add the `capacity)` arm, fix the dataset, fix the
                    key — and a client that re-worded them would drop the half that says what to do.

                    THE MARK IS THE OUTCOME, NOT DECORATION. Four states with four remedies read as
                    one wall of grey text, and `ok` is the one the operator is scanning for after
                    each attempt (Operator, 2026-08-13). `not_migrated` deliberately gets the warn
                    tone rather than the ok one: the helper works and the cost is a card reading
                    "free space unavailable", which is neither success nor failure. */}
                <div className="flex items-start gap-2">
                  {hookCheck.outcome === "ok" ? (
                    <Check size={16} className="mt-0.5 shrink-0 text-ok" aria-hidden />
                  ) : (
                    <AlertCircle
                      size={16}
                      className={
                        (hookCheck.outcome === "not_migrated" ? "text-warn" : "text-danger") +
                        " mt-0.5 shrink-0"
                      }
                      aria-hidden
                    />
                  )}
                  <div>{hookCheck.reason}</div>
                </div>
                {/* THE RAW OUTPUT IS FOR DIAGNOSING A FAILURE, so it is not shown on success. On
                    `ok` it is `capacity`'s two numbers in BYTES — `98304  128848920576` — which
                    reads as a fault rather than as evidence, and the storage card already shows the
                    same figures in units a person uses (Operator, 2026-08-13). Every other outcome
                    keeps it: ssh's own words are the whole answer to why a key does not work. */}
                {hookCheck.detail !== "" && hookCheck.outcome !== "ok" ? (
                  // THE TRANSPORT'S OWN OUTPUT. ssh's "Permission denied (publickey)" is the whole
                  // answer to why a key does not work, and quince cannot improve on it. It may name
                  // this operator's host, so it is shown here and nowhere else — never logged,
                  // never in a fixture, never pasted into a PR.
                  // IT WRAPS RATHER THAN SCROLLING. ssh's messages are long single lines —
                  // "No ED25519 host key is known for <host> and you have requested strict
                  // checking" runs past 90 characters — and in a horizontally scrolling box the
                  // operator sees the first half of the one sentence that says what is wrong
                  // (Operator, from a phone, 2026-08-13). This is prose to READ, unlike the helper
                  // script above it, which is a thing to COPY and where wrapping would corrupt what
                  // you paste.
                  <pre className="mt-2 rounded bg-card p-2 text-xs whitespace-pre-wrap break-words text-muted">
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
