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
