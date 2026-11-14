package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Keys are derived from one master secret, one per purpose, so a key leaked from one use cannot be
// replayed in another.
type Keys struct {
	CodeMAC     []byte
	OAuthCookie []byte
	BindingMAC  []byte
}

func DeriveKeys(master []byte) (Keys, error) {
	if len(master) < 32 {
		return Keys{}, errors.New("the auth key must be at least 32 bytes")
	}
	derive := func(info string) ([]byte, error) { return hkdf.Key(sha256.New, master, nil, info, 32) }
	var k Keys
	var err error
	if k.CodeMAC, err = derive("terka code"); err != nil {
		return Keys{}, err
	}
	if k.OAuthCookie, err = derive("terka oauth cookie"); err != nil {
		return Keys{}, err
	}
	if k.BindingMAC, err = derive("terka binding"); err != nil {
		return Keys{}, err
	}
	return k, nil
}

// NewCode draws a six-digit code. Values past the largest multiple of a million are redrawn, so every
// code is equally likely.
func NewCode() (string, error) {
	const space = 1_000_000
	const limit = (1 << 32) / space * space
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		if v := binary.BigEndian.Uint32(b[:]); v < limit {
			return fmt.Sprintf("%06d", v%space), nil
		}
	}
}

// CodeMAC is what the database keeps instead of the code. The separator keeps address and code from
// running into each other.
func CodeMAC(k Keys, email, code string) []byte {
	m := hmac.New(sha256.New, k.CodeMAC)
	m.Write([]byte(email))
	m.Write([]byte{0})
	m.Write([]byte(code))
	return m.Sum(nil)
}

// NewToken returns a secret for a cookie and the hash the database keeps of it.
func NewToken() (token string, hash []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashToken(token), nil
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

var errSealed = errors.New("sealed value is invalid or expired")

// Seal encrypts and authenticates plaintext with an expiry inside, for values the browser carries but
// must neither read nor change.
func Seal(key, plaintext []byte, now time.Time, ttl time.Duration) (string, error) {
	aead, err := gcm(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	body := binary.BigEndian.AppendUint64(nil, uint64(now.Add(ttl).Unix()))
	body = append(body, plaintext...)
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, body, nil)), nil
}

func Open(key []byte, sealed string, now time.Time) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead()+8 {
		return nil, errSealed
	}
	body, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return nil, errSealed
	}
	if now.Unix() > int64(binary.BigEndian.Uint64(body[:8])) {
		return nil, errSealed
	}
	return body[8:], nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
