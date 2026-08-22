package vault

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
	sqlite "modernc.org/sqlite"
)

// G5 — D4's claim is about CONTROL FLOW, so the gate counts rather than times.
//
// A wall-clock budget stood in the spec here and was withdrawn before this was written
// (quince#1448): a change that reintroduced pagination but landed inside the budget would
// have been waved through by a timer and caught by a pass count. The clock is the weaker
// gate as well as the flakier one.
//
// WHAT THIS CATCHES: Aggregate reimplemented as a loop over List. That issues one query per
// page — ceil(n/limit) of them — where one pass issues exactly one. On this fixture a
// paginated version would issue at least 2, and the assertion is `== 1`, so it fails on the
// first page boundary rather than needing a large fixture to notice.

// countingDriver wraps modernc's SQLite driver and counts queries. It is the only way to
// see the shape of a read from outside: the implementation holds a *sql.DB and exposes no
// hook, and a Vault wrapper cannot help because Aggregate's internal calls never cross it.
type countingDriver struct {
	mu sync.Mutex
	n  int
}

func (d *countingDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

func (d *countingDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n = 0
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	c, err := (&sqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, d: d}, nil
}

type countingConn struct {
	driver.Conn
	d *countingDriver
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.d.mu.Lock()
	c.d.n++
	c.d.mu.Unlock()
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("underlying conn is not a QueryerContext")
	}
	return q.QueryContext(ctx, query, args)
}

var (
	counter      = &countingDriver{}
	registerOnce sync.Once
)

func TestAggregateIsOnePass(t *testing.T) {
	registerOnce.Do(func() { sql.Register("sqlite-counting", counter) })

	dir := t.TempDir()
	// Enough rows that a paginated implementation must issue more than one query at any
	// sane page size, and small enough to build in well under a second.
	files := make([]fixture.File, 0, 600)
	for i := 0; i < 600; i++ {
		files = append(files, fixture.File{
			Domain:       fmt.Sprintf("AppDomain-com.example.app%d", i%7),
			RelativePath: fmt.Sprintf("Library/f%04d.dat", i),
			Flags:        1,
			Data:         []byte("x"),
		})
	}
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: files}); err != nil {
		t.Fatal(err)
	}

	prev := sqliteDriverName
	sqliteDriverName = "sqlite-counting"
	defer func() { sqliteDriverName = prev }()

	v, err := OpenUnencrypted(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	ctx := context.Background()
	if _, err := v.Unlock(ctx, ""); err != nil {
		t.Fatal(err)
	}

	counter.reset()
	got, err := v.Aggregate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n := counter.count(); n != 1 {
		t.Errorf("Aggregate issued %d queries, want exactly 1 — an aggregate that pages "+
			"re-pays a per-page cost for every page (qn.9 D4)", n)
	}
	if got.TotalFiles != 600 {
		t.Errorf("TotalFiles = %d, want 600", got.TotalFiles)
	}

	// CONTROL: the paginated walk this exists to forbid really does issue many queries, so
	// the assertion above is discriminating rather than passing against an instrument that
	// counts nothing.
	counter.reset()
	cursor := ""
	for {
		p, err := v.List(ctx, Query{Cursor: cursor, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if n := counter.count(); n < 2 {
		t.Fatalf("the paginated walk issued %d queries — the counter is not working, so the "+
			"one-pass assertion above proves nothing", n)
	} else {
		t.Logf("control: paginated walk at limit=100 issued %d queries against Aggregate's 1", n)
	}
}
