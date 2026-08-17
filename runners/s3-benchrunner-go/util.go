package main

import (
	"io"
	"math/rand"
	"syscall"
)

// newRandomData returns size bytes of pseudo-random data. Only a ~30 MiB prefix
// is truly random; it is copied forward to fill the rest (copy beats RNG).
func newRandomData(size int) []byte {
	data := make([]byte, size)

	// ~30MiB of real randomness (digits of pi as an arbitrary non-round len).
	randLen := 31415926
	if randLen > size {
		randLen = size
	}

	//nolint:gosec // benchmark payload, not security-sensitive
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < randLen; i++ {
		data[i] = byte(rng.Intn(256))
	}

	// Copy the random prefix forward until the buffer is full.
	for filled := randLen; filled < size; {
		n := copy(data[filled:], data[:filled])
		filled += n
	}
	return data
}

// discardWriterAt is an io.WriterAt that throws away all data. Used for RAM
// downloads, where we only care about throughput, not the bytes.
type discardWriterAt struct{}

func newDiscardWriterAt() *discardWriterAt { return &discardWriterAt{} }

func (*discardWriterAt) WriteAt(p []byte, _ int64) (int, error) {
	return len(p), nil
}

// fileDescriptorBudget returns 40% of the soft RLIMIT_NOFILE (matching the
// Python runner), or ok=false if the limit can't be read.
func fileDescriptorBudget() (int, bool) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, false
	}
	return int(lim.Cur * 4 / 10), true
}

// patternReader repeats a small pattern to produce size bytes without a full-
// object allocation. Not an io.Seeker (forces the SDK's streaming upload path).
type patternReader struct {
	pattern []byte
	size    int64
	off     int64 // total bytes already produced
}

// newPatternReader returns a reader producing size bytes from pattern (non-empty,
// shared read-only). Each reader carries its own read state.
func newPatternReader(pattern []byte, size int64) *patternReader {
	return &patternReader{pattern: pattern, size: size}
}

func (r *patternReader) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	remaining := r.size - r.off
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n := 0
	for n < len(p) {
		start := int((r.off + int64(n)) % int64(len(r.pattern)))
		c := copy(p[n:], r.pattern[start:])
		n += c
	}
	r.off += int64(n)
	if r.off >= r.size {
		return n, io.EOF
	}
	return n, nil
}
