package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/novkostya/quince/core/internal/wire"
)

const (
	defaultJobsLimit = 50
	maxJobsLimit     = 200
)

func (d Deps) handleDevices() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices := d.Devices.Devices()
		if devices == nil {
			devices = []wire.Device{}
		}
		writeJSON(w, d.Log, http.StatusOK, wire.DevicesResponse{Devices: devices})
	}
}

func (d Deps) handleDevice() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, ok := d.Devices.Device(r.PathValue("udid"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such device")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, dev)
	}
}

func (d Deps) handleJobs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := defaultJobsLimit
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > maxJobsLimit {
			limit = maxJobsLimit
		}
		jobs, next := d.Jobs.Jobs(q.Get("udid"), q.Get("cursor"), limit)
		if jobs == nil {
			jobs = []wire.Job{}
		}
		resp := wire.JobsResponse{Jobs: jobs}
		if next != "" {
			resp.NextCursor = &next
		}
		writeJSON(w, d.Log, http.StatusOK, resp)
	}
}

func (d Deps) handleJob() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, ok := d.Jobs.Job(r.PathValue("id"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such job")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, job)
	}
}

// handleJobLog serves GET /api/jobs/{id}/log as text/plain (contracts §1: the full log
// so far; the live tail is the WS job.log stream). 404 when the job is unknown.
func (d Deps) handleJobLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log, ok := d.Jobs.JobLog(r.PathValue("id"))
		if !ok {
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such job")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, log)
	}
}

func (d Deps) handleVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		versions := d.Versions.Versions(r.URL.Query().Get("udid"))
		if versions == nil {
			versions = []wire.Version{}
		}
		writeJSON(w, d.Log, http.StatusOK, wire.VersionsResponse{Versions: versions})
	}
}
