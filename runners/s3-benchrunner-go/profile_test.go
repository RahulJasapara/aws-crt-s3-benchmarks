package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// assertReadableProfile checks that path holds a complete pprof profile. pprof
// writes gzipped protobuf, so decompressing the whole file is what catches the
// failure that actually matters here: a profile that was never flushed by
// StopCPUProfile and is therefore truncated and useless. That mistake would only
// surface after a multi-hundred-GiB benchmark run, so it's worth a test.
func assertReadableProfile(t *testing.T, path string) {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("profile %q not created: %v", path, err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("profile %q is not gzip (pprof format): %v", path, err)
	}
	defer zr.Close()

	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("profile %q is truncated: %v", path, err)
	}
	if len(body) == 0 {
		t.Fatalf("profile %q decompressed to nothing", path)
	}
}

func TestStartProfilingDisabledByDefault(t *testing.T) {
	// Neither var set: profiling must be off, and the returned stop must still be
	// safe to defer.
	t.Setenv("CPU_PROFILE", "")
	t.Setenv("MEM_PROFILE", "")

	stop, err := startProfiling()
	if err != nil {
		t.Fatalf("unexpected error with no env set: %v", err)
	}
	if stop == nil {
		t.Fatal("stop func must be non-nil even when profiling is disabled")
	}
	stop() // must not panic
}

func TestStartProfilingWritesCPUProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	t.Setenv("CPU_PROFILE", path)
	t.Setenv("MEM_PROFILE", "")

	stop, err := startProfiling()
	if err != nil {
		t.Fatalf("startProfiling: %v", err)
	}

	// Burn a little CPU so the profiler has something to sample. We deliberately
	// don't assert a sample count — that would be flaky on a loaded machine.
	sink := 0
	for i := 0; i < 5_000_000; i++ {
		sink += i % 7
	}
	_ = sink

	stop()
	assertReadableProfile(t, path)
}

func TestStartProfilingWritesHeapProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mem.pprof")
	t.Setenv("CPU_PROFILE", "")
	t.Setenv("MEM_PROFILE", path)

	stop, err := startProfiling()
	if err != nil {
		t.Fatalf("startProfiling: %v", err)
	}

	// The heap snapshot is taken at stop time, so allocate before stopping.
	held := make([][]byte, 0, 64)
	for i := 0; i < 64; i++ {
		held = append(held, make([]byte, 64*1024))
	}

	stop()
	assertReadableProfile(t, path)

	// Keep held alive past the snapshot so the profile has live memory in it.
	if len(held) != 64 {
		t.Fatalf("held %d buffers, want 64", len(held))
	}
}

func TestStartProfilingBothProfilesTogether(t *testing.T) {
	dir := t.TempDir()
	cpuPath := filepath.Join(dir, "cpu.pprof")
	memPath := filepath.Join(dir, "mem.pprof")
	t.Setenv("CPU_PROFILE", cpuPath)
	t.Setenv("MEM_PROFILE", memPath)

	stop, err := startProfiling()
	if err != nil {
		t.Fatalf("startProfiling: %v", err)
	}
	stop()

	assertReadableProfile(t, cpuPath)
	assertReadableProfile(t, memPath)
}

func TestStartProfilingRejectsUnwritableCPUPath(t *testing.T) {
	// A bad path must fail fast, before the benchmark burns hours, rather than
	// silently running unprofiled.
	t.Setenv("CPU_PROFILE", filepath.Join(t.TempDir(), "no-such-dir", "cpu.pprof"))
	t.Setenv("MEM_PROFILE", "")

	if _, err := startProfiling(); err == nil {
		t.Fatal("expected an error for an uncreatable CPU_PROFILE path, got nil")
	}
}
