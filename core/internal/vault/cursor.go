package vault

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// The cursor is the last (Domain, RelativePath) a page returned, encoded opaquely.
//
// WHY A POSITION AND NOT AN ITERATOR HANDLE. The decryption library walks a manifest as a
// lazy sequence ordered by domain then relative path — a stable TOTAL order — with no
// cursor of its own. A handle would mean parking that walk between HTTP requests: a
// goroutine, an open statement and a share of the index held for as long as a person
// leaves a browser tab alone. A position means the next page is a fresh query that seeks,
// so an idle session holds nothing at all, which is what design §7's "the vault process
// dies at session lock — RSS returns to zero between sessions" wants from the in-process
// case too.
//
// IT IS OPAQUE ON PURPOSE. Nothing outside this package may construct or parse one: the
// encoding is this implementation's, and a client that learns to build cursors has
// promoted an internal ordering into a contract. Decoding a cursor from another version is
// meaningless rather than dangerous — the session id already decides which version a
// request reads.
type cursor struct {
	Domain string `json:"d"`
	Path   string `json:"p"`
}

func encodeCursor(c cursor) string {
	b, err := json.Marshal(c)
	if err != nil {
		// Two strings into a two-field struct cannot fail to marshal. Panicking on the
		// impossible branch beats returning an empty cursor, which reads as "last page".
		panic("vault: encoding a cursor failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor parses a cursor. An empty string is the start of the sequence, not an
// error — that is what a first request sends.
func decodeCursor(s string) (cursor, error) {
	if s == "" {
		return cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, fmt.Errorf("vault: unreadable cursor: %w", err)
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return cursor{}, fmt.Errorf("vault: unreadable cursor: %w", err)
	}
	return c, nil
}
