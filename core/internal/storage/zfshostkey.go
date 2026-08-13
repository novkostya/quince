package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The host-key half of the zfs transport (quince#912).
//
// WHY IT EXISTS: quince composes `StrictHostKeyChecking=yes`, which is the right choice and is
// argued for in `config/zfsssh.go` — `accept-new` trusts whatever answers on the first connect, and
// the first connect is exactly when an attacker is positioned. But that decision only works if
// something puts an entry in `known_hosts`, and until this file nothing did. `DefaultZFSKnownHosts`
// is INSIDE the container, so the only way to create it was `docker exec`, and the add-storage form
// therefore could not be finished from the UI at all.
//
// `zfsssh.go` asserted the opposite in a comment — that quince#818's form "seeds `known_hosts` from
// a fingerprint the operator confirms" — describing a mechanism nobody had built. That sentence is
// corrected in the same change as this file.
//
// THE CEREMONY IS TWO STEPS, AND THE SPLIT IS THE SECURITY PROPERTY:
//
//	1. ScanHostKey reads what the host offers and returns its FINGERPRINT. It authenticates
//	   nothing, sends no credential, and writes nothing.
//	2. TrustHostKey writes the key THE CALLER PASSES BACK — not a fresh scan.
//
// Step 2 taking the key rather than re-scanning is what makes the operator's confirmation mean
// something. If it re-scanned, an attacker answering between the two calls would have their key
// written after the operator approved a different one, and the ceremony would be theatre.
//
// It is trust-on-first-use WITH A HUMAN CHECK, which is what `StrictHostKeyChecking=yes` was chosen
// to preserve: the operator compares the fingerprint against `ssh-keygen -lf` on the host, which is
// one command they can actually run.

// hostKeyScanTimeout bounds a scan. An unreachable host is the ordinary failing case — it is what
// the operator presses this for — and a form that hangs reads as broken quince rather than as a
// host that is not there.
const hostKeyScanTimeout = 10 * time.Second

// HostKey is what a scan found. NO PRIVATE MATERIAL AND NOTHING SECRET: a host's public key is
// public by construction — every client that connects is handed it.
type HostKey struct {
	// Host and Port as quince will compose them, so the fingerprint the operator confirms belongs
	// to the address quince will actually dial.
	Host string
	Port int
	// Type is the algorithm, e.g. `ssh-ed25519`.
	Type string
	// Fingerprint is the SHA256 form ssh itself prints, so it can be compared character for
	// character against `ssh-keygen -lf /etc/ssh/ssh_host_<type>_key.pub` on the host.
	Fingerprint string
	// Line is the complete `known_hosts` entry. It is what TrustHostKey writes and what the client
	// hands back, so the thing confirmed and the thing stored are one string.
	Line string
}

// errStopAfterHostKey aborts the handshake once the host key is in hand. Reaching authentication
// would mean offering a credential to a host quince has not yet decided to trust.
var errStopAfterHostKey = errors.New("host key captured")

// ScanHostKey asks the host what its key is. It never authenticates and never writes.
func ScanHostKey(ctx context.Context, host string, port int) (HostKey, error) {
	if host == "" {
		return HostKey{}, errors.New("no host to scan")
	}
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	var captured ssh.PublicKey
	cfg := &ssh.ClientConfig{
		// A USER IS REQUIRED BY THE PROTOCOL AND IS NEVER USED: the callback below aborts before
		// any authentication method is offered. Named so it is recognisable in the host's sshd log
		// rather than looking like a probe by something unidentified.
		User: "quince-hostkey-probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			captured = key
			return errStopAfterHostKey
		},
		Timeout: hostKeyScanTimeout,
	}

	d := net.Dialer{Timeout: hostKeyScanTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return HostKey{}, fmt.Errorf("could not reach %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	// The handshake is EXPECTED to fail — errStopAfterHostKey is how it stops. What decides success
	// is whether the callback ran, not what this returns.
	_, _, _, _ = ssh.NewClientConn(conn, addr, cfg) //nolint:dogsled // the error is expected; `captured` is the result
	if captured == nil {
		return HostKey{}, fmt.Errorf("%s answered but offered no host key", addr)
	}

	return HostKey{
		Host:        host,
		Port:        port,
		Type:        captured.Type(),
		Fingerprint: ssh.FingerprintSHA256(captured),
		Line:        knownhosts.Line([]string{knownhosts.Normalize(addr)}, captured),
	}, nil
}

// TrustHostKey records a confirmed host key in quince's own known_hosts.
//
// IT WRITES WHAT IT IS GIVEN. The caller passes back the exact line from the scan the operator
// confirmed — see the ceremony note at the top of this file for why re-scanning here would make the
// confirmation meaningless.
//
// APPEND, NEVER REPLACE. A file that already has an entry for this host and a DIFFERENT key is the
// host-key-changed case, which is either a rebuilt host or an attack, and quince must not resolve
// that on the operator's behalf. It refuses and says which.
func TrustHostKey(knownHostsPath, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return errors.New("no host key line to record")
	}
	// IT MUST PARSE AS A known_hosts LINE. The value arrives over the wire and is written to a file
	// ssh will later read as configuration, so it is validated as the thing it claims to be rather
	// than trusted because quince produced it a moment ago.
	marker, hosts, key, _, _, err := ssh.ParseKnownHosts([]byte(line + "\n"))
	if err != nil {
		return fmt.Errorf("that is not a known_hosts line: %w", err)
	}
	if marker != "" {
		return fmt.Errorf("refusing a %q marker line — quince records plain host keys only", marker)
	}
	if len(hosts) == 0 || key == nil {
		return errors.New("that known_hosts line names no host or carries no key")
	}

	existing, err := os.ReadFile(knownHostsPath) //nolint:gosec // a path from config, not a request
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", knownHostsPath, err)
	}
	if same, conflict := hostKeyState(existing, hosts[0], key); same {
		return nil // already trusted; recording it again would only grow the file
	} else if conflict {
		return fmt.Errorf("%s already has a DIFFERENT key for %s — the host was rebuilt, or "+
			"something is impersonating it. quince will not overwrite it; remove the old line by "+
			"hand once you know which", knownHostsPath, hosts[0])
	}

	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(knownHostsPath), err)
	}
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // as above
	if err != nil {
		return fmt.Errorf("opening %s: %w", knownHostsPath, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("writing %s: %w", knownHostsPath, err)
	}
	return f.Close()
}

// hostKeyState reports whether `host` is already recorded with this exact key (same), or recorded
// with a different one (conflict). Both false means it is not there at all.
func hostKeyState(file []byte, host string, key ssh.PublicKey) (same, conflict bool) {
	rest := file
	want := key.Marshal()
	for len(rest) > 0 {
		_, hosts, k, _, next, err := ssh.ParseKnownHosts(rest)
		if err != nil {
			// A line quince cannot parse is somebody else's business — it may be a hashed entry or
			// a format from a newer ssh. Skipping is right: this function decides whether to APPEND,
			// and an unreadable line is not evidence either way.
			if len(next) == len(rest) {
				break
			}
			rest = next
			continue
		}
		rest = next
		for _, h := range hosts {
			if h != host {
				continue
			}
			if string(k.Marshal()) == string(want) {
				return true, false
			}
			conflict = true
		}
	}
	return false, conflict
}
