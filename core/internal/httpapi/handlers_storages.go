package httpapi

import (
	"net/http"
	"path/filepath"

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
