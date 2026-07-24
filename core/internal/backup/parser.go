package backup

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// scanFrames is the bufio.SplitFunc the supervisor scans idevicebackup2 output with. It splits like
// bufio.ScanLines but ALSO treats a bare carriage return as a line terminator, because the tool
// redraws its progress bar in place with '\r' and NO newline ("[..] 2% (23.2 MB/938.6 MB)\r[..] 4%
// …"). Under ScanLines those redraws accumulate into one multi-kilobyte "line" until a newline (or
// the 1 MB buffer cap) finally arrives — which mangles the log pane AND makes the byte regex match
// the OLDEST frame in the blob, so the byte counter reads stale (gate-11 finding #3, (cj)). Splitting
// on '\r' yields one token per frame: the parser sees the LATEST bytes, the pane stays clean, and the
// pure-progress frames are dropped from the log (handleLine), killing the bloat. '\r\n' is one break.
func scanFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	i := bytes.IndexAny(data, "\r\n")
	if i < 0 {
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil // no terminator yet — ask for more
	}
	if data[i] == '\r' {
		// A trailing '\r' with more possibly coming could be the '\r' of a '\r\n' split across reads;
		// wait for the next byte so the '\n' is folded into this same break, not emitted as an empty line.
		if i == len(data)-1 && !atEOF {
			return 0, nil, nil
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			return i + 2, data[:i], nil // fold \r\n
		}
		return i + 1, data[:i], nil
	}
	return i + 1, data[:i], nil // '\n'
}

// The parser is transcript-grounded, not guessed: its recognizers come from the real
// idevicebackup2 output captured in the lab (core/internal/backup/testdata/transcripts). A line
// it does not recognize changes no state and is passed verbatim to the log — robust to version
// drift (design §2 backup supervisor: "unknown lines are logged, never fatal").
type parsed struct {
	phaseReceiving  bool     // "Receiving files" / "Sending files"
	waitingPasscode bool     // "*** Waiting for passcode ... ***"
	success         bool     // "Backup Successful."
	failReason      string   // the tool's OWN words for a failure (see reErrorCode)
	overallPercent  *float64 // from "NN% Finished" (the only trustworthy OVERALL percent)
	bytesDone       int64    // best-effort current-transfer bytes from "(X/Y)"
	bytesTotal      int64
	hasBytes        bool
}

var (
	// "[.....]  38% Finished" — the overall progress. The per-file "100% (x/x)" bars are NOT
	// overall percent (every finished file shows 100%), so only "Finished" drives job.percent.
	reFinished = regexp.MustCompile(`(\d+)%\s+Finished`)
	// "[..]  2% (23.2 MB/938.6 MB)" — a size pair; best-effort current-transfer bytes.
	reBytes = regexp.MustCompile(`\(([\d.]+)\s*([KMGT]?B)/([\d.]+)\s*([KMGT]?B)\)`)
	// "ErrorCode 105: Insufficient free disk space on drive to back up (MBErrorDomain/105)" —
	// the DEVICE's own explanation of a refusal. Captured verbatim so a failed job can say what
	// went wrong instead of "exit status 151" (qn.4c lab finding: 151 == MBErrorDomain 105, and
	// the bare exit code told the Operator nothing). Also matches a plain "ERROR: <text>" line.
	reErrorCode = regexp.MustCompile(`^(?:ErrorCode \d+: |ERROR: )(.+)$`)
)

// parseLine classifies one line of idevicebackup2 output.
func parseLine(line string) parsed {
	var p parsed
	l := strings.TrimSpace(line)
	switch {
	case strings.Contains(l, "Waiting for passcode"):
		p.waitingPasscode = true
	case strings.Contains(l, "Backup Successful"):
		p.success = true
	}
	if strings.Contains(l, "Receiving files") || strings.Contains(l, "Sending files") {
		p.phaseReceiving = true
	}
	if m := reFinished.FindStringSubmatch(l); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			p.overallPercent = &v
		}
	}
	if m := reBytes.FindStringSubmatch(l); m != nil {
		p.bytesDone = parseSize(m[1], m[2])
		p.bytesTotal = parseSize(m[3], m[4])
		p.hasBytes = true
	}
	if m := reErrorCode.FindStringSubmatch(l); m != nil {
		p.failReason = strings.TrimSpace(m[1])
	}
	return p
}

// parseSize converts an idevicebackup2 size ("61.2", "MB") to bytes (best-effort).
func parseSize(num, unit string) int64 {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(unit) {
	case "B":
		return int64(f)
	case "KB":
		return int64(f * 1024)
	case "MB":
		return int64(f * 1024 * 1024)
	case "GB":
		return int64(f * 1024 * 1024 * 1024)
	case "TB":
		return int64(f * 1024 * 1024 * 1024 * 1024)
	}
	return int64(f)
}
