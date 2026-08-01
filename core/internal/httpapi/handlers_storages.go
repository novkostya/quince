package httpapi

import (
	"net/http"

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

// handleStorageRecheck serves POST /api/storages/{id}/recheck → 200 {storage} | 404.
//
// The button behind *plug the disk in and press it* (Operator ruling 2026-08-01): reachability may
// change without a restart, where the storage LIST still needs one.
//
// 200 rather than 202 because the check is a stat, not a job — a 202 would imply something to poll,
// and this rung has no queue by ruling. Per-storage rather than global because the user plugged in
// ONE disk, and a global re-check would make one press pay for every unreachable root.
func (d Deps) handleStorageRecheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, ok := d.Storages.Recheck(r.PathValue("id"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "no_such_storage",
				"no storage with that id is declared")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.StorageResponse{Storage: s})
	}
}
