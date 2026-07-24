package backup

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// scanFrames must split idevicebackup2's carriage-return progress redraws into one token per frame,
// so the parser reads the LATEST bytes (not the oldest) and the log pane isn't a mangled blob
// (gate-11 finding #3, (cj)). \n and \r\n are ordinary breaks; a bare \r is a break too.
func TestScanFramesSplitsCarriageReturnFrames(t *testing.T) {
	// A \r-joined progress blob (no interior newline) followed by a normal \n line.
	blob := "[.] 2% (23.2 MB/938.6 MB)\r[.] 40% (375 MB/938.6 MB)\r[.] 98% (920 MB/938.6 MB)\r\nBackup Successful.\n"
	sc := bufio.NewScanner(strings.NewReader(blob))
	sc.Split(scanFrames)
	var frames []string
	for sc.Scan() {
		frames = append(frames, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	want := []string{
		"[.] 2% (23.2 MB/938.6 MB)",
		"[.] 40% (375 MB/938.6 MB)",
		"[.] 98% (920 MB/938.6 MB)",
		"Backup Successful.",
	}
	if len(frames) != len(want) {
		t.Fatalf("frames = %d %q, want %d", len(frames), frames, len(want))
	}
	for i := range want {
		if frames[i] != want[i] {
			t.Fatalf("frame[%d] = %q, want %q", i, frames[i], want[i])
		}
	}
	// The LAST byte frame must be the one the parser trusts (latest, not stale).
	last := parseLine(frames[2])
	if !last.hasBytes || last.bytesDone < 900*1024*1024 {
		t.Fatalf("latest frame parsed bytesDone=%d, want ~920 MB", last.bytesDone)
	}
}

// A '\r\n' split across two reads must fold into one break, never emit a spurious empty line.
func TestScanFramesFoldsCRLFAcrossReads(t *testing.T) {
	// iotest-style: feed the reader in two chunks so the \r ends chunk 1 and \n starts chunk 2.
	pr := &twoChunkReader{chunks: [][]byte{[]byte("line-a\r"), []byte("\nline-b\n")}}
	sc := bufio.NewScanner(pr)
	sc.Split(scanFrames)
	var frames []string
	for sc.Scan() {
		frames = append(frames, sc.Text())
	}
	want := []string{"line-a", "line-b"}
	if len(frames) != len(want) {
		t.Fatalf("frames = %q, want %q (no empty line from a split \\r\\n)", frames, want)
	}
	for i := range want {
		if frames[i] != want[i] {
			t.Fatalf("frame[%d] = %q, want %q", i, frames[i], want[i])
		}
	}
}

// twoChunkReader hands out its chunks one Read at a time, so a token terminator can straddle a
// read boundary (exercises scanFrames' request-more-data path for a trailing \r).
type twoChunkReader struct {
	chunks [][]byte
	i      int
}

func (r *twoChunkReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	return n, nil
}
