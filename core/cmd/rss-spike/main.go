// Command rss-spike measures the peak resident memory of reading an encrypted iOS backup,
// so that qn.8's process-model decision is taken against a number instead of an argument.
//
// IT DRIVES THE LIBRARY, NOT QUINCE'S VAULT, and that is the point rather than a shortcut
// (qn.8 spec D10.1). Peak RSS is held by keybag derivation, the Manifest.db decrypt and the
// file stream — all `ios-backup-crypt`'s. Measuring the library directly measures the thing
// that holds the memory, it can be written before any implementation of `vault.Vault`
// exists, and it cannot become work anybody is reluctant to throw away. The Operator's
// ruling asked for a measurement rather than an implementation to bless; a harness with no
// production code in it is what keeps that free.
//
// THREE PROCESSES PER ROW, AND THE SPLIT IS THE WHOLE INSTRUMENT.
//
//  1. Peak RSS is a high-water mark for the life of a process, so several phases in one
//     process would each report the largest of them.
//  2. BUILDING THE FIXTURE COSTS MORE MEMORY THAN READING IT, and measuring both together
//     reports the builder. `fixture` reads the whole assembled Manifest.db into memory and
//     holds plaintext and ciphertext at once, and the caller holds a slice of every row it
//     asked for — all of which scales with row count. A first version of this harness built
//     in-process and produced unlock figures rising 13.6 → 23.6 → 62.8 MiB across 1k → 20k
//     rows, which reads as "the library is O(rows)" and was very largely this harness
//     measuring itself. **A rising curve is exactly what D10.3 clause (b) fails on**, so
//     that artifact would have argued for a sidecar on the strength of the instrument.
//
// So: one process builds the fixture and exits, and a second opens what is on disk and is
// the only one measured. Resetting the kernel counter mid-process
// (`/proc/self/clear_refs`) was the alternative and is worse — it makes the measurement
// depend on a procfs write, where a fresh process is unarguable.
//
// WHAT IT DOES NOT MEASURE, stated because the threshold has a third clause. D10.3's clause
// (c) is about memory RETENTION after `lock` — RSS returning near its pre-unlock baseline.
// A process that exits has no post-lock RSS, so this harness is structurally blind to it
// (spec D10.3b). Clause (c) is measured in-process, across a lock, on the implementation
// slice, by G7.
//
// Usage:
//
//	rss-spike                                     # every phase at every size, as a table
//	rss-spike -build -files 20000 -out DIR        # build a fixture, print its credentials
//	rss-spike -phase unlock -backup DIR -password P
package main

import (
	"bufio"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	iosbackup "github.com/novkostya/ios-backup-crypt"
	"github.com/novkostya/ios-backup-crypt/fixture"
)

// manifestSizes are row counts for the unlock and walk curves. The point is the SHAPE of
// the curve, not the largest value: a flat line across an order of magnitude says the
// streaming design holds, and a rising one says it does not, whichever absolute figure
// today's hardware produces (spec D10.3 clause (b)).
var manifestSizes = []int{1_000, 5_000, 20_000, 50_000}

// streamSizes are single-file byte counts for the stream curve. A backup's largest member
// is typically a video; these bracket that without needing a real one.
var streamSizes = []int{1 << 20, 16 << 20, 128 << 20} // 1 MiB, 16 MiB, 128 MiB

func main() {
	build := flag.Bool("build", false, "build a fixture into -out and print its credentials")
	phase := flag.String("phase", "", "one of: unlock, walk, stream")
	files := flag.Int("files", 1000, "manifest row count (build mode)")
	fileSize := flag.Int("filesize", 0, "single large-file size in bytes (build mode); 0 means none")
	out := flag.String("out", "", "where to build the fixture (build mode)")
	backup := flag.String("backup", "", "an already-built fixture to measure")
	password := flag.String("password", "", "the fixture's password")
	fileID := flag.String("fileid", "", "the file to stream (stream phase)")
	pageSize := flag.Int("page", 500, "walk page size; matches the vault seam's default")
	flag.Parse()

	switch {
	case *build:
		if err := buildFixture(*out, *files, *fileSize); err != nil {
			fail(err)
		}
	case *phase != "":
		row, err := measure(*phase, *backup, *password, *fileID, *pageSize)
		if err != nil {
			fail(err)
		}
		fmt.Println(row)
	default:
		if err := runAll(); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rss-spike:", err)
	os.Exit(1)
}

// buildFixture writes a synthetic backup and prints "password\tfileID" — the fileID being
// the large file when one was asked for. Nothing about THIS process is measured.
func buildFixture(dir string, files, fileSize int) error {
	if dir == "" {
		return fmt.Errorf("-build needs -out")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var spec fixture.Spec
	if fileSize > 0 {
		blob := make([]byte, fileSize)
		if _, err := rand.Read(blob); err != nil {
			return err
		}
		spec = fixture.Spec{Files: []fixture.File{
			{Domain: "CameraRollDomain", RelativePath: "Media/DCIM/big.mov", Flags: 1, Data: blob},
		}}
	} else {
		spec = manifestSpec(files)
	}

	res, err := fixture.Build(dir, spec)
	if err != nil {
		return fmt.Errorf("building the fixture: %w", err)
	}
	last := ""
	if len(res.Files) > 0 {
		last = res.Files[len(res.Files)-1].FileID
	}
	fmt.Printf("%s\t%s\n", res.Password, last)
	return nil
}

// measure opens an already-built fixture and runs one phase. This process holds nothing but
// what the library allocates, which is the figure the threshold is about.
func measure(phase, backupDir, password, fileID string, pageSize int) (string, error) {
	if backupDir == "" || password == "" {
		return "", fmt.Errorf("-phase needs -backup and -password")
	}

	start := time.Now()

	b, err := iosbackup.Open(backupDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = b.Close() }()
	if err := b.Unlock(password); err != nil {
		return "", err
	}

	var detail string
	switch phase {
	case "unlock":
		info, err := b.DeviceInfo()
		if err != nil {
			return "", err
		}
		detail = fmt.Sprintf("file_count=%d", info.FileCount)

	case "walk":
		n, pages := 0, 1
		for range b.List("", "") {
			n++
			if n%pageSize == 0 {
				pages++
			}
		}
		if err := b.Err(); err != nil {
			return "", err
		}
		detail = fmt.Sprintf("entries=%d pages=%d", n, pages)

	case "stream":
		n, err := io.Copy(io.Discard, readerFor(b, fileID))
		if err != nil {
			return "", err
		}
		detail = fmt.Sprintf("streamed=%s", humanBytes(n))

	default:
		return "", fmt.Errorf("unknown phase %q", phase)
	}

	elapsed := time.Since(start)
	peak, err := peakRSS()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\t%d\t%d\t%s", phase, peak, elapsed.Milliseconds(), detail), nil
}

// runAll orchestrates: build in one child, measure in another, one row at a time.
func runAll() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating this binary: %w", err)
	}

	baseline, err := peakRSS()
	if err != nil {
		return err
	}

	fmt.Println("# qn.8 spike — peak RSS of reading an encrypted iOS backup (spec D10.2)")
	fmt.Println("# the fixture is built in a SEPARATE process; only the reader is measured")
	fmt.Printf("# this orchestrator's own peak RSS, for scale: %s\n", humanBytes(baseline))
	fmt.Printf("# %-8s %-12s %-12s %-9s %s\n", "phase", "input", "peak_rss", "elapsed", "detail")

	type job struct {
		phase string
		files int
		size  int
	}
	var jobs []job
	for _, n := range manifestSizes {
		jobs = append(jobs, job{"unlock", n, 0}, job{"walk", n, 0})
	}
	for _, s := range streamSizes {
		jobs = append(jobs, job{"stream", 0, s})
	}

	for _, j := range jobs {
		if err := runOne(self, j.phase, j.files, j.size); err != nil {
			return fmt.Errorf("%s: %w", j.phase, err)
		}
	}
	return nil
}

func runOne(self, phase string, files, size int) error {
	dir, err := os.MkdirTemp("", "rss-spike-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	backupDir := filepath.Join(dir, "backup")

	buildArgs := []string{"-build", "-out", backupDir}
	if size > 0 {
		buildArgs = append(buildArgs, "-filesize", strconv.Itoa(size))
	} else {
		buildArgs = append(buildArgs, "-files", strconv.Itoa(files))
	}
	creds, err := exec.Command(self, buildArgs...).Output()
	if err != nil {
		return fmt.Errorf("build child: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(creds)), "\t")
	if len(parts) != 2 {
		return fmt.Errorf("build child printed %q", creds)
	}

	measureArgs := []string{"-phase", phase, "-backup", backupDir, "-password", parts[0]}
	if phase == "stream" {
		measureArgs = append(measureArgs, "-fileid", parts[1])
	}
	row, err := exec.Command(self, measureArgs...).Output()
	if err != nil {
		return fmt.Errorf("measure child: %w", err)
	}

	f := strings.Split(strings.TrimSpace(string(row)), "\t")
	if len(f) != 4 {
		return fmt.Errorf("measure child printed %q", row)
	}
	peak, _ := strconv.ParseInt(f[1], 10, 64)
	ms, _ := strconv.Atoi(f[2])
	input := files
	if phase == "stream" {
		input = size
	}
	fmt.Printf("  %-8s %-12s %-12s %-9s %s\n",
		f[0], humanInput(phase, input), humanBytes(peak),
		(time.Duration(ms) * time.Millisecond).String(), f[3])
	return nil
}

// readerFor adapts DecryptFile's Writer sink to a Reader, which is the same shape the
// in-process vault implementation will use — so the stream row measures the pipe too,
// rather than a form nothing will ship.
func readerFor(b *iosbackup.Backup, fileID string) io.Reader {
	pr, pw := io.Pipe()
	go func() { _ = pw.CloseWithError(b.DecryptFile(fileID, pw)) }()
	return pr
}

func manifestSpec(n int) fixture.Spec {
	files := make([]fixture.File, 0, n)
	for i := range n {
		// Tiny bodies: these curves are about the MANIFEST, and per-file content would make
		// the fixture build dominate both the runtime and the disk.
		files = append(files, fixture.File{
			Domain:       fmt.Sprintf("Domain%02d", i%16),
			RelativePath: fmt.Sprintf("Library/Generated/%06d/file.dat", i),
			Flags:        1,
			Data:         []byte{byte(i), byte(i >> 8)},
		})
	}
	return fixture.Spec{Files: files}
}

// peakRSS reads VmHWM — the kernel's own high-water mark for this process's resident set.
// Not runtime.MemStats: the threshold in D10.3 is about memory the OS sees held, and Go's
// heap accounting says nothing about pages the runtime has not returned.
func peakRSS() (int64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("peak RSS is Linux-only (no /proc/self/status): %w", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unparseable VmHWM line %q", line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("unparseable VmHWM value %q: %w", fields[1], err)
		}
		return kb * 1024, nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("no VmHWM in /proc/self/status")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

func humanInput(phase string, n int) string {
	if phase == "stream" {
		return humanBytes(int64(n))
	}
	return fmt.Sprintf("%d rows", n)
}
