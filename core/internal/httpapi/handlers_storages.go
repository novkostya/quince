package httpapi

import (
	"errors"
	"io"
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

// handleStorageZFSHelper serves GET /api/storages/zfs/helper → 200 {script, path} (contracts §1,
// quince#818 piece C; quince#985).
//
// It answers the question the form used to leave to the operator: *"what exactly do I put on the ZFS
// host?"*
//
// IT TAKES NO PARAMETER AND CANNOT FAIL, WHICH IS quince#985's DOING. The script used to come back
// with `PARENT=` set to the dataset typed on the same screen — so every install's file differed,
// while `ZFSHelperPath` said there was one place to put it. A second zfs storage saved its helper
// over the first's and the first broke at its next commit. The dataset now rides in the
// `authorized_keys` forced command, which is per key, so the file is the same bytes everywhere.
//
// WHAT THAT REMOVES: the `422` (there is no caller value left to be unsafe) and the `500` (there is
// no substitution left to no-op). The dataset's refusal moved to `POST /api/storages/zfs/key`, which
// is now where it is interpolated — see `TestHelperTakesItsParentFromTheForcedCommand` for the
// build-time guard that replaced the runtime one.
//
// NO STATE, NO WRITE, AND NOTHING ABOUT THIS INSTALL — more so than before: the response is a
// constant. That is why it is a GET, and it is what makes serving it to an unauthenticated fetch a
// question about convenience rather than about secrets.
func (d Deps) handleStorageZFSHelper() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, d.Log, http.StatusOK, wire.StorageZFSHelperResponse{
			Script:     storage.ZFSHelperScript(),
			Path:       storage.ZFSHelperPath,
			SourcePath: ZFSHelperRoute,
		})
	}
}

// handleZFSHelperPlain serves GET /zfs/helper → 200 text/plain, the helper script and nothing else.
//
// IT EXISTS SO THE HOST THAT NEEDS THE FILE CAN FETCH IT. The browser reading the add-storage form
// is not the machine the helper is installed on; the ZFS host is, and it has no session, no cookie
// jar and frequently no browser. `GET /api/storages/zfs/helper` answers `401` to it, correctly, and
// that is why this second door exists rather than the first being opened.
//
// UNAUTHENTICATED, AND HERE IS EXACTLY WHAT THAT COSTS. The response is a compile-time constant —
// the same bytes for every install of a version since quince#985, when the operator's dataset moved
// into the `authorized_keys` forced command. It reads no config, touches no `/data`, and names no
// dataset, host, user or key. The same file is public in the repository. So what a stranger who can
// reach this port learns is *"a quince of about this version is here"*, which the login page already
// tells them.
//
// THAT IS A PROPERTY OF THE SCRIPT, NOT OF THIS HANDLER, and it is the thing to re-check before
// anyone makes the script per-install again: the day it carries one operator's dataset, this route
// is a disclosure and must move behind the session with it. `TestZFSHelperPlainServesAConstant` is
// the guard, and it fails rather than warns.
//
// OUTSIDE THE `/api/` CHAIN, so it has no `authGuard`, no `csrfGuard` (a GET carries no CSRF risk)
// and no `setupGuard` — the last one matters: installing the helper is something an operator does
// BEFORE the first storage exists, so a readiness gate here would 503 the one moment it is wanted.
//
// `text/plain`, NOT AN ATTACHMENT. `curl <url>` should print the script, because the whole argument
// for offering this at all is that a file you are about to run as root is one you can read first. A
// `Content-Disposition` would make the default action *download silently*, which is the shape the
// Operator declined.
// ZFSHelperRoute is where the plain-text helper is served, and it is a constant because THREE places
// have to agree about it: the route registration, the `source_path` on the JSON response, and the
// address the form renders into a `curl` line. The form takes it off the wire rather than hardcoding
// it, so moving the route cannot leave a link pointing at a 404 that nobody notices until an
// operator tries to install a helper.
const ZFSHelperRoute = "/zfs/helper"

func (d Deps) handleZFSHelperPlain() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		// A shell script is exactly the sort of body a browser might be talked into sniffing as
		// something else; `securityHeaders` already sets this globally, and it is restated here
		// because this is the one route whose body is a program.
		h.Set("X-Content-Type-Options", "nosniff")
		if _, err := io.WriteString(w, storage.ZFSHelperScript()); err != nil {
			d.Log.Warn("zfs helper: writing the plain-text helper", "error", err)
		}
	}
}

// handleStorageZFSKey serves POST /api/storages/zfs/key {parent_dataset} → 200 {key} | 422 | 500
// (contracts §1, quince#818 piece B; quince#985).
//
// It answers *"what key should I put on my ZFS host?"* — one key per PARENT DATASET, at a path
// quince DERIVES from that dataset under `config.DefaultZFSKeyDir` (quince#989).
//
// ONE PATH FOR EVERY STORAGE WAS A BUG, NOT A LIMITATION, and it is worth naming because the symptom
// pointed elsewhere. A forced command is a property of a key — sshd uses the first `authorized_keys`
// line whose key matches and stops looking — so one key can be confined to exactly one parent. Asked
// about a second storage's dataset, this endpoint's discover-before-generate found the FIRST key and
// rendered a line pairing key A with dataset B. That line is inert; storage B stays confined to
// dataset A. And it reads healthy: `capacity` takes no argument and answers for whatever `$PARENT`
// the live forced command names, so *Test helper* returns dataset A's numbers and only `create`
// fails, at commit.
//
// IT DISCOVERS BEFORE IT GENERATES, PER DERIVED PATH. A key already there may have its public half
// in an `authorized_keys` on a host quince cannot see, so replacing it would break a working storage
// silently, with the failure surfacing at the next backup rather than here. `EnsureZFSKey` therefore
// has no force flag, and a file that is not a key is a REFUSAL rather than a reason to overwrite.
// The property has to hold for EACH key rather than once globally, which is what the derivation
// makes possible.
//
// THE PRIVATE HALF NEVER REACHES THE RESPONSE. `storage.ZFSKey` carries only the public line, the
// complete `authorized_keys` line, the path, and whether it was just created — the same discipline
// backup passwords follow. `Created` is on the wire because the form must be able to say *found
// yours* rather than *made you one*.
//
// THE BODY IS ONE FIELD AND IT IS NOT A PATH, which keeps the security shape §1 states — and the
// derivation does not weaken it. A caller-supplied *path* would make this a write-a-file-anywhere
// primitive; a caller-supplied *dataset* is validated, then escaped into a single filename component
// under a directory quince chooses. `config.ZFSKeyPathFor` carries why that is escaping rather than
// mirrored directories: `datasetPattern` accepts `tank/../../etc`, and a mirrored tree would resolve
// it outside the key directory.
//
// 422 IS NEW, AND IT IS THE ONE THE HELPER ENDPOINT GAVE UP (quince#985). The dataset goes inside
// `command="…"` in a file sshd parses, so an unsafe name is refused rather than escaped — the same
// rule, at the place where the interpolation now happens.
func (d Deps) handleStorageZFSKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wire.StorageZFSKeyRequest
		if err := decodeJSON(r, &req); err != nil {
			hookCheck422(w, d, "parent_dataset", "the request body could not be read as JSON")
			return
		}
		dir := d.zfsKeyDir()
		// A READ, NOT A GENERATE (quince#1038). `ZFSKeyFor` answers what the key situation is for
		// this dataset and writes nothing per call beyond the single `.pending` key the first time
		// one is needed. It used to derive a path per dataset and generate into it, so the debounced
		// re-fetch behind the form wrote a private key for every prefix the operator paused on.
		k, err := storage.ZFSKeyFor(dir, req.ParentDataset)
		if err != nil {
			if errors.Is(err, storage.ErrUnsafeDataset) {
				// SAME FIELD, SAME FACTS as `CheckHook`'s refusal and the probe's — the buttons sit
				// inches apart on one form, so a user who mistypes this must not get a different
				// explanation depending on which one they pressed first.
				hookCheck422(w, d, "parent_dataset",
					"must be a ZFS dataset name like `rpool/quince` — a pool and a path inside it, "+
						"with no leading `/`. This is not the folder path from the field above. "+
						"quince puts it straight into the `authorized_keys` line, so a name it "+
						"cannot vouch for is refused rather than escaped")
				return
			}
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
				Pending:        k.Pending,
				LandsAt:        k.LandsAt,
				Fingerprint:    k.Fingerprint,
			},
		})
	}
}

// handleStorageZFSHostKey serves POST /api/storages/zfs/hostkey → 200 {found, host_key, reason} |
// 422 (contracts §1, quince#912).
//
// THE FIRST HALF OF A TWO-STEP CEREMONY. It asks the host what key it offers and shows the
// FINGERPRINT. It authenticates nothing, sends no credential, and writes nothing — see
// `storage.ScanHostKey`.
//
// EVERY ANSWER ABOUT A REAL ADDRESS IS A 200, including "nothing answered", for the same reason
// `probe` and `probe/hook` do it: a host that is not up yet is the ANSWER to the question, not a
// failure to answer it, and the form renders it beside the same field. Only a malformed question —
// no host — is a 422.
//
// IT DIALS AN ADDRESS THE CALLER SUPPLIED, which is the thing to declare. It adds no capability an
// authenticated admin lacks — `PUT /api/config` already stores an `ssh_host` that quince connects to
// at the next job — and it is strictly narrower than the hook check beside it, which runs a
// subprocess: this opens a TCP connection, reads one handshake, and closes it.
func (d Deps) handleStorageZFSHostKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wire.StorageZFSHostKeyRequest
		if err := decodeJSON(r, &req); err != nil || req.SSHHost == "" {
			hookCheck422(w, d, "ssh_host",
				"must not be empty — this is the host whose key quince needs to trust")
			return
		}
		hk, err := storage.ScanHostKey(r.Context(), req.SSHHost, req.SSHPort)
		if err != nil {
			writeJSON(w, d.Log, http.StatusOK, wire.StorageZFSHostKeyResponse{
				Found: false, Reason: err.Error(),
			})
			return
		}
		// WHAT known_hosts ALREADY SAYS, answered here rather than left for the trust call to
		// discover. It reads the same file the transport will and makes the same comparison
		// TrustHostKey makes — storage.HostKeyTrustState shares that code deliberately, so the scan
		// cannot report `trusted` while the trust call disagrees.
		knownHosts := d.ZFSKnownHostsPath
		if knownHosts == "" {
			knownHosts = config.DefaultZFSKnownHosts
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StorageZFSHostKeyResponse{
			Found: true,
			Trust: storage.HostKeyTrustState(knownHosts, hk.Line),
			HostKey: &wire.StorageZFSHostKey{
				Host: hk.Host, Port: hk.Port, KeyType: hk.Type,
				Fingerprint: hk.Fingerprint, Line: hk.Line,
			},
		})
	}
}

// handleStorageZFSHostKeyTrust serves POST /api/storages/zfs/hostkey/trust → 200 {trusted, path} |
// 422 | 500 (contracts §1, quince#912).
//
// THE SECOND HALF. It records the line the operator confirmed — the one from the scan, passed back
// unchanged — and never re-scans. See `storage.TrustHostKey` for why that distinction is the whole
// point of the ceremony rather than an implementation detail.
//
// A CHANGED HOST KEY IS A 422, NOT AN OVERWRITE. It means either a rebuilt host or an impersonation,
// quince cannot tell which, and resolving it silently would make this button trust an attacker as
// readily as the real host. The daemon's sentence names both possibilities and the file.
func (d Deps) handleStorageZFSHostKeyTrust() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req wire.StorageZFSHostKeyTrustRequest
		if err := decodeJSON(r, &req); err != nil || req.Line == "" {
			hookCheck422(w, d, "line",
				"must carry the known_hosts line quince showed you — trust records what you "+
					"confirmed, and never re-reads the host")
			return
		}
		path := d.ZFSKnownHostsPath
		if path == "" {
			path = config.DefaultZFSKnownHosts
		}
		if err := storage.TrustHostKey(path, req.Line); err != nil {
			// The refusals here are the operator's business — a changed key, or a line quince will
			// not write — and each names what to do. A write failure is quince's own and is a 500.
			hookCheck422(w, d, "line", err.Error())
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StorageZFSHostKeyTrustResponse{
			Trusted: true, Path: path,
		})
	}
}
