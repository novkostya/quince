package storage

import (
	"fmt"
	"net/http"
	"strings"
)

// RepairWorking discards a device's dirty working/ on ONE storage, resolving WHICH from an optional
// storage id (contracts §1 `POST /api/devices/{udid}/reset-working {storage_id?}`; Operator ruling
// 2026-08-02, quince#448).
//
// The endpoint is DEVICE-scoped and, after qn.6c, a device can have a dirty working/ under more
// than one storage — so "reset this device's working copy" stopped having one answer. It resolves:
//
//	storage_id present, usable      reset exactly that one
//	storage_id present, unknown     404, matching unknown-device
//	storage_id present, unreachable 409, carrying that storage's own unreachable_reason
//	omitted, 0 dirty                202 — already clean, today's idempotent behaviour unchanged
//	omitted, exactly 1 dirty        reset it, and NAME it in the reason
//	omitted, 2 or more dirty        409, listing them, saying to name one
//
// THE 2-OR-MORE REFUSAL IS THE RULING'S POINT, and it is the same answer quince#435 gave for a job
// that names no storage: refused with a reason naming the candidates, never silently redirected. A
// backup's dirty working/ is a RESUMABLE MULTI-HOUR PARTIAL — over Wi-Fi, hours — so "reset all"
// would discard a transfer on a disk the user was not thinking about. Reset is that same ambiguity
// pointed at deletion instead of writing, so it gets the same answer: refuse and name, rather than
// guess well.
//
// Callers reach here past the engine's own checks (unknown device 404, backup running 409), which
// keep their order and wording.
func (m *Manager) RepairWorking(udid, storageID string) (int, string) {
	slots := m.slotsSnapshot()

	if storageID != "" {
		for _, s := range slots {
			if s.StorageID != storageID {
				continue
			}
			if !s.Usable() {
				// The job path answers 409 for "this storage is not serving", and one endpoint
				// inventing its own code for a condition another already has is the drift worth
				// avoiding (the ruling says to match the job path rather than its own text).
				return http.StatusConflict, fmt.Sprintf(
					"storage %q is not reachable (%s): %s", s.Name, s.UnreachableCode, s.UnreachableReason)
			}
			return m.repairOn(s, udid)
		}
		return http.StatusNotFound, "no storage with that id is declared"
	}

	// UNREACHABLE STORAGES ARE NAMED, NEVER SILENTLY SKIPPED. One that cannot be read cannot be
	// inspected for dirtiness and cannot be reset, so the answer must say which storages were not
	// looked at even when it successfully resets another. "No silent caps or fallbacks" applies to
	// the ones we could not examine, not only to the ones we changed.
	var dirty, blind []Slot
	for _, s := range slots {
		if !s.Usable() {
			blind = append(blind, s)
			continue
		}
		if s.Backend.Dirty(udid) {
			dirty = append(dirty, s)
		}
	}

	switch len(dirty) {
	case 0:
		return http.StatusAccepted, "nothing to reset — no working copy on any storage" + notInspected(blind)
	case 1:
		status, reason := m.repairOn(dirty[0], udid)
		if status == http.StatusAccepted {
			reason += notInspected(blind)
		}
		return status, reason
	default:
		return http.StatusConflict, fmt.Sprintf(
			"%d storages have a working copy for this device — name one with `storage_id`: %s%s",
			len(dirty), nameList(dirty), notInspected(blind))
	}
}

func (m *Manager) repairOn(s Slot, udid string) (int, string) {
	if err := s.Backend.RepairWorkingCopy(udid); err != nil {
		m.log.Error("storage: reset working failed", "udid", udid, "storage", s.Name, "error", err)
		return http.StatusInternalServerError, "reset failed on storage " + s.Name + ": " + err.Error()
	}
	// A DESTRUCTIVE ACTION WHOSE LOG DOES NOT SAY WHICH DISK is not much of a record.
	m.appendAudit("working.reset", fmt.Sprintf("udid=%s storage=%s (%s)", udid, s.Name, s.StorageID))
	m.log.Info("storage: reset — discarded dirty working copy",
		"udid", udid, "storage", s.Name, "storage_id", s.StorageID)
	return http.StatusAccepted, "discarded the working copy on storage " + s.Name
}

func nameList(ss []Slot) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, fmt.Sprintf("%s (%s)", s.Name, s.StorageID))
	}
	return strings.Join(out, ", ")
}

// notInspected renders the storages that could not be examined, so a successful reset never implies
// the others were checked and found clean.
func notInspected(blind []Slot) string {
	if len(blind) == 0 {
		return ""
	}
	names := make([]string, 0, len(blind))
	for _, s := range blind {
		names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.UnreachableCode))
	}
	return " — NOT inspected, unreachable: " + strings.Join(names, ", ")
}

// StorageIDByName maps a declared storage's NAME to its id, for the CLI's `--storage <name>`.
//
// The CLI takes a name because that is what an operator has in `config.yml`; the API takes an id
// because that is what a client got from `GET /api/storages`. Empty in, empty out — which is how
// "no --storage" reaches RepairWorking as the omitted case.
func (m *Manager) StorageIDByName(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	for _, s := range m.slotsSnapshot() {
		if s.Name == name {
			return s.StorageID, nil
		}
	}
	return "", fmt.Errorf("no storage named %q is declared", name)
}
