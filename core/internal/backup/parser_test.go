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

// `Bytes` IS A UNIT THE TOOL WRITES, and the redraw filter did not recognise it (quince#809).
//
// THE TABLE IS PER UNIT because that is what was missing: every fixture in this suite used `MB`, so
// a whole class of frame reached neither consumer in a test and the pattern was free to be wrong for
// as long as it was.
//
// TWO COLUMNS, BECAUSE ONE PREDICATE WAS ANSWERING TWO QUESTIONS. `sizeFrame` is the log filter's —
// *is this a redraw* — and must now be true for every unit. `hasBytes` is the progress publisher's —
// *are these figures worth publishing* — and is deliberately UNCHANGED, so a per-file `Bytes` counter
// three orders of magnitude below the job total does not start driving `bytes_done` while quince#808
// is open about those numbers at all. The `want` columns differing on the `Bytes` rows IS the fix.
func TestParseLineRecognisesEveryProgressUnit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		line          string
		wantSizeFrame bool
		wantHasBytes  bool
		wantDone      int64
	}{
		{"plain Bytes — the missed class", "[=  ]   0% (16 Bytes/1.4 MB)", true, false, 0},
		{"plain Bytes, larger", "[=  ]   0% (784 Bytes/122.0 MB)", true, false, 0},
		{"B", "[=  ]   0% (16 B/1.4 MB)", true, true, 16},
		{"KB", "[=  ]   1% (12.0 KB/1.4 MB)", true, true, 12 * 1024},
		{"MB — the control, unchanged", "[== ]   2% (23.2 MB/938.6 MB)", true, true, 23*1024*1024 + 209715},
		{"GB", "[== ]  50% (1.5 GB/3.0 GB)", true, true, 1610612736},
		{"no size pair at all", "Receiving files", false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := parseLine(tc.line)
			if p.sizeFrame != tc.wantSizeFrame {
				t.Errorf("sizeFrame = %v, want %v — this decides whether the LOG drops the frame; "+
					"a false here is quince#809, hundreds of these lines at frame rate", p.sizeFrame, tc.wantSizeFrame)
			}
			if p.hasBytes != tc.wantHasBytes {
				t.Errorf("hasBytes = %v, want %v — this decides whether the frame's figures are "+
					"PUBLISHED as bytes_done/bytes_total, and quince#809 must not change it",
					p.hasBytes, tc.wantHasBytes)
			}
			if tc.wantHasBytes && p.bytesDone != tc.wantDone {
				t.Errorf("bytesDone = %d, want %d", p.bytesDone, tc.wantDone)
			}
			if !tc.wantHasBytes && p.bytesDone != 0 {
				t.Errorf("bytesDone = %d on a frame that must publish nothing — a per-file Bytes "+
					"counter reaching bytes_done is what this scoping exists to prevent", p.bytesDone)
			}
		})
	}
}

// THE UNIT MUST BE CAPTURED WHOLE, because the publishing guard is a string comparison on it:
// `strings.EqualFold(m[2], "Bytes")` is what keeps a per-file Bytes counter out of `bytes_done`,
// and a capture of just `B` would silently let it through — the frame would stop flooding the log
// AND start publishing, which is exactly the combination quince#809's review warned about.
//
// THIS TEST DOES NOT PIN THE ALTERNATION ORDER, and an earlier revision of it claimed to. The claim
// was that `[KMGT]?B|Bytes` would match the bare `B`, leave `ytes`, fail the required `/`, and so
// reproduce quince#809 inside its own fix. MEASURED: it does not. Go's regexp keeps whichever
// branch lets the WHOLE pattern match, so the branch that cannot reach the `/` is discarded, and
// this assertion passes with either order. Kept — what it actually asserts is load-bearing —
// renamed and re-commented so it stops claiming something about the engine that is not true.
func TestBytesUnitIsCapturedWhole(t *testing.T) {
	m := reBytes.FindStringSubmatch("[=  ]   0% (16 Bytes/1.4 MB)")
	if m == nil {
		t.Fatal("a plain-Bytes frame does not match reBytes at all (quince#809)")
	}
	if m[2] != "Bytes" {
		t.Fatalf("unit captured as %q, want \"Bytes\" — the publishing guard compares this string, "+
			"so a partial capture would let a per-file counter reach bytes_done", m[2])
	}
}

// quince#808. The tool's own closing count is the ONLY true file count it emits — `file_count` is
// incremented per file and summed across messages, then printed once (idevicebackup2.c:1134/2309/
// 2568). Nothing on the receive path prints per file, so a running count does not exist to parse.
func TestParseReceivedFilesCount(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int64
		ok   bool
	}{
		{"Received 94035 files from device.", 94035, true},
		{"Received 1 file from device.", 1, true}, // singular — a one-file backup must not be skipped
		{"Received 0 files from device.", 0, true},
		// The per-message header this field USED to be counted from. It carries no number, and
		// mistaking it for one is the whole of the defect.
		{"Receiving files", 0, false},
		{"Backup Successful.", 0, false},
	} {
		p := parseLine(tc.line)
		if tc.ok {
			if p.receivedFiles == nil {
				t.Fatalf("%q: no count parsed", tc.line)
			}
			if *p.receivedFiles != tc.want {
				t.Fatalf("%q: got %d want %d", tc.line, *p.receivedFiles, tc.want)
			}
			continue
		}
		if p.receivedFiles != nil {
			t.Fatalf("%q: parsed %d, want no count", tc.line, *p.receivedFiles)
		}
	}
}
