package main

import (
	"bytes"
	"io"
	"testing"
)

// referenceData is what a full-allocation buffer of the given size would
// contain, so we can assert patternReader produces byte-identical output.
func referenceData(pattern []byte, size int64) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = pattern[i%len(pattern)]
	}
	return out
}

func TestPatternReaderProducesExactBytes(t *testing.T) {
	pattern := newRandomData(1024)
	// Sizes chosen to exercise: shorter than pattern, exact multiple, and
	// non-multiple that wraps several times.
	for _, size := range []int64{0, 1, 100, 1024, 1025, 4096, 5000} {
		want := referenceData(pattern, size)
		got, err := io.ReadAll(newPatternReader(pattern, size))
		if err != nil {
			t.Fatalf("size %d: ReadAll error: %v", size, err)
		}
		if int64(len(got)) != size {
			t.Fatalf("size %d: got %d bytes, want %d", size, len(got), size)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: bytes differ from reference pattern", size)
		}
	}
}

// TestPatternReaderSmallBuffers verifies correctness when the consumer reads
// through a buffer smaller than the pattern, so wrapping happens mid-buffer.
func TestPatternReaderSmallBuffers(t *testing.T) {
	pattern := newRandomData(64)
	const size = 1000
	want := referenceData(pattern, size)

	r := newPatternReader(pattern, size)
	var got []byte
	buf := make([]byte, 7) // deliberately coprime-ish with pattern length
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("small-buffer read differs from reference")
	}
}

func TestPatternReaderNotSeeker(t *testing.T) {
	// The RAM upload path depends on the reader being treated as a stream, so
	// it must NOT satisfy io.Seeker (which would make the SDK size it directly).
	var r io.Reader = newPatternReader([]byte{1}, 10)
	if _, ok := r.(io.Seeker); ok {
		t.Fatal("patternReader must not implement io.Seeker")
	}
}

func TestMaxConcurrentTasksBounds(t *testing.T) {
	got := maxConcurrentTasks()
	if got < 1 {
		t.Fatalf("maxConcurrentTasks = %d, must be >= 1", got)
	}
	if got > taskConcurrencyCap {
		t.Fatalf("maxConcurrentTasks = %d, must not exceed cap %d", got, taskConcurrencyCap)
	}
}
