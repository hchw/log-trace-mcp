package crypto

import "testing"

func TestCipherRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"ak_id":"abc","ak_secret":"secret"}`)
	aad := []byte("prod-sls")
	blob, err := c.Encrypt(plain, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decrypt(blob, aad)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round trip mismatch: %s", got)
	}
}

func TestCipherTamperFails(t *testing.T) {
	key := make([]byte, 32)
	c, _ := New(key)
	blob, _ := c.Encrypt([]byte("data"), []byte("id"))
	blob[len(blob)-1] ^= 0xff
	if _, err := c.Decrypt(blob, []byte("id")); err == nil {
		t.Fatal("expected tamper detection")
	}
}

func TestCipherAADMismatch(t *testing.T) {
	key := make([]byte, 32)
	c, _ := New(key)
	blob, _ := c.Encrypt([]byte("data"), []byte("id-a"))
	if _, err := c.Decrypt(blob, []byte("id-b")); err == nil {
		t.Fatal("expected AAD mismatch failure")
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error for short key")
	}
}
