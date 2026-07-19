// Package muxd is quince's client for the usbmuxd/netmuxd wire protocol (stack D2, design
// §2): it maintains a Listen-mode connection to a muxer socket and turns the muxer's
// Attached/Detached messages into resolved presence Events (UDID + transport). One Client
// serves one configured socket; the device registry (a later increment) merges N of them
// into the device table. This file is the wire codec: usbmuxd's length-prefixed header
// wrapping an XML property list.
package muxd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"howett.net/plist"
)

const (
	protocolVersion = 1       // 1 = plist protocol (vs the legacy binary protocol)
	messagePlist    = 8       // the Request field for every plist-protocol message
	maxPayload      = 1 << 20 // 1 MiB guard on a single plist body — muxer messages are tiny
)

// header is the 16-byte little-endian usbmuxd message header. Length counts the header too.
type header struct {
	Length  uint32
	Version uint32
	Request uint32
	Tag     uint32
}

// writePlist frames payload (marshaled as an XML plist) with a usbmuxd header and writes it.
func writePlist(w io.Writer, tag uint32, payload any) error {
	body, err := plist.Marshal(payload, plist.XMLFormat)
	if err != nil {
		return fmt.Errorf("muxd: marshal plist: %w", err)
	}
	h := header{Length: uint32(16 + len(body)), Version: protocolVersion, Request: messagePlist, Tag: tag}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, h)
	buf.Write(body)
	_, err = w.Write(buf.Bytes())
	return err
}

// readPlist reads one framed message and returns its raw plist body + tag. A length outside
// [16, 16+maxPayload] is rejected so a corrupt header can't drive a huge allocation.
func readPlist(r io.Reader) (body []byte, tag uint32, err error) {
	var h header
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		return nil, 0, err
	}
	if h.Length < 16 || h.Length > 16+maxPayload {
		return nil, 0, fmt.Errorf("muxd: implausible message length %d", h.Length)
	}
	body = make([]byte, h.Length-16)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, 0, err
	}
	return body, h.Tag, nil
}

// listenRequest is the plist body of the Listen handshake.
type listenRequest struct {
	MessageType         string `plist:"MessageType"`
	ClientVersionString string `plist:"ClientVersionString"`
	ProgName            string `plist:"ProgName"`
	LibUSBMuxVersion    uint32 `plist:"kLibUSBMuxVersion"`
}

// reply is the superset of the Result / Attached / Detached message fields; plist decoding
// leaves absent keys at their zero value, so one struct decodes all three.
type reply struct {
	MessageType string     `plist:"MessageType"`
	Number      int        `plist:"Number"`   // Result: 0 = OK
	DeviceID    int        `plist:"DeviceID"` // Attached/Detached: per-connection id
	Properties  properties `plist:"Properties"`
}

// properties is the Attached message's device info the muxer knows (SerialNumber IS the
// device UDID). Friendly name/model/iOS come from lockdown (ideviceinfo) — qn.3.
type properties struct {
	SerialNumber   string `plist:"SerialNumber"`
	ConnectionType string `plist:"ConnectionType"` // "USB" | "Network"
	ProductID      int    `plist:"ProductID"`
}
