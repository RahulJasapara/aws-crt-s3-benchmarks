// Command s3-benchrunner-go is a benchmark runner for the AWS SDK for Go v2
// S3 Transfer Manager, part of the aws-crt-s3-benchmarks suite.
//
// Usage:
//
//	s3-benchrunner-go S3_CLIENT WORKLOAD BUCKET REGION TARGET_THROUGHPUT
//
// It reads a workload .run.json file, performs all its tasks (repeating until
// maxRepeatCount or maxRepeatSecs), and prints one line per run to stderr:
//
//	Run:N Secs:X.XXXXXX Gb/s:Y.YYYYYY
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

// exitCodeSkip signals to the harness that this runner intentionally skipped
// the workload (not a failure). Matches the convention used by other runners.
const exitCodeSkip = 123

func main() {
	os.Exit(run())
}

// run performs the benchmark and returns the process exit code. It is split out
// from main so that deferred work — flushing pprof profiles in particular —
// happens on every exit path, since os.Exit skips defers.
func run() int {
	if len(os.Args) < 6 {
		fmt.Fprintf(os.Stderr,
			"usage: %s S3_CLIENT WORKLOAD BUCKET REGION TARGET_THROUGHPUT\n", os.Args[0])
		return 2
	}

	clientID := S3ClientID(os.Args[1])
	workloadPath := os.Args[2]
	bucket := os.Args[3]
	region := os.Args[4]
	targetThroughput, err := strconv.ParseFloat(os.Args[5], 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid TARGET_THROUGHPUT %q: %v\n", os.Args[5], err)
		return 2
	}

	switch clientID {
	case clientTM, clientTMGet, clientTMStream:
	default:
		fmt.Fprintf(os.Stderr, "unknown S3_CLIENT %q (want %q, %q, or %q)\n",
			clientID, clientTM, clientTMGet, clientTMStream)
		return 2
	}

	workload, err := loadWorkload(workloadPath)
	if err != nil {
		// A parse failure most likely means a different schema version.
		fmt.Fprintf(os.Stderr, "Skipping benchmark - %v\n", err)
		return exitCodeSkip
	}
	if workload.Version != workloadVersion {
		fmt.Fprintf(os.Stderr, "Skipping benchmark - workload version %d not supported (want %d)\n",
			workload.Version, workloadVersion)
		return exitCodeSkip
	}

	ctx := context.Background()
	cfg := &BenchmarkConfig{
		Workload:             workload,
		Bucket:               bucket,
		Region:               region,
		TargetThroughputGbps: targetThroughput,
	}

	runner, err := NewRunner(ctx, clientID, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed creating runner: %v\n", err)
		return 1
	}

	gigabitsPerRun := bytesToGigabits(workload.bytesPerRun())

	// Start profiling after setup so credential resolution and client
	// construction stay out of the samples.
	stopProfiling, err := startProfiling()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	defer stopProfiling()

	appStart := time.Now()
	for runNum := 1; runNum <= workload.MaxRepeatCount; runNum++ {
		if err := prepareRun(workload); err != nil {
			fmt.Fprintf(os.Stderr, "failed preparing run %d: %v\n", runNum, err)
			return 1
		}

		runStart := time.Now()
		if err := runner.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "run %d failed: %v\n", runNum, err)
			return 1
		}
		runSecs := time.Since(runStart).Seconds()

		fmt.Fprintf(os.Stderr, "Run:%d Secs:%.6f Gb/s:%.6f\n",
			runNum, runSecs, gigabitsPerRun/runSecs)

		if time.Since(appStart).Seconds() >= workload.MaxRepeatSecs {
			break
		}
	}
	return 0
}

// bytesToGigabits converts bytes to decimal gigabits (bits / 1e9).
func bytesToGigabits(b int64) float64 {
	return float64(b*8) / 1e9
}
