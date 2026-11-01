package capture

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCipherAuthenticatesScopeAndTampering(t *testing.T) {
	c, e := NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if e != nil {
		t.Fatal(e)
	}
	plain := []byte("sensitive prompt")
	sealed, e := c.Seal(plain, "request-a")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("plaintext in ciphertext")
	}
	out, e := c.Open(sealed, "request-a")
	if e != nil || !bytes.Equal(out, plain) {
		t.Fatal("round trip failed", e)
	}
	if _, e = c.Open(sealed, "request-b"); e == nil {
		t.Fatal("scope substitution accepted")
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = c.Open(sealed, "request-a"); e == nil {
		t.Fatal("tampering accepted")
	}
}
