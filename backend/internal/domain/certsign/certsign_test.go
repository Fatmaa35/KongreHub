package certsign

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

const seed = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestSignVerifyAndTamper(t *testing.T) {
	s, err := NewSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	p := Payload{ID: "a1b2", UserID: 7, Kind: "attendance", Holder: "Zeynep Kaya", IssuedAt: time.Unix(1800000000, 0)}
	sig := s.Sign(p)
	if !s.Verify(p, sig) {
		t.Fatal("valid signature rejected")
	}
	tampered := p
	tampered.Holder = "Mallory"
	if s.Verify(tampered, sig) {
		t.Fatal("tampered holder accepted")
	}
	tampered = p
	tampered.Kind = "presenter"
	if s.Verify(tampered, sig) {
		t.Fatal("tampered kind accepted")
	}
	mid := len(sig) / 2
	flip := "A"
	if sig[mid] == 'A' {
		flip = "B"
	}
	if s.Verify(p, sig[:mid]+flip+sig[mid+1:]) || s.Verify(p, "not-base64!!") {
		t.Fatal("bad signature accepted")
	}
	pub, _ := hex.DecodeString(s.PublicKeyHex())
	if !VerifyWith(ed25519.PublicKey(pub), p, sig) {
		t.Fatal("offline verification failed")
	}
}

func TestSignerSeedValidation(t *testing.T) {
	for _, bad := range []string{"", "zz", strings.Repeat("ab", 31)} {
		if _, err := NewSigner(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCanonicalEscapesSeparator(t *testing.T) {
	a := Payload{ID: "x", Holder: "A|B"}.Canonical()
	if strings.Count(string(a), "|") != 7 {
		t.Fatalf("separator injected: %s", a)
	}
	if got := VerifyURL("http://localhost:3000/", "id1", "sig"); got != "http://localhost:3000/verify/id1?sig=sig" {
		t.Fatal(got)
	}
}
