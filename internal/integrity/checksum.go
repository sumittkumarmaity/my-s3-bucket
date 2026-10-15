package integrity

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// Castagnoli table used by AWS S3 for CRC32C
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

type HashingReader struct {
	reader    io.Reader
	crc       hash.Hash32
	sha       hash.Hash
	md5       hash.Hash
	bytesRead int64
}

func NewHashingReader(r io.Reader) *HashingReader {
	return &HashingReader{
		reader: r,
		crc:    crc32.New(crc32cTable),
		sha:    sha256.New(),
		md5:    md5.New(),
	}
}

func (h *HashingReader) Read(p []byte) (int, error) {
	n, err := h.reader.Read(p)
	if n > 0 {
		h.bytesRead += int64(n)
		_, _ = h.crc.Write(p[:n])
		_, _ = h.sha.Write(p[:n])
		_, _ = h.md5.Write(p[:n])
	}
	return n, err
}

func (h *HashingReader) BytesRead() int64 {
	return h.bytesRead
}

func (h *HashingReader) CRC32CHex() string {
	return fmt.Sprintf("%08x", h.crc.Sum32())
}

func (h *HashingReader) SHA256Hex() string {
	return hex.EncodeToString(h.sha.Sum(nil))
}

func (h *HashingReader) ETag() string {
	return fmt.Sprintf("\"%s\"", hex.EncodeToString(h.md5.Sum(nil)))
}

// ComputeChecksums calculates hashes directly for an io.Reader.
func ComputeChecksums(r io.Reader) (crc32c string, sha256Hex string, etag string, bytesRead int64, err error) {
	hr := NewHashingReader(r)
	buf := make([]byte, 32*1024)
	for {
		_, readErr := hr.Read(buf)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", "", 0, readErr
		}
	}
	return hr.CRC32CHex(), hr.SHA256Hex(), hr.ETag(), hr.BytesRead(), nil
}
