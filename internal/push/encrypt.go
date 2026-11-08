package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// RFC 8188 aes128gcm framing with one record of recordSize bytes, keyed per RFC 8291.
const (
	saltLen       = 16
	recordSize    = 4096
	keyIDLen      = 65
	headerLen     = saltLen + 4 + 1 + keyIDLen
	cekLen        = 16
	nonceLen      = 12
	ikmLen        = 32
	gcmTagLen     = 16
	delimiter     = 0x02
	authSecretLen = 16
)

// MaxPlaintextBytes is the largest plaintext whose encrypted body fits in one record.
const MaxPlaintextBytes = recordSize - headerLen - 1 - gcmTagLen

// ErrPlaintextTooLarge is returned by Encrypt when the plaintext exceeds MaxPlaintextBytes.
var ErrPlaintextTooLarge = errors.New("push: plaintext too large for one record")

// Encrypt returns the aes128gcm body for plaintext addressed to the user agent's
// P-256 public key uaPublic (uncompressed form), using the application server's
// private key asPrivate and the RFC 8291 auth secret and RFC 8188 salt.
// The body is the 86-byte header followed by the single encrypted record.
func Encrypt(plaintext, uaPublic, authSecret []byte, asPrivate *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > MaxPlaintextBytes {
		return nil, ErrPlaintextTooLarge
	}
	if len(salt) != saltLen {
		return nil, fmt.Errorf("push: salt is %d bytes, want %d", len(salt), saltLen)
	}
	if len(authSecret) != authSecretLen {
		return nil, fmt.Errorf("push: auth secret is %d bytes, want %d", len(authSecret), authSecretLen)
	}
	if asPrivate == nil {
		return nil, errors.New("push: nil application server key")
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("push: invalid user agent public key: %w", err)
	}
	secret, err := asPrivate.ECDH(uaKey)
	if err != nil {
		return nil, fmt.Errorf("push: ecdh: %w", err)
	}
	asPublic := asPrivate.PublicKey().Bytes()

	ikm, err := hkdf.Key(sha256.New, secret, authSecret,
		"WebPush: info\x00"+string(uaKey.Bytes())+string(asPublic), ikmLen)
	if err != nil {
		return nil, fmt.Errorf("push: derive ikm: %w", err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", cekLen)
	if err != nil {
		return nil, fmt.Errorf("push: derive cek: %w", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", nonceLen)
	if err != nil {
		return nil, fmt.Errorf("push: derive nonce: %w", err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("push: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("push: gcm: %w", err)
	}

	padded := make([]byte, 0, len(plaintext)+1)
	padded = append(padded, plaintext...)
	padded = append(padded, delimiter)

	body := make([]byte, headerLen, headerLen+len(padded)+gcmTagLen)
	copy(body, salt)
	binary.BigEndian.PutUint32(body[saltLen:], recordSize)
	body[saltLen+4] = keyIDLen
	copy(body[saltLen+5:], asPublic)
	return gcm.Seal(body, nonce, padded, nil), nil
}
