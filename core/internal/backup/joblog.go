package backup

import "sync"

// The live job log is an in-memory ring, not a DB column: the job.log WS stream is not replayable
// (qn.1 review), so GET /api/jobs/{id}/log serves the full-so-far tail from here — bounded per job
// and across jobs so a long backup or a busy history can't grow memory without bound. It survives
// a WS reconnect (same process); it does not survive a restart (a restarted job is connection_lost).
const (
	maxJobLogBytes = 256 * 1024 // per-job tail cap
	maxJobLogs     = 256        // number of jobs retained (LRU by first-append order)
)

type logStore struct {
	mu    sync.Mutex
	logs  map[string][]byte
	order []string // insertion order for LRU eviction
}

func newLogStore() *logStore { return &logStore{logs: map[string][]byte{}} }

// append adds a chunk to a job's log tail, capping the tail and evicting the oldest job's log
// once the retained-job count is exceeded.
func (s *logStore) append(jobID, chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf, ok := s.logs[jobID]
	if !ok {
		s.order = append(s.order, jobID)
		for len(s.order) > maxJobLogs {
			delete(s.logs, s.order[0])
			s.order = s.order[1:]
		}
	}
	buf = append(buf, chunk...)
	if len(buf) > maxJobLogBytes {
		buf = buf[len(buf)-maxJobLogBytes:]
	}
	s.logs[jobID] = buf
}

// get returns a job's full-so-far log tail; ok=false when this store has never seen the job.
func (s *logStore) get(jobID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf, ok := s.logs[jobID]
	if !ok {
		return "", false
	}
	return string(buf), true
}
