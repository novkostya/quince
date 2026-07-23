package backup

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite" // real SQLite Manifest.db for the unencrypted verify branch
)

// The fake idevicebackup2: a re-exec of the test binary (the qn.2b/qn.3 GO_WANT_HELPER_PROCESS
// discipline) that replays one committed transcript on stdout with its timing, writes the matching
// MobileBackup2 tree into <target>/<UDID>/ (which, qn.5b, IS the storage backend's working/<udid>
// because the engine hands idevicebackup2 the working/ parent directly — no symlink adapter — so
// storage.Verify runs against real structure), and exits with the transcript's code —
// or hangs (frozen transport) until the engine's liveness timeout kills it. All params travel in
// argv (no env), so parallel tests stay isolated.

type fakeParams struct {
	TranscriptPath string
	Tree           string // complete | torn | none
	Encrypted      bool
	Kind           string // full | incremental
	ExitCode       int
	Hang           bool
	StallAfter     int // 1-based line index after which to stall (0 = none)
	StallMs        int
	StallChurn     bool // churn the tree during the stall (silent_but_connected, survives)
	LineDelayMs    int
}

// fakeToolEnv builds the ToolConfig.Env that selects the fake behaviour (the deviceops
// GO_WANT_HELPER_PROCESS discipline): the guard + the JSON-encoded replay params.
func fakeToolEnv(p fakeParams) []string {
	b, _ := json.Marshal(p)
	return []string{"GO_WANT_HELPER_PROCESS=1", "FAKE_PARAMS=" + string(b)}
}

// fakeArgPrefix is the ToolConfig.ArgPrefix that re-execs this test binary as the fake.
func fakeArgPrefix() []string { return []string{"-test.run=TestFakeIdevicebackup2", "--"} }

// TestFakeIdevicebackup2 is NOT a real test — it is the fake's entry point. On a normal `go test`
// run (no GO_WANT_HELPER_PROCESS) it returns immediately; when the engine re-execs it, it replays.
func TestFakeIdevicebackup2(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	var p fakeParams
	if err := json.Unmarshal([]byte(os.Getenv("FAKE_PARAMS")), &p); err != nil {
		fmt.Fprintln(os.Stderr, "fake: bad FAKE_PARAMS:", err)
		syscall.Exit(2)
	}
	rest := helperArgsAfter("--")
	udid := flagVal(rest, "-u")
	target := posAfter(rest, "backup")
	// syscall.Exit, NOT os.Exit: this fake is a -race-instrumented test-binary re-exec, and
	// os.Exit runs the Go runtime exit hooks (incl. the race finalizer), which can deadlock in a
	// helper process that has done file I/O. syscall.Exit calls exit_group directly; stdout is an
	// unbuffered pipe, so no replayed output is lost.
	syscall.Exit(runFake(p, udid, target))
}

func helperArgsAfter(sep string) []string {
	for i, a := range os.Args {
		if a == sep {
			return os.Args[i+1:]
		}
	}
	return nil
}

func runFake(p fakeParams, udid, target string) int {
	treeDir := filepath.Join(target, udid) // qn.5b: == the storage working/<udid> tree (no symlink)
	_ = os.MkdirAll(treeDir, 0o755)

	lines := readLinesOrEmpty(p.TranscriptPath)
	for i, line := range lines {
		fmt.Println(line)
		writeMarker(treeDir, i) // churn the tree as output flows (keeps the sampler "active")
		sleepMs(p.LineDelayMs)
		if p.StallAfter == i+1 {
			doStall(p, treeDir)
		}
	}
	if p.Hang {
		select {} // frozen transport: no output, no churn → the engine's timeout kills the group
	}
	writeTree(treeDir, p)
	return p.ExitCode
}

func doStall(p fakeParams, treeDir string) {
	if !p.StallChurn {
		sleepMs(p.StallMs) // silent, no churn (e.g. the passcode wait — engine pauses the clock)
		return
	}
	end := time.Now().Add(time.Duration(p.StallMs) * time.Millisecond)
	for i := 0; time.Now().Before(end); i++ {
		writeMarker(treeDir, 100000+i) // churn: device is preparing, tree still changing → alive
		sleepMs(20)
	}
}

// writeTree lays down a MobileBackup2 tree matching the fixture. "complete" passes storage.Verify
// (encrypted variant: non-trivial non-SQLite-magic Manifest.db + a populated blob shard on a full
// backup); "torn" writes an unfinished Status.plist so Verify fails; "none" writes nothing.
func writeTree(dir string, p fakeParams) {
	if p.Tree == "none" {
		return
	}
	snapshotState := "finished"
	if p.Tree == "torn" {
		snapshotState = "new"
	}
	isFull := "false"
	if p.Kind == "full" {
		isFull = "true"
	}
	writeFile(filepath.Join(dir, "Status.plist"), statusPlist(snapshotState, isFull))
	writeFile(filepath.Join(dir, "Info.plist"), infoPlist())
	writeFile(filepath.Join(dir, "Manifest.plist"), manifestPlist(p.Encrypted))
	if p.Tree == "torn" {
		return // unfinished snapshot → Verify fails before the DB checks
	}
	if p.Encrypted {
		// Encrypted-flavoured Manifest.db: > minManifestSize, and NOT plaintext SQLite magic.
		writeFile(filepath.Join(dir, "Manifest.db"), []byte(strings.Repeat("ENCRYPTED-MANIFEST-", 16)))
		// A populated two-hex-char blob shard (verify requires one on a full encrypted backup).
		_ = os.MkdirAll(filepath.Join(dir, "ab"), 0o755)
		writeFile(filepath.Join(dir, "ab", "ab00112233445566778899aabbccddeeff001122"), []byte("blob-bytes"))
		return
	}
	// Unencrypted: a real SQLite Manifest.db with the tables + a sampled Files row → blob that the
	// plaintext-verify branch opens and checks.
	createSQLiteManifest(filepath.Join(dir, "Manifest.db"), dir)
}

// createSQLiteManifest writes a minimal but real SQLite Manifest.db (the tables + one Files row)
// and the blob it references, so storage.Verify's unencrypted branch passes.
func createSQLiteManifest(dbPath, treeDir string) {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE Files (fileID TEXT PRIMARY KEY, domain TEXT, relativePath TEXT)`); err != nil {
		return
	}
	if _, err := db.Exec(`CREATE TABLE Properties (key TEXT, value TEXT)`); err != nil {
		return
	}
	fileID := "ab00112233445566778899aabbccddeeff001122"
	if _, err := db.Exec(`INSERT INTO Files (fileID, domain, relativePath) VALUES (?, 'TestDomain', 'x')`, fileID); err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Join(treeDir, fileID[:2]), 0o755)
	writeFile(filepath.Join(treeDir, fileID[:2], fileID), []byte("blob-bytes"))
}

func statusPlist(snapshotState, isFull string) []byte {
	return []byte(plistDoc(fmt.Sprintf(
		"<key>SnapshotState</key><string>%s</string><key>IsFullBackup</key><%s/>",
		snapshotState, boolTag(isFull))))
}

func manifestPlist(encrypted bool) []byte {
	return []byte(plistDoc(fmt.Sprintf("<key>IsEncrypted</key><%s/>", boolTag(fmt.Sprint(encrypted)))))
}

func infoPlist() []byte {
	return []byte(plistDoc("<key>Device Name</key><string>test-iphone</string>" +
		"<key>Product Version</key><string>26.5</string>"))
}

func plistDoc(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` +
		`<plist version="1.0"><dict>` + body + `</dict></plist>`
}

func boolTag(v string) string {
	if v == "true" {
		return "true"
	}
	return "false"
}

// --- small argv/io helpers ---

func flagVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func posAfter(args []string, word string) string {
	for i, a := range args {
		if a == word && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func readLinesOrEmpty(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func writeMarker(dir string, i int) {
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf(".part-%d", i)), []byte("x"), 0o644)
}

func writeFile(path string, b []byte) {
	_ = os.WriteFile(path, b, 0o644)
}

func sleepMs(ms int) {
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// transcriptPath resolves a fixture's absolute path so the re-exec'd fake (same cwd) can read it.
func transcriptPath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "transcripts", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
