package domain

import (
	"crypto/rand"
	"time"
)

// crockford is the Crockford base32 alphabet used by ULIDs (sortable, no ambiguous chars).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a 26-char, time-sortable, ULID-style identifier (§02.1).
// The first 10 chars encode the millisecond timestamp; the remaining 16 are random.
// App-generated so we never depend on DB auto-increment/identity (portability, §02.7).
func NewID() string {
	t := uint64(time.Now().UnixMilli()) & ((1 << 48) - 1)
	out := make([]byte, 26)
	for i := range 10 {
		out[9-i] = crockford[(t>>(5*uint(i)))&31]
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	for i := range 16 {
		out[10+i] = crockford[int(b[i])&31]
	}
	return string(out)
}
