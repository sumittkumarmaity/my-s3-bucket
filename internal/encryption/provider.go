package encryption

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrDecryptionFailed = errors.New("decryption failed or data tampered")
)

type Provider interface {
	Encrypt(r io.Reader) (io.Reader, error)
	Decrypt(r io.Reader) (io.Reader, error)
	IsEnabled() bool
}

type NoOpProvider struct{}

func (n *NoOpProvider) Encrypt(r io.Reader) (io.Reader, error) { return r, nil }
func (n *NoOpProvider) Decrypt(r io.Reader) (io.Reader, error) { return r, nil }
func (n *NoOpProvider) IsEnabled() bool                        { return false }

// AESGCMProvider implements transparent streaming encryption using AES-256-GCM chunking.
// Format:
// [12-byte Base Nonce]
// Chunks:
//   [4-byte Chunk Size (length of ciphertext + tag)][1-byte isLast][Ciphertext + 16-byte Tag]
type AESGCMProvider struct {
	key [32]byte
}

const chunkSize = 64 * 1024 // 64KB plaintext chunks

func NewAESGCMProvider(secret string) (*AESGCMProvider, error) {
	if secret == "" {
		return nil, errors.New("encryption key cannot be empty")
	}
	// Derive 32-byte key using SHA-256
	key := sha256.Sum256([]byte(secret))
	return &AESGCMProvider{key: key}, nil
}

func (p *AESGCMProvider) IsEnabled() bool {
	return true
}

func (p *AESGCMProvider) Encrypt(r io.Reader) (io.Reader, error) {
	block, err := aes.NewCipher(p.key[:])
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	baseNonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, baseNonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		// Write base nonce
		if _, err := pw.Write(baseNonce); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		buf := make([]byte, chunkSize)
		var seq uint32 = 0

		for {
			n, err := io.ReadFull(r, buf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				_ = pw.CloseWithError(err)
				return
			}

			isLast := byte(0)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				isLast = 1
			}

			// Generate chunk nonce: baseNonce[0:8] XOR seq
			chunkNonce := make([]byte, 12)
			copy(chunkNonce, baseNonce)
			binary.BigEndian.PutUint32(chunkNonce[8:], seq)

			ciphertext := aesGCM.Seal(nil, chunkNonce, buf[:n], []byte{isLast})

			header := make([]byte, 5)
			binary.BigEndian.PutUint32(header[0:4], uint32(len(ciphertext)))
			header[4] = isLast

			if _, err := pw.Write(header); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := pw.Write(ciphertext); err != nil {
				_ = pw.CloseWithError(err)
				return
			}

			seq++
			if isLast == 1 {
				break
			}
		}
	}()

	return pr, nil
}

func (p *AESGCMProvider) Decrypt(r io.Reader) (io.Reader, error) {
	block, err := aes.NewCipher(p.key[:])
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	baseNonce := make([]byte, 12)
	if _, err := io.ReadFull(r, baseNonce); err != nil {
		return nil, fmt.Errorf("failed to read nonce: %w", err)
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		var seq uint32 = 0
		header := make([]byte, 5)

		for {
			if _, err := io.ReadFull(r, header); err != nil {
				if err == io.EOF {
					break
				}
				_ = pw.CloseWithError(err)
				return
			}

			cipherLen := binary.BigEndian.Uint32(header[0:4])
			isLast := header[4]

			cipherBuf := make([]byte, cipherLen)
			if _, err := io.ReadFull(r, cipherBuf); err != nil {
				_ = pw.CloseWithError(err)
				return
			}

			chunkNonce := make([]byte, 12)
			copy(chunkNonce, baseNonce)
			binary.BigEndian.PutUint32(chunkNonce[8:], seq)

			plaintext, err := aesGCM.Open(nil, chunkNonce, cipherBuf, []byte{isLast})
			if err != nil {
				_ = pw.CloseWithError(ErrDecryptionFailed)
				return
			}

			if _, err := pw.Write(plaintext); err != nil {
				_ = pw.CloseWithError(err)
				return
			}

			seq++
			if isLast == 1 {
				break
			}
		}
	}()

	return pr, nil
}

// EncryptBytes / DecryptBytes helpers
func EncryptBytes(p Provider, data []byte) ([]byte, error) {
	if !p.IsEnabled() {
		return data, nil
	}
	encReader, err := p.Encrypt(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(encReader)
}

func DecryptBytes(p Provider, data []byte) ([]byte, error) {
	if !p.IsEnabled() {
		return data, nil
	}
	decReader, err := p.Decrypt(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(decReader)
}
