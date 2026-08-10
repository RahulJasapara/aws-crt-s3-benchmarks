package main

import "math/rand"

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
