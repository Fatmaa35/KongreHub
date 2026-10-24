// Package certsign signs and verifies certificate payloads with Ed25519.
//
// QR kodu {PUBLIC_URL}/verify/{id}?sig={imza} adresini taşır. İmza; sertifika
// kimliği, sahibinin adı, türü ve düzenlenme zamanından oluşan kanonik metin
// üzerinedir, bu yüzden adı ya da türü değiştirilmiş bir sertifika doğrulanmaz.
// Açık anahtar /api/certificates/public-key ucundan yayınlanır; çevrimdışı
// doğrulama da mümkündür.
package certsign

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Payload is the signed content.
type Payload struct {
	ID       string
	UserID   int64
	Kind     string
	Holder   string
	Detail   string
	IssuedAt time.Time
}

// Canonical returns the exact byte string that is signed.
func (p Payload) Canonical() []byte {
	clean := func(s string) string { return strings.ReplaceAll(s, "|", "/") }
	return []byte(fmt.Sprintf("kongrehub-cert|v1|%s|%d|%s|%s|%s|%d",
		clean(p.ID), p.UserID, clean(p.Kind), clean(p.Holder), clean(p.Detail), p.IssuedAt.UTC().Unix()))
}

// Signer holds the key pair.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner builds a signer from a 32-byte hex seed.
func NewSigner(seedHex string) (*Signer, error) {
	seed, err := hex.DecodeString(strings.TrimSpace(seedHex))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("CERT_SIGNING_SEED 64 karakterlik hex (32 bayt) olmalı")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Signer{priv: priv, pub: priv.Public().(ed25519.PublicKey)}, nil
}

// Sign returns a URL-safe base64 signature.
func (s *Signer) Sign(p Payload) string {
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.priv, p.Canonical()))
}

// Verify checks a signature.
func (s *Signer) Verify(p Payload, sig string) bool { return VerifyWith(s.pub, p, sig) }

// PublicKeyHex exposes the public key.
func (s *Signer) PublicKeyHex() string { return hex.EncodeToString(s.pub) }

// VerifyWith verifies using only a public key (offline verification).
func VerifyWith(pub ed25519.PublicKey, p Payload, sig string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, p.Canonical(), raw)
}

// VerifyURL composes the URL encoded in the QR code.
func VerifyURL(base, id, sig string) string {
	return strings.TrimRight(base, "/") + "/verify/" + id + "?sig=" + sig
}
