package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
)

// startProfiling honors two env vars, each naming a file to write a pprof
// profile to:
//
//	CPU_PROFILE  where CPU samples go (the one you usually want)
//	MEM_PROFILE  where a heap snapshot, taken after the last run, goes
//
// Env vars rather than flags, so profiling composes with the existing
// CONCURRENCY / PART_SIZE_MIB sweeps without touching the harness's fixed
// command line. Neither var set means no profiling and a no-op stop.
//
// It returns a stop function that MUST run before the process exits: only
// pprof.StopCPUProfile flushes the CPU profile, so skipping it leaves a
// truncated, unreadable file.
//
// Capture spans every run in the workload rather than one. That's what you want
// here — idle time between runs burns no CPU and so adds no samples, while more
// runs mean more samples. A 30 GiB download costing ~40 CPU-seconds per run
// yields tens of thousands of samples at pprof's default 100 Hz.
//
// Read the result with:
//
//	go tool pprof -top /tmp/cpu.pprof
//	go tool pprof -http=:8080 /tmp/cpu.pprof
func startProfiling() (func(), error) {
	var stops []func()
	stop := func() {
		// Registration order matters: the CPU profile stops before the heap
		// snapshot is taken, so the snapshot's own work stays out of the CPU
		// samples.
		for _, s := range stops {
			s()
		}
	}

	if path := os.Getenv("CPU_PROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("invalid CPU_PROFILE %q: %w", path, err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return nil, fmt.Errorf("failed starting CPU profile: %w", err)
		}
		stops = append(stops, func() {
			pprof.StopCPUProfile()
			f.Close()
		})
		fmt.Fprintf(os.Stderr, "profile: cpu=%s\n", path)
	}

	if path := os.Getenv("MEM_PROFILE"); path != "" {
		// A heap profile is a snapshot, so take it at stop time (after the last
		// run) rather than now, when nothing has been transferred yet.
		stops = append(stops, func() {
			f, err := os.Create(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid MEM_PROFILE %q: %v\n", path, err)
				return
			}
			defer f.Close()
			runtime.GC() // so the snapshot reflects live memory, not garbage
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "failed writing heap profile: %v\n", err)
			}
		})
		fmt.Fprintf(os.Stderr, "profile: mem=%s\n", path)
	}

	return stop, nil
}
