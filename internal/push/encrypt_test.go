package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"testing"
)

// Layout constants restated in the test so the round trip checks the wire
// format independently of encrypt.go.
const (
	testSaltLen       = 16
	testRecordSize    = 4096
	testKeyIDLen      = 65
	testHeaderLen     = testSaltLen + 4 + 1 + testKeyIDLen
	testKeyLen        = 16
	testNonceLen      = 12
	testIKMLen        = 32
	testTagLen        = 16
	testDelimiter     = 0x02
	testAuthSecretLen = 16
)

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

// decryptForTest is the user-agent side of RFC 8291 section 3.4 for one
// aes128gcm record. It returns the plaintext with the delimiter removed.
func decryptForTest(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) != testRecordSize {
		t.Fatalf("body length = %d, want %d", len(body), testRecordSize)
	}
	salt := body[:testSaltLen]
	rs := binary.BigEndian.Uint32(body[testSaltLen : testSaltLen+4])
	if rs != testRecordSize {
		t.Fatalf("rs = %d, want %d", rs, testRecordSize)
	}
	idlen := int(body[testSaltLen+4])
	if idlen != testKeyIDLen {
		t.Fatalf("idlen = %d, want %d", idlen, testKeyIDLen)
	}
	keyID := body[testSaltLen+5 : testHeaderLen]
	record := body[testHeaderLen:]

	asPublic, err := ecdh.P256().NewPublicKey(keyID)
	if err != nil {
		t.Fatalf("keyid is not a P-256 point: %v", err)
	}
	uaPublic := uaPrivate.PublicKey().Bytes()
	secret, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	ikm, err := hkdf.Key(sha256.New, secret, authSecret,
		"WebPush: info\x00"+string(uaPublic)+string(keyID), testIKMLen)
	if err != nil {
		t.Fatalf("ikm: %v", err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", testKeyLen)
	if err != nil {
		t.Fatalf("cek: %v", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", testNonceLen)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	padded, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		t.Fatalf("open record: %v", err)
	}
	if len(padded) == 0 || padded[len(padded)-1] != testDelimiter {
		t.Fatalf("missing last-record delimiter")
	}
	return padded[:len(padded)-1]
}

func TestPushEncrypt(t *testing.T) {
	plaintext := mustDecode(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPrivateBytes := mustDecode(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	asPrivate, err := ecdh.P256().NewPrivateKey(asPrivateBytes)
	if err != nil {
		t.Fatalf("as private key: %v", err)
	}
	uaPublic := mustDecode(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	authSecret := mustDecode(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustDecode(t, "DGv6ra1nlYgDCS1FRnbzlw")
	wantHeader := mustDecode(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vC"+
		"YLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	wantRecord := mustDecode(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ")
	want := append(append([]byte{}, wantHeader...), wantRecord...)

	got, err := Encrypt(plaintext, uaPublic, authSecret, asPrivate, salt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body mismatch\n got: %x\nwant: %x", got, want)
	}
}

func newTestKeys(t *testing.T) (ua, as *ecdh.PrivateKey) {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ua key: %v", err)
	}
	as, err = ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate as key: %v", err)
	}
	return ua, as
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return b
}

func TestEncryptRoundTrip(t *testing.T) {
	uaPrivate, asPrivate := newTestKeys(t)
	authSecret := randomBytes(t, testAuthSecretLen)
	salt := randomBytes(t, testSaltLen)
	plaintext := randomBytes(t, MaxPlaintextBytes)

	body, err := Encrypt(plaintext, uaPrivate.PublicKey().Bytes(), authSecret, asPrivate, salt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(body) != testRecordSize {
		t.Fatalf("body length = %d, want %d", len(body), testRecordSize)
	}
	got := decryptForTest(t, body, uaPrivate, authSecret)
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip plaintext mismatch")
	}
}

func TestEncryptRejects(t *testing.T) {
	uaPrivate, asPrivate := newTestKeys(t)
	uaPublic := uaPrivate.PublicKey().Bytes()
	authSecret := randomBytes(t, testAuthSecretLen)
	salt := randomBytes(t, testSaltLen)

	offCurve := make([]byte, testKeyIDLen)
	offCurve[0] = 0x04
	for i := 1; i < len(offCurve); i++ {
		offCurve[i] = 0x01
	}

	tests := []struct {
		name      string
		plaintext []byte
		uaPublic  []byte
		authSec   []byte
		salt      []byte
		tooLarge  bool
	}{
		{
			name:      "plaintext one byte over the maximum",
			plaintext: randomBytes(t, MaxPlaintextBytes+1),
			uaPublic:  uaPublic,
			authSec:   authSecret,
			salt:      salt,
			tooLarge:  true,
		},
		{
			name:      "salt of 15 bytes",
			plaintext: []byte("x"),
			uaPublic:  uaPublic,
			authSec:   authSecret,
			salt:      randomBytes(t, testSaltLen-1),
		},
		{
			name:      "auth secret of 15 bytes",
			plaintext: []byte("x"),
			uaPublic:  uaPublic,
			authSec:   randomBytes(t, testAuthSecretLen-1),
			salt:      salt,
		},
		{
			name:      "uaPublic not on the P-256 curve",
			plaintext: []byte("x"),
			uaPublic:  offCurve,
			authSec:   authSecret,
			salt:      salt,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := Encrypt(tc.plaintext, tc.uaPublic, tc.authSec, asPrivate, tc.salt)
			if err == nil {
				t.Fatalf("Encrypt returned no error and body of %d bytes", len(body))
			}
			if errors.Is(err, ErrPlaintextTooLarge) != tc.tooLarge {
				t.Fatalf("ErrPlaintextTooLarge match = %v, want %v (err: %v)",
					errors.Is(err, ErrPlaintextTooLarge), tc.tooLarge, err)
			}
		})
	}
}
