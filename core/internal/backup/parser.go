package backup

import (
	"regexp"
	"strconv"
	"strings"
)

// The parser is transcript-grounded, not guessed: its recognizers come from the real
// idevicebackup2 output captured in the lab (core/internal/backup/testdata/transcripts). A line
// it does not recognize changes no state and is passed verbatim to the log — robust to version
// drift (design §2 backup supervisor: "unknown lines are logged, never fatal").
type parsed struct {
	phaseReceiving  bool     // "Receiving files" / "Sending files"
	waitingPasscode bool     // "*** Waiting for passcode ... ***"
	success         bool     // "Backup Successful."
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
