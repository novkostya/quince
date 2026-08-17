package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"encoding/binary"
	"errors"
	"testing"
)

// THE RECEIVER SIDE, written as a user agent would — it parses the aes128gcm header off the wire and
// derives its own keys, rather than being handed anything Encrypt computed.
//
// WHY IT EXISTS BESIDE THE RFC VECTOR. The vector proves quince agrees with the standard for ONE
// fixed input, including a fixed ephemeral key and salt. The production path generates both, and no
// fixture can cover that. This closes the gap: encrypt with fresh randomness, then decrypt as the
// phone would. If Encrypt ever starts writing a header field the receiver cannot parse — a wrong
// record size, a mis-sized keyid — this fails where the vector test would still pass.
func decryptAsUserAgent(t *testing.T, uaPriv *ecdh.PrivateKey, authSecret, body []byte) ([]byte, error) {
	t.Helper()
	// RFC 8188 §2.1: salt(16) || rs(4) || idlen(1) || keyid(idlen) || ciphertext
	if len(body) < 21 {
		return nil, errors.New("body is shorter than an aes128gcm header")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if len(body) < 21+idlen {
		return nil, errors.New("keyid runs past the end of the body")
	}
	asPublicRaw := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]

	// `rs` is the record-size PARAMETER, so the final record may be shorter — but never longer, and a
	// receiver that framed on a smaller rs than the sender used would cut the record in half.
	// Asserted from the receiver's side because Encrypt is the only thing that writes this field and
	// the RFC vector is the only other thing that would notice it drifting.
	if len(ciphertext) > int(rs) {
		return nil, errors.New("the record is longer than the advertised record size")
	}

	asPublic, err := ecdh.P256().NewPublicKey(asPublicRaw)
	if err != nil {
		return nil, err
	}
	shared, err := uaPriv.ECDH(asPublic)
	if err != nil {
		return nil, err
	}
	// Note the argument order: the receiver's own public key first, then the sender's. `key_info` is
	// asymmetric, and swapping these is the mistake that makes two correct implementations disagree.
	_, cek, nonce, err := derive(shared, uaPriv.PublicKey().Bytes(), asPublicRaw, authSecret, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	if len(record) == 0 {
		return nil, errors.New("record has no padding delimiter")
	}
	// `0x02` is the last-record delimiter. `0x01` would mean another record follows, and a user agent
	// that got one from quince would wait forever for a record quince never sends.
	if record[len(record)-1] != 0x02 {
		return nil, errors.New("record is not marked as the last one")
	}
	return record[:len(record)-1], nil
}

// deriveForTest exposes the production key schedule to the vector test. It is a thin pass-through
// ON PURPOSE: a test that re-implemented the derivation would be comparing the RFC against a second
// copy of the code rather than against the code that ships.
func deriveForTest(t *testing.T, shared, uaPublic, asPublic, authSecret, salt []byte) (ikm, cek, nonce []byte) {
	t.Helper()
	ikm, cek, nonce, err := derive(shared, uaPublic, asPublic, authSecret, salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return ikm, cek, nonce
}
