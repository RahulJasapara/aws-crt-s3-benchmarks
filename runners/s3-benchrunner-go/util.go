package main

import (
	"io"
	"math/rand"
	"syscall"
)

// newRandomData quickly generates a buffer of random data of the given size.
//
// Generating randomness is much slower than copying memory, so we only fill a
// modest prefix with real randomness and then copy that prefix repeatedly to
// fill the rest. The prefix length is chosen to not fall on a part boundary so
// no two parts are identical.
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

// fileDescriptorBudget returns a safe number of concurrent open files, derived
// from the soft RLIMIT_NOFILE (40% of it, matching the Python runner). Returns
// ok=false if the limit can't be read (e.g. Getrlimit unsupported).
func fileDescriptorBudget() (int, bool) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, false
	}
	return int(lim.Cur * 4 / 10), true
}

// patternReader is an io.Reader that synthesizes `size` bytes by repeating a
// small in-memory pattern, so a huge RAM upload doesn't require allocating the
// whole object up front (a 30 GiB task would otherwise be a 30 GiB heap alloc).
//
// It is deliberately NOT an io.Seeker: the Transfer Manager's UploadObject
// treats a non-seekable Body as a stream and uses the request's ContentLength
// for its multipart decisions, which is exactly the streaming path we want to
// exercise. Each task must use its own patternReader — it carries read state.
type patternReader struct {
	pattern []byte
	size    int64
	off     int64 // total bytes already produced
}

// newPatternReader returns a reader that will produce exactly size bytes drawn
// from pattern (which must be non-empty). pattern may be shared read-only
// across readers; the reader never mutates it.
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
