package event

import (
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
)

var (
	fastIDPrefix [19]byte
	fastIDLo atomic.Uint64
)

func init() {
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err == nil {
		hi := seed[:8]
		// Set version (4) bits on the high bytes once.
		hi[6] = (hi[6] & 0x0f) | 0x40
		encodeUUIDPrefix(&fastIDPrefix, hi)
		fastIDLo.Store(binary.BigEndian.Uint64(seed[8:]))
	} else {
		// Fallback: still produce unique ids within the process.
		hi := []byte{0, 0, 0, 0, 0, 0, 0, 0}
		hi[6] = (hi[6] & 0x0f) | 0x40
		encodeUUIDPrefix(&fastIDPrefix, hi)
		fastIDLo.Store(0)
	}
}

const hexTable = "0123456789abcdef"

func encodeHexByte(dst []byte, b byte) {
	dst[0] = hexTable[b>>4]
	dst[1] = hexTable[b&0x0f]
}

func encodeUUIDPrefix(dst *[19]byte, hi []byte) {
	// UUID string layout: 8-4-4-4-12 (36 chars, 4 hyphens).
	// Prefix covers the first three groups + trailing hyphen: xxxxxxxx-xxxx-xxxx-
	encodeHexByte(dst[0:2], hi[0])
	encodeHexByte(dst[2:4], hi[1])
	encodeHexByte(dst[4:6], hi[2])
	encodeHexByte(dst[6:8], hi[3])
	dst[8] = '-'
	encodeHexByte(dst[9:11], hi[4])
	encodeHexByte(dst[11:13], hi[5])
	dst[13] = '-'
	encodeHexByte(dst[14:16], hi[6])
	encodeHexByte(dst[16:18], hi[7])
	dst[18] = '-'
}

// NewID returns a unique event id string.
//
// It avoids per-call crypto/rand usage to reduce overhead on high-throughput
// event streams while preserving a UUID-shaped identifier.
func NewID() string {
	lo := fastIDLo.Add(1)
	var buf [36]byte
	copy(buf[:19], fastIDPrefix[:])

	var loBytes [8]byte
	binary.BigEndian.PutUint64(loBytes[:], lo)
	// Set RFC 4122 variant bits on the first byte of the low segment.
	loBytes[0] = (loBytes[0] & 0x3f) | 0x80

	// Group 4 (bytes 8-9): xxxx
	encodeHexByte(buf[19:21], loBytes[0])
	encodeHexByte(buf[21:23], loBytes[1])
	buf[23] = '-'
	// Group 5 (bytes 10-15): xxxxxxxxxxxx
	encodeHexByte(buf[24:26], loBytes[2])
	encodeHexByte(buf[26:28], loBytes[3])
	encodeHexByte(buf[28:30], loBytes[4])
	encodeHexByte(buf[30:32], loBytes[5])
	encodeHexByte(buf[32:34], loBytes[6])
	encodeHexByte(buf[34:36], loBytes[7])

	return string(buf[:])
}
