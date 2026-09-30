package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a 16-byte hex id.
func New() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
