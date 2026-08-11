package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3ClientID selects which Go Transfer Manager download engine to benchmark.
type S3ClientID string

const (
	// clientTM uses DownloadObject (the io.WriterAt engine).
	clientTM S3ClientID = "sdk-go-tm"
	// clientTMGet uses GetObject (the io.Reader engine).
	clientTMGet S3ClientID = "sdk-go-tm-get"
)

// baselineConcurrency is the Concurrency used for benchmark runs.
//
// Set to 64 per team guidance: for now, don't exceed the number of CPUs on the
// instance (benchmarking on a 64-vCPU c7gn.16xlarge). The Go Transfer Manager
// has no native "target throughput" mode like the CRT/Rust runners, so we map
// TARGET_THROUGHPUT to Concurrency ourselves here. Keeping this in one function
// makes it trivial to sweep other values later.
const baselineConcurrency = 64

// concurrencyForTargetThroughput derives the Transfer Manager's Concurrency
// setting from the target throughput. For now this is a fixed baseline; the
// targetThroughputGbps argument is accepted so the mapping can be made
// throughput-aware later without touching call sites.
func concurrencyForTargetThroughput(targetThroughputGbps float64) int {
	return baselineConcurrency
}

// Runner executes a workload against S3 using the Go Transfer Manager.
type Runner struct {
	clientID   S3ClientID
	config     *BenchmarkConfig
	client     *transfermanager.Client
	uploadData []byte // pre-generated random data for RAM uploads
}

// BenchmarkConfig combines the workload with the command-line arguments.
type BenchmarkConfig struct {
	Workload             *WorkloadConfig
	Bucket               string
	Region               string
	TargetThroughputGbps float64
}

// NewRunner builds the Transfer Manager client and prepares upload data.
func NewRunner(ctx context.Context, clientID S3ClientID, cfg *BenchmarkConfig) (*Runner, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("failed loading AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)
	concurrency := concurrencyForTargetThroughput(cfg.TargetThroughputGbps)
	client := transfermanager.New(s3Client, func(o *transfermanager.Options) {
		o.Concurrency = concurrency
	})

	fmt.Fprintf(os.Stderr, "config: client=%s concurrency=%d target=%.1fGb/s region=%s\n",
		clientID, concurrency, cfg.TargetThroughputGbps, cfg.Region)

	r := &Runner{
		clientID: clientID,
		config:   cfg,
		client:   client,
	}

	// For RAM uploads, pre-generate one random buffer big enough for the
	// largest upload task, so buffer creation isn't measured in the timed run.
	if !cfg.Workload.FilesOnDisk {
		var maxUpload int64
		for _, t := range cfg.Workload.Tasks {
			if t.Action == ActionUpload && t.Size > maxUpload {
				maxUpload = t.Size
			}
		}
		if maxUpload > 0 {
			r.uploadData = newRandomData(int(maxUpload))
		}
	}

	return r, nil
}

// Run performs every task in the workload once, concurrently, and returns the
// first error encountered.
func (r *Runner) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i := range r.config.Workload.Tasks {
		wg.Add(1)
		go func(task TaskConfig) {
			defer wg.Done()
			err := r.runTask(ctx, task)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(r.config.Workload.Tasks[i])
	}

	wg.Wait()
	return firstErr
}

func (r *Runner) runTask(ctx context.Context, task TaskConfig) error {
	switch task.Action {
	case ActionUpload:
		return r.upload(ctx, task)
	case ActionDownload:
		return r.download(ctx, task)
	default:
		return fmt.Errorf("unknown task action: %q", task.Action)
	}
}

func (r *Runner) upload(ctx context.Context, task TaskConfig) error {
	var body io.Reader
	if r.config.Workload.FilesOnDisk {
		f, err := os.Open(task.Key)
		if err != nil {
			return fmt.Errorf("failed opening upload file %q: %w", task.Key, err)
		}
		defer f.Close()
		body = f
	} else {
		body = bytes.NewReader(r.uploadData[:task.Size])
	}

	_, err := r.client.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(r.config.Bucket),
		Key:    aws.String(task.Key),
		Body:   body,
	})
	if err != nil {
		return fmt.Errorf("failed uploading %q: %w", task.Key, err)
	}
	return nil
}

func (r *Runner) download(ctx context.Context, task TaskConfig) error {
	switch r.clientID {
	case clientTMGet:
		return r.downloadViaGetObject(ctx, task)
	default:
		return r.downloadViaDownloadObject(ctx, task)
	}
}

// downloadViaDownloadObject uses the io.WriterAt engine (DownloadObject).
func (r *Runner) downloadViaDownloadObject(ctx context.Context, task TaskConfig) error {
	var w io.WriterAt
	if r.config.Workload.FilesOnDisk {
		f, err := os.Create(task.Key)
		if err != nil {
			return fmt.Errorf("failed creating file %q: %w", task.Key, err)
		}
		defer f.Close()
		w = f
	} else {
		w = newDiscardWriterAt()
	}

	_, err := r.client.DownloadObject(ctx, &transfermanager.DownloadObjectInput{
		Bucket:   aws.String(r.config.Bucket),
		Key:      aws.String(task.Key),
		WriterAt: w,
	})
	if err != nil {
		return fmt.Errorf("failed downloading %q: %w", task.Key, err)
	}
	return nil
}

// downloadViaGetObject uses the io.Reader engine (GetObject).
func (r *Runner) downloadViaGetObject(ctx context.Context, task TaskConfig) error {
	out, err := r.client.GetObject(ctx, &transfermanager.GetObjectInput{
		Bucket: aws.String(r.config.Bucket),
		Key:    aws.String(task.Key),
	}, func(o *transfermanager.Options) {
		o.GetObjectType = types.GetObjectRanges
	})
	if err != nil {
		return fmt.Errorf("failed getting %q: %w", task.Key, err)
	}

	var dst io.Writer
	if r.config.Workload.FilesOnDisk {
		f, err := os.Create(task.Key)
		if err != nil {
			return fmt.Errorf("failed creating file %q: %w", task.Key, err)
		}
		defer f.Close()
		dst = f
	} else {
		dst = io.Discard
	}

	if _, err := io.Copy(dst, out.Body); err != nil {
		return fmt.Errorf("failed reading body of %q: %w", task.Key, err)
	}
	return nil
}

// prepareRun does per-run setup that must happen BEFORE the timer starts:
// for downloads, remove any stale file and ensure parent dirs exist; for
// uploads, verify the source file is present.
func prepareRun(w *WorkloadConfig) error {
	if !w.FilesOnDisk {
		return nil
	}
	for _, task := range w.Tasks {
		switch task.Action {
		case ActionDownload:
			if _, err := os.Stat(task.Key); err == nil {
				// Delete pre-existing file: overwriting may be slower than a
				// fresh write, which would skew the measurement.
				if err := os.Remove(task.Key); err != nil {
					return fmt.Errorf("failed removing stale file %q: %w", task.Key, err)
				}
			}
			if dir := filepath.Dir(task.Key); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("failed creating dir %q: %w", dir, err)
				}
			}
		case ActionUpload:
			if _, err := os.Stat(task.Key); err != nil {
				return fmt.Errorf("upload source file not found %q: %w", task.Key, err)
			}
		}
	}
	return nil
}
