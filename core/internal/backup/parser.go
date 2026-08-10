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

	// TWO FLAGS, BECAUSE ONE PREDICATE WAS ANSWERING TWO QUESTIONS (quince#809 review).
	//
	// `hasBytes` was named for what it MATCHED — a line carrying a size pair — and was consumed by
	// callers who wanted what it MEANT: the log filter asking *is this a redraw frame*, and the
	// progress publisher asking *are these figures worth publishing*. Those coincided only while
	// the pattern was narrow. Widening it to catch the tool's spelled-out `Bytes` would have
	// silently started publishing per-file figures three orders of magnitude below the job total —
	// landing in quince#808's territory with no test able to see it.
	//
	//	sizeFrame — a progress redraw, whatever the unit. The LOG FILTER's question.
	//	hasBytes  — figures the publisher may use. UNCHANGED by quince#809: plain `Bytes` frames
	//	            still publish nothing, exactly as before. Whether they SHOULD is quince#808's
	//	            question and is deliberately not answered here.
	sizeFrame bool
	hasBytes  bool
}

var (
	// "[.....]  38% Finished" — the overall progress. The per-file "100% (x/x)" bars are NOT
	// overall percent (every finished file shows 100%), so only "Finished" drives job.percent.
	reFinished = regexp.MustCompile(`(\d+)%\s+Finished`)
	// "[..]  2% (23.2 MB/938.6 MB)" — a size pair; best-effort current-transfer bytes.
	//
	// `Bytes` IS A UNIT THE TOOL WRITES, and `[KMGT]?B` does not match it (quince#809). The
	// alternation is anchored at `\(`, so `([KMGT]?B)` can only consume the `B` of `Bytes` and the
	// required `/` then fails — every sub-KB frame missed the pattern entirely and was logged
	// verbatim. Every file's transfer starts under 1 KB and small files never leave that range, so
	// the log filled with near-identical `0% (16 Bytes/…)`, `(32 Bytes/…)` at frame rate.
	//
	// THE ALTERNATION ORDER DOES NOT MATTER, and this comment claimed the opposite until it was
	// run. `[KMGT]?B|Bytes` is the obvious trap — leftmost-first matches the bare `B` of `Bytes`,
	// leaves `ytes`, and fails the required `/`, reproducing the bug inside its own fix. It does
	// not happen: Go's regexp keeps whichever branch lets the WHOLE pattern match, so a branch that
	// cannot reach the `/` is discarded. MEASURED — the suite below passes with both orders.
	// `Bytes` is written first anyway: it reads in the order the reader cares about and costs
	// nothing. What DOES matter is that the unit is captured whole, which is asserted below,
	// because `strings.EqualFold(m[2], "Bytes")` is what keeps these frames out of the published
	// figures — a partial `B` capture would silently let them through.
	reBytes = regexp.MustCompile(`\(([\d.]+)\s*(Bytes|[KMGT]?B)/([\d.]+)\s*(Bytes|[KMGT]?B)\)`)
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
		// Every size pair is a redraw frame — that is the log filter's question, and it is the one
		// quince#809 is about.
		p.sizeFrame = true
		// PUBLISHING IS DELIBERATELY UNCHANGED. A frame whose CURRENT figure is in plain `Bytes` is
		// a per-file counter at the start of a file, three orders of magnitude below the job total;
		// letting it set the published figures would make `bytes_done` jitter between scales for no
		// gain, inside quince#808's open question about those numbers being per-message at all.
		// So these frames stop flooding the log and go on publishing nothing, which is exactly
		// what they did before — this fix is a strict no-op on r.BytesDone / r.BytesTotal.
		if !strings.EqualFold(m[2], "Bytes") {
			p.bytesDone = parseSize(m[1], m[2])
			p.bytesTotal = parseSize(m[3], m[4])
			p.hasBytes = true
		}
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
	// `BYTES` IS UNREACHED TODAY AND IS HERE ON PURPOSE (quince#809). parseLine does not call this
	// for a `Bytes` frame — those set `sizeFrame` and stop, so nothing publishes their figures. The
	// arm exists because the alternative to an unreached case is a silent 0: whoever later decides
	// that `Bytes` frames SHOULD publish (quince#808's question) removes one `if` in parseLine, and
	// without this they would get zeroes with nothing to say why.
	case "B", "BYTES":
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
