import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type {
  Config,
  ConfigResponse,
  StorageAddition,
  StorageHookCheckResponse,
  StorageProbeResponse,
  StorageZFSHelperResponse,
  StorageZFSHostKeyResponse,
  StorageZFSHostKeyTrustResponse,
  StorageZFSKeyResponse,
} from "./types";

export const configKey = ["config"] as const;

export function useConfig() {
  return useQuery({
    queryKey: configKey,
    queryFn: () => api.get<ConfigResponse>("/api/config"),
  });
}

// updateConfig PUTs the full document (contracts §1: full-document replace). A 422 surfaces
// as an APIError whose details carry {errors:[{path,message}]} for inline field errors.
export function updateConfig(config: Config): Promise<ConfigResponse> {
  return api.put<ConfigResponse>("/api/config", config);
}

// forgetStorage removes ONE storage from the declaration (contracts §1, qn.6d gap B).
//
// A NARROW ENDPOINT RATHER THAN updateConfig ABOVE, and the difference is not stylistic: a
// full-document PUT rebuilt from a list this client rendered drops every surviving entry's
// `retention:`. That key is a POINTER server-side, so an omitted one is indistinguishable from
// "keep none" and the code defaults silently take over. No storage surface renders retention, so
// this client could not round-trip it even in principle. The server splices instead.
//
// The name is percent-encoded because it comes from config and may contain a slash: `name`
// defaults to the PATH (quince#504), so on a single-storage install it usually IS one.
export function forgetStorage(name: string): Promise<ConfigResponse> {
  return api.del<ConfigResponse>(`/api/config/storage/${encodeURIComponent(name)}`);
}

// makeStorageDefault re-designates the default storage (contracts §1, quince#722).
//
// A NARROW ENDPOINT for the reason forgetStorage gives directly above, unchanged: the server
// splices over the live parsed config, so every other entry keeps the keys this client never
// rendered and could not round-trip.
//
// IT SENDS NO BODY. The name in the path is the whole request — there is nothing to say about a
// storage beyond WHICH one — and a `{default:true}` body would invite the question of what
// `{default:false}` means. There is no such state: exactly one storage is default at all times, so
// un-defaulting is not an operation and the API deliberately does not offer one.
//
// Percent-encoded for forgetStorage's reason: `name` defaults to the PATH (quince#504), so on a
// single-storage install it usually IS one.
export function makeStorageDefault(name: string): Promise<ConfigResponse> {
  return api.post<ConfigResponse>(`/api/config/storage/${encodeURIComponent(name)}/default`, {});
}

// probeStorage asks what a typed path IS, WITHOUT CHANGING IT (contracts §1, qn.6e).
//
// EVERY REFUSAL COMES BACK AS A 200 carrying `outcome`, not as an error status: "that path does not
// exist" is the ANSWER to the question, not a failure to answer it, and the form renders it beside
// the same field as a success. Only a malformed question — no path, or a relative one — is a 422,
// which arrives as an APIError like every other config refusal.
export function probeStorage(path: string): Promise<StorageProbeResponse> {
  return api.post<StorageProbeResponse>("/api/storages/probe", { path });
}

// addStorage splices ONE storage into the declaration (contracts §1, qn.6e).
//
// A NARROW ENDPOINT rather than updateConfig, for the identical reason forgetStorage gives: a
// full-document PUT rebuilt from what this client rendered drops every entry's `zfs:` and
// `retention:`, which no storage surface renders. The server splices instead.
export function addStorage(entry: StorageAddition): Promise<ConfigResponse> {
  return api.post<ConfigResponse>("/api/config/storage", entry);
}

// checkStorageHook fires the constrained helper's two READ-ONLY verbs — `capacity`, then
// `list <parent dataset>` — and classifies the result (contracts §1, qn.6e).
//
// Nothing it sends can create, destroy or write: both arms are path-guarded inside the operator's
// own forced command, which is the property that makes this safe to fire from a form.
//
// Every verdict is a 200, `unreachable` included: a user who has not installed the helper yet has
// asked a perfectly good question. Only a malformed question — no dataset, or no command — is a 422.
//
// IT SENDS THE TRANSPORT STRUCTURED and the server composes the argv (quince#818). `ssh_port` and
// `ssh_key` are deliberately NOT sent: they default server-side exactly as the config does, so
// omitting them tests the same transport the saved storage will use — which is the only thing that
// makes this button mean anything.
export function checkStorageHook(
  parentDataset: string,
  sshUser: string,
  sshHost: string,
): Promise<StorageHookCheckResponse> {
  return api.post<StorageHookCheckResponse>("/api/storages/probe/hook", {
    parent_dataset: parentDataset,
    ssh_user: sshUser,
    ssh_host: sshHost,
  });
}

// ensureZFSKey asks quince for the key it uses to reach the ZFS helper — generating one at
// `/data/keys/zfs` only if there is nothing there (contracts §1, quince#818 piece B).
//
// NO PATH IS SENT, and the endpoint takes none. That is what keeps it from being an authenticated
// write-a-file-anywhere primitive whose contents happen to be a private key; quince can only ever
// touch its own path. An operator who keeps a key elsewhere sets `ssh_key` by hand instead.
//
// THE DATASET IS SENT, AND IT IS NOT A PATH (quince#985). It goes inside the `command="…"` forced
// command on the returned `authorized_keys` line, which is now the only place a key's confinement is
// written down — the helper script itself is identical on every install. A name quince cannot vouch
// for comes back 422 naming `parent_dataset`, refused rather than escaped.
//
// `created` DISTINGUISHES *made you one* FROM *found yours*, which the screen must say: an existing
// key's public half may already be installed on a host, and offering to replace it would break a
// working storage silently.
export function ensureZFSKey(parentDataset: string): Promise<StorageZFSKeyResponse> {
  return api.post<StorageZFSKeyResponse>("/api/storages/zfs/key", {
    parent_dataset: parentDataset,
  });
}

// fetchZFSHelper asks quince for the constrained helper script (contracts §1, quince#818 piece C).
//
// IT TAKES NO ARGUMENT SINCE quince#985. The script used to arrive with the operator's dataset
// substituted into a `PARENT=` line, so every install's file differed while there was one documented
// place to put it — and a second zfs storage on one host overwrote the first's helper, breaking it
// silently. The dataset moved into the `authorized_keys` forced command, which is per key, so this
// answer is now the same bytes for everyone.
export function fetchZFSHelper(): Promise<StorageZFSHelperResponse> {
  return api.get<StorageZFSHelperResponse>("/api/storages/zfs/helper");
}

// scanZFSHostKey asks what key a host offers (contracts §1, quince#912). It authenticates nothing
// and writes nothing — it is the half that lets the operator SEE a fingerprint before deciding.
export function scanZFSHostKey(
  sshHost: string,
  sshPort?: number,
): Promise<StorageZFSHostKeyResponse> {
  return api.post<StorageZFSHostKeyResponse>("/api/storages/zfs/hostkey", {
    ssh_host: sshHost,
    ...(sshPort ? { ssh_port: sshPort } : {}),
  });
}

// trustZFSHostKey records the line the operator confirmed.
//
// IT SENDS THE LINE BACK, NEVER THE HOST. The server does not re-scan, and that is deliberate: a
// host answering differently between the two calls would otherwise be trusted after the operator
// approved a different fingerprint, making the confirmation theatre.
export function trustZFSHostKey(line: string): Promise<StorageZFSHostKeyTrustResponse> {
  return api.post<StorageZFSHostKeyTrustResponse>("/api/storages/zfs/hostkey/trust", { line });
}

// useConfigDraft — an editable copy of the server's configuration document, kept honest about the
// server moving underneath it.
//
// EXTRACTED FROM `ConfigEditor` WITH NO BEHAVIOUR CHANGE (quince#1212). It is here because there is
// now a SECOND config surface — `/settings/notifications` edits the same document — and
// `PUT /api/config` is a FULL-DOCUMENT REPLACE. Two forms each holding their own draft of one object
// is precisely quince#764's defect with a new road into it: save on one page and whatever the other
// page changed is reverted, with nothing said.
//
// Copying the mechanism instead would have been the wrong shape twice over. It is forty lines of
// deliberately subtle state whose comments say, in as many words, that its failure mode is SILENT
// (see the key-order note below) — and a copy that drifts from the original produces two pages
// disagreeing about when it is safe to save, which is the bug rather than a variation on it.
export function useConfigDraft(config: Config) {
  const [draft, setDraft] = useState<Config>(config);

  // THE DRAFT FOLLOWS THE SERVER (quince#764). `useState(config)` captures its argument on FIRST
  // MOUNT and ignores every later value, so without this a form can hold a document the server
  // stopped serving minutes ago — and saving one field then ships the whole stale draft, reverting
  // everything that changed underneath it.
  //
  // Observed on the staging stand: two storages hand-edited to `backend: hardlink`, quince restarted,
  // one unrelated save, and both were back to `auto` on disk — while the preview panel showed the
  // correct file. Two documents on one screen, disagreeing.
  //
  // ADJUSTED DURING RENDER, NOT IN AN EFFECT. This is React's own pattern for state derived from
  // props; an effect renders once with the stale value first, and on a form that frame is a save the
  // user can click.
  //
  // ONLY WHEN THE FORM IS CLEAN, and that half is load-bearing. React Query refetches on window
  // focus, so an unconditional re-sync would wipe whatever was being typed the moment you switched
  // tabs — trading this bug for a second silent loss in the opposite direction. A dirty form keeps
  // its draft and is TOLD, which is `staleElsewhere`.
  //
  // `config !== synced` is a REFERENCE test, deliberately: React Query's structural sharing preserves
  // identity across a refetch whose content is unchanged, so this fires when the document actually
  // moved rather than on every poll.
  //
  // THE STRINGIFY COMPARISON IS KEY-ORDER SENSITIVE, and a false `dirty` silently restores
  // quince#764 (architect note on quince#765). It is safe because BOTH SIDES COME FROM THE SAME
  // SERVER DOCUMENT and are only ever updated by SPREAD, which preserves key insertion order — so a
  // clean draft stringifies identically to `synced`. A caller that rebuilds a nested section from an
  // object literal in a different key order breaks that: the form reads dirty when nobody touched it,
  // the re-sync stops, and the bug is back for that section **with no test failing**, because the
  // tests reach this through spreads. Written down because the assumption is invisible and its
  // failure is silent — and it now binds EVERY caller of this hook, not just the one that grew it.
  const [synced, setSynced] = useState<Config>(config);
  const [staleElsewhere, setStaleElsewhere] = useState(false);
  if (config !== synced) {
    const dirty = JSON.stringify(draft) !== JSON.stringify(synced);
    setSynced(config);
    if (dirty) {
      // THE FORM IS NOT OVERWRITTEN AND THE USER IS TOLD. Keeping the draft protects the edit in
      // progress; saying so is what stops the save silently shipping a stale section.
      setStaleElsewhere(true);
    } else {
      setDraft(config);
      setStaleElsewhere(false);
    }
  }

  // Taking the server's document DISCARDS the edit in progress, so it is an explicit action rather
  // than something that happens to you — which is the whole distinction this mechanism draws.
  const takeServerVersion = () => {
    setDraft(config);
    setSynced(config);
    setStaleElsewhere(false);
  };

  // adopt ENDS THE DIVERGENCE after a successful save, whichever way it went. The PUT response IS
  // the new server document — `PUT` returns the same body `GET` does — so adopting it leaves the
  // form, `synced` and the server agreeing, and clears a notice whose cause the save has resolved.
  // Without it, a form that was stale-then-saved would keep warning about a document it has replaced.
  const adopt = (c: Config) => {
    setDraft(c);
    setSynced(c);
    setStaleElsewhere(false);
  };

  return { draft, setDraft, staleElsewhere, takeServerVersion, adopt };
}
