package encryption

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestAESGCMProvider(t *testing.T) {
	key := "my-super-secret-passphrase-32-bytes!"
	prov, err := NewAESGCMProvider(key)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	testCases := [][]byte{
		[]byte(""),
		[]byte("Hello World"),
		bytes.Repeat([]byte("A"), 1024),
		bytes.Repeat([]byte("B"), 150*1024), // spans multiple 64KB chunks
	}

	for i, tc := range testCases {
		encrypted, err := EncryptBytes(prov, tc)
		if err != nil {
			t.Fatalf("test case %d encrypt error: %v", i, err)
		}
		if len(tc) > 0 && bytes.Equal(encrypted, tc) {
			t.Fatalf("test case %d encrypted bytes match plaintext!", i)
		}

		decrypted, err := DecryptBytes(prov, encrypted)
		if err != nil {
			t.Fatalf("test case %d decrypt error: %v", i, err)
		}
		if !bytes.Equal(decrypted, tc) {
			t.Fatalf("test case %d decrypted bytes mismatch", i)
		}
	}

	// Test tampering detection
	payload := []byte("Tamper test payload")
	encrypted, _ := EncryptBytes(prov, payload)
	if len(encrypted) > 20 {
		encrypted[20] ^= 0xFF // corrupt a byte in ciphertext
		_, err := DecryptBytes(prov, encrypted)
		if err == nil {
			t.Fatalf("expected tampering error, got nil")
		}
	}

	// Test random large payload
	bigData := make([]byte, 256*1024)
	_, _ = rand.Read(bigData)
	encBig, err := EncryptBytes(prov, bigData)
	if err != nil {
		t.Fatalf("failed to encrypt big data: %v", err)
	}
	decBig, err := DecryptBytes(prov, encBig)
	if err != nil {
		t.Fatalf("failed to decrypt big data: %v", err)
	}
	if !bytes.Equal(decBig, bigData) {
		t.Fatal("big data mismatch")
	}
}
