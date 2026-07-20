package deviceops

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Fake-CLI harness (the muxsup GO_WANT_HELPER_PROCESS discipline): every wrapper points at
// this test binary, re-exec'd via -test.run=TestHelperProcess, so the libimobiledevice CLIs
// are impersonated with the exact strings verified live (qn.3 interface facts). No hardware,
// no real muxer. Synthetic UDIDs only (a real SerialNumber is personal data).

const fakeUDID = "SYNTHETIC-UDID-AAAA-0001"

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeTools returns a Tools whose three CLIs are the test binary, with the given extra child
// env selecting fake behaviour (DEVICEOPS_FAKE=…, DEVICEOPS_* knobs).
func fakeTools(env ...string) *Tools {
	tl := NewTools("/var/run/usbmuxd", "127.0.0.1:27015", discard())
	tl.Idevicepair = os.Args[0]
	tl.Ideviceinfo = os.Args[0]
	tl.Idevicebackup2 = os.Args[0]
	tl.argPrefix = []string{"-test.run=TestHelperProcess", "--"}
	tl.env = append([]string{"GO_WANT_HELPER_PROCESS=1"}, env...)
	return tl
}

// capture is what the fake idevicebackup2 records so a test can assert the password never
// reached argv/env and only arrived over the pty (story 5).
type capture struct {
	Argv     []string `json:"argv"`
	Env      []string `json:"env"`
	Received []string `json:"received"` // secrets typed at the pty prompts, in order
}

// TestHelperProcess is not a real test: when GO_WANT_HELPER_PROCESS=1 it impersonates a
// libimobiledevice CLI (dispatched by argv), then exits.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := helperArgs()
	scenario := os.Getenv("DEVICEOPS_FAKE")
	switch {
	case hasArg(args, "validate"):
		fakeValidate(scenario)
	case hasArg(args, "pair"):
		fakePair(scenario)
	case hasArg(args, "-k"): // ideviceinfo -q com.apple.mobile.backup -k WillEncrypt
		fakeWillEncrypt(scenario)
	case hasArg(args, "-x"): // ideviceinfo -x
		fakeInfo(args)
	case hasArg(args, "encryption"):
		fakeBackup2Encryption(args, scenario)
	case hasArg(args, "changepw"):
		fakeBackup2Changepw(args, scenario)
	default:
		fmt.Fprintln(os.Stderr, "fake: unrecognized argv", args)
		os.Exit(2)
	}
	os.Exit(0)
}

// helperArgs returns the CLI args passed after the "--" separator.
func helperArgs() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func fakeValidate(scenario string) {
	switch scenario {
	case "unpaired":
		fmt.Fprintf(os.Stderr, "ERROR: Device %s is not paired with this host\n", fakeUDID)
		os.Exit(1)
	case "locked":
		fmt.Fprintf(os.Stderr, "ERROR: Could not validate with device %s because a passcode is set. Please enter the passcode on the device and retry.\n", fakeUDID)
		os.Exit(1)
	default:
		fmt.Printf("SUCCESS: Validated pairing with device %s\n", fakeUDID)
		os.Exit(0)
	}
}

func fakePair(scenario string) {
	switch scenario {
	case "denied":
		fmt.Fprintf(os.Stderr, "ERROR: Device %s said that the user denied the trust dialog.\n", fakeUDID)
		os.Exit(1)
	case "notusb":
		fmt.Fprintln(os.Stderr, "ERROR: Pairing is not possible over this connection.")
		os.Exit(1)
	case "trust_then_success":
		// First DEVICEOPS_TRUST_UNTIL attempts return the trust-dialog error; then success.
		n := bumpCounter(os.Getenv("DEVICEOPS_COUNTER"))
		until, _ := strconv.Atoi(os.Getenv("DEVICEOPS_TRUST_UNTIL"))
		if n <= until {
			fmt.Fprintf(os.Stderr, "ERROR: Please accept the trust dialog on the screen of device %s, then attempt to pair again.\n", fakeUDID)
			os.Exit(1)
		}
		fmt.Printf("SUCCESS: Paired with device %s\n", fakeUDID)
		os.Exit(0)
	default:
		fmt.Printf("SUCCESS: Paired with device %s\n", fakeUDID)
		os.Exit(0)
	}
}

func fakeWillEncrypt(scenario string) {
	if scenario == "enc_off" {
		fmt.Println("false")
	} else {
		fmt.Println("true")
	}
	os.Exit(0)
}

// fakeInfo prints an ideviceinfo -x plist. A simple (-s) read omits DeviceName (models the
// limited pre-pairing read); a full read includes it.
func fakeInfo(args []string) {
	name := "<key>DeviceName</key><string>synthetic-iphone</string>"
	if hasArg(args, "-s") {
		name = ""
	}
	fmt.Printf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
%s
<key>ProductType</key><string>iPhone17,2</string>
<key>ProductVersion</key><string>26.0.1</string>
</dict></plist>
`, name)
	os.Exit(0)
}

// fakeBackup2Encryption impersonates `idevicebackup2 -i … encryption on|off`: prompts for the
// password over the pty (twice when enabling — set + confirm), narrates the device confirm,
// records the capture, and exits.
func fakeBackup2Encryption(args []string, scenario string) {
	enabling := hasArg(args, "on")
	in := bufio.NewReader(os.Stdin)
	var received []string
	received = append(received, prompt(in, "Enter backup password: "))
	if enabling {
		received = append(received, prompt(in, "Enter backup password: ")) // confirm
	}
	writeCapture(received)
	if scenario == "enc_fail" {
		fmt.Fprintln(os.Stderr, "ERROR: Could not enable backup encryption.")
		os.Exit(1)
	}
	fmt.Println("Please confirm by entering the passcode on the device.")
	fmt.Println("Backup encryption has been changed successfully.")
	os.Exit(0)
}

// fakeBackup2Changepw impersonates `idevicebackup2 -i … changepw`.
func fakeBackup2Changepw(_ []string, scenario string) {
	in := bufio.NewReader(os.Stdin)
	var received []string
	received = append(received, prompt(in, "Enter old backup password: "))
	received = append(received, prompt(in, "Enter new backup password: "))
	writeCapture(received)
	if scenario == "enc_fail" {
		fmt.Fprintln(os.Stderr, "Could not change backup encryption password.")
		os.Exit(1)
	}
	fmt.Println("Please confirm changing the backup password by entering the passcode on the device.")
	fmt.Println("Backup encryption password has been changed successfully.")
	os.Exit(0)
}

// prompt writes a getpass-style prompt to the pty and reads one line back.
func prompt(in *bufio.Reader, text string) string {
	fmt.Print(text)
	line, _ := in.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

// writeCapture records argv/env/received so a test can assert the password never touched
// argv or env (story 5). No-op when DEVICEOPS_CAPTURE is unset.
func writeCapture(received []string) {
	path := os.Getenv("DEVICEOPS_CAPTURE")
	if path == "" {
		return
	}
	b, _ := json.Marshal(capture{Argv: os.Args, Env: os.Environ(), Received: received})
	_ = os.WriteFile(path, b, 0o600)
}

// bumpCounter increments a small counter file and returns the new value (pair poll loop).
func bumpCounter(path string) int {
	if path == "" {
		return 1
	}
	n := 0
	if b, err := os.ReadFile(path); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	n++
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o600)
	return n
}
