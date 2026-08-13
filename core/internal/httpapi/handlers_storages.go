package httpapi

import (
	"net/http"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/wire"
)

// handleStorages serves GET /api/storages → {storages: Storage[]} (contracts §1, qn.6c story 5).
//
// DEVICE-INDEPENDENT by ruling: `?udid=` adds `will_be_full` per storage, and without it the list
// is a plain resource. "Will the next backup be full" is a property of a (device, storage) pair,
// not of a storage, so putting it on the object unconditionally would distort the resource for the
// storage-cards rung that follows.
func (d Deps) handleStorages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list := d.Storages.Storages(r.URL.Query().Get("udid"))
		if list == nil {
			list = []wire.Storage{}
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StoragesResponse{Storages: list})
	}
}

// handleStorageRecheck serves POST /api/storages/{name}/recheck → 200 {storage} | 404.
//
// The button behind *plug the disk in and press it* (Operator ruling 2026-08-01): reachability may
// change without a restart, where the storage LIST still needs one.
//
// 200 rather than 202 because the check is a stat, not a job — a 202 would imply something to poll,
// and this rung has no queue by ruling. Per-storage rather than global because the user plugged in
// ONE disk, and a global re-check would make one press pay for every unreachable root.
func (d Deps) handleStorageRecheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, ok := d.Storages.Recheck(r.PathValue("name"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "no_such_storage",
				"no storage with that name is declared")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StorageResponse{Storage: s})
	}
}

// handleStorageProbe serves POST /api/storages/probe → 200 {probe} | 422 (contracts §1, qn.6e).
//
// It answers ONE question — what IS this path? — and it answers it WITHOUT CHANGING THE PATH. The
// probe never creates a directory and never mints a storage marker; creating and declaring are
// separate, explicit acts that a user takes after seeing this answer. `storage.Inspect` carries
// that guarantee and its own gate (quince#415, and qn.6e G1).
//
// NOT KEYED ON A STORAGE NAME, unlike every other route in this file, because the subject does not
// exist yet. `/probe` is a literal segment beside `/{name}/recheck` and cannot collide with it: they
// differ in segment count, so a storage may even be named "probe" without shadowing this.
//
// WHERE THE 422 LINE IS, and it is not where "an error is a non-200" would put it.
//
// A 422 means THE QUESTION WAS MALFORMED — no body, no `path`, or a path that is not absolute. Those
// are statements about the request: the client did not ask about a path.
//
// Everything Inspect can say about a real absolute path is a 200, INCLUDING every refusal. "That
// path does not exist", "that is a file", "quince cannot write there" are the ANSWER to the question
// asked, not a failure to answer it — and a form needs them rendered the same way as a success,
// beside the same field, with the daemon's own sentence. Mapping them to statuses would make the
// client branch twice on one thing and would lose `marker` and the free-space figures, which are
// reported on refusals too.
//
// The non-absolute case is deliberately on the 422 side, matching config's `validate.go`: a relative
// path is not a path quince could ever store, so the form's refusal and the config's refusal say the
// same thing rather than disagreeing about the same string.
func (d Deps) handleStorageProbe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wire.StorageProbeRequest
		if err := decodeJSON(r, &req); err != nil {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: []wire.ConfigError{{Path: "path", Message: "expected a JSON object with a `path`"}}})
			return
		}
		// `storage[i].path`'s two rules, asked before the syscall rather than after, so the answer
		// is the same sentence the config would give for the same string.
		if req.Path == "" {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: []wire.ConfigError{{Path: "path", Message: "must not be empty"}}})
			return
		}
		if !filepath.IsAbs(req.Path) {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: []wire.ConfigError{{Path: "path",
				Message: "must be an absolute path, and it must be the path INSIDE the container"}}})
			return
		}

		writeJSON(w, d.Log, http.StatusOK, wire.StorageProbeResponse{
			Probe: probeToWire(storage.Inspect(req.Path, storage.InspectOptions{})),
		})
	}
}

// probeToWire is the whole of the mapping, kept explicit rather than reusing the storage type on the
// wire: `storage.Report` may grow fields for quince's own use, and a struct that is both an internal
// value and a frozen contract publishes every one of them by default.
func probeToWire(r storage.Report) wire.StorageProbe {
	p := wire.StorageProbe{
		Path:                 r.Path,
		CleanPath:            r.CleanPath,
		Outcome:              string(r.Outcome),
		Reason:               r.Reason,
		Backend:              r.Backend,
		BackendReason:        r.BackendReason,
		NonEmpty:             r.NonEmpty,
		ZFS:                  string(r.ZFS),
		FilesystemFreeBytes:  r.FreeBytes,
		FilesystemTotalBytes: r.TotalBytes,
	}
	if r.Marker != nil {
		// A SUBSET, deliberately: the checksum is the file's own integrity detail and app_version is
		// quince's history. Neither is a form's business, and publishing them would freeze them.
		p.Marker = &wire.StorageProbeMarker{
			StorageID: r.Marker.StorageID,
			Backend:   r.Marker.Backend,
			CreatedAt: r.Marker.CreatedAt,
		}
	}
	return p
}

// handleStorageHookCheck serves POST /api/storages/probe/hook → 200 {check} | 422 (contracts §1,
// qn.6e).
//
// `Test helper` — the load-bearing control of the zfs branch. Without it, "did I install the helper
// correctly?" is answered by a failed multi-hour Wi-Fi transfer at commit time: the key, the forced
// command in authorized_keys, and the $PARENT baked into the helper are three things that must line
// up, and none of them is observable from the path.
//
// IT RUNS TWO READ-ONLY, PATH-GUARDED VERBS and can create, destroy or write nothing —
// `capacity` (no caller argument at all) then `list <typed parent>` (guarded by
// `case "$target" in "$PARENT"|"$PARENT"/*`). That is what makes it safe to fire from a form.
//
// THE 422 LINE IS THE PROBE'S, unchanged: a malformed QUESTION — no parent dataset, or no hook
// command — is a 422; every verdict about a real pair, refusals included, is a 200 carrying the
// outcome. A user who has not installed the helper has asked a perfectly good question.
//
// DECLARED, because it is the sharpest thing in this rung: THIS ENDPOINT EXECUTES A
// REQUEST-SUPPLIED ARGV. It adds no capability an authenticated admin lacks — `PUT /api/config`
// already stores a `hook_cmd` that quince execs at the next job — but it shortens the loop from
// *next backup* to *now*. It is behind authGuard and csrfGuard, adds nothing to the five-entry
// exempt set, runs an argv ARRAY and never a shell string, is bounded by a timeout, and its
// subprocess is killed by process group on cancellation.
func (d Deps) handleStorageHookCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wire.StorageHookCheckRequest
		if err := decodeJSON(r, &req); err != nil {
			hookCheck422(w, d, "parent_dataset", "expected a JSON object with `parent_dataset`, `ssh_user` and `ssh_host`")
			return
		}
		if req.ParentDataset == "" {
			hookCheck422(w, d, "parent_dataset", "must not be empty — quince cannot ask the helper about nothing")
			return
		}
		// THE TWO FIELDS WITH NO DEFAULT (quince#818). `ssh_port` and `ssh_key` are omitted by a form
		// that has not asked for them and default the way the config does, so refusing an absent one
		// here would refuse the ordinary request.
		if req.SSHHost == "" {
			hookCheck422(w, d, "ssh_host", "must not be empty — this is the host running the quince-zfs-helper")
			return
		}
		if req.SSHUser == "" {
			hookCheck422(w, d, "ssh_user", "must not be empty — this is the remote user whose `authorized_keys` carries the forced command")
			return
		}

		// COMPOSED BY THE SAME FUNCTION THE SAVED STORAGE WILL USE, deliberately. If this built its
		// own argv the button could pass against a transport the running backup never takes, which
		// is the precise failure `Test helper` exists to prevent.
		zc := config.ZFSConfig{
			SSHUser: req.SSHUser, SSHHost: req.SSHHost,
			SSHPort: req.SSHPort, SSHKey: req.SSHKey,
		}
		c := storage.CheckHook(r.Context(), req.ParentDataset, zc.SSHArgv())
		writeJSON(w, d.Log, http.StatusOK, wire.StorageHookCheckResponse{
			Check: wire.StorageHookCheck{
				Outcome: string(c.Outcome),
				Reason:  c.Reason,
				Detail:  c.Detail,
			},
		})
	}
}

func hookCheck422(w http.ResponseWriter, d Deps, path, msg string) {
	writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
		Errors []wire.ConfigError `json:"errors"`
	}{Errors: []wire.ConfigError{{Path: path, Message: msg}}})
}

// handleStorageZFSKey serves POST /api/storages/zfs/key → 200 {key} | 500 (contracts §1,
// quince#818 piece B).
//
// It answers *"what key should I put on my ZFS host?"* — and it can only ever answer about ONE path,
// `config.DefaultZFSKeyPath`, which is quince's own `/data/keys/zfs`.
//
// NO PATH IN THE REQUEST, and that is the security shape rather than a missing feature. A
// caller-supplied path would make this an authenticated *write-a-file-anywhere* primitive whose
// contents happen to be a private key; refusing to take one means the endpoint has no reachable
// target but its own. An operator who keeps a key elsewhere sets `ssh_key` by hand and never presses
// this button — the field stays settable for exactly that case.
//
// IT DISCOVERS BEFORE IT GENERATES, which is the property that protects existing installs. A key
// already at that path may have its public half in an `authorized_keys` on a host quince cannot see,
// so replacing it would break a working storage silently, with the failure surfacing at the next
// backup rather than here. `EnsureZFSKey` therefore has no force flag, and a file that is not a key
// is a REFUSAL rather than a reason to overwrite.
//
// THE PRIVATE HALF NEVER REACHES THE RESPONSE. `storage.ZFSKey` carries only the public line, the
// complete `authorized_keys` line, the path, and whether it was just created — the same discipline
// backup passwords follow. `Created` is on the wire because the form must be able to say *found
// yours* rather than *made you one*.
func (d Deps) handleStorageZFSKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := d.ZFSKeyPath
		if path == "" {
			path = config.DefaultZFSKeyPath
		}
		k, err := storage.EnsureZFSKey(path)
		if err != nil {
			// THE DAEMON'S OWN SENTENCE, because the two reachable failures both need the operator to
			// do something specific: a `/data` that is not writable, and a file at that path which is
			// not a key. A generic "could not create key" would name neither.
			d.Log.Error("zfs key", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "zfs_key", err.Error())
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StorageZFSKeyResponse{
			Key: wire.StorageZFSKey{
				Path:           k.Path,
				PublicKey:      k.PublicKey,
				AuthorizedKeys: k.AuthorizedKeys,
				Created:        k.Created,
			},
		})
	}
}
