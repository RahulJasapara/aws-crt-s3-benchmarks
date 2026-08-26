package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	// clientTMGet uses GetObject (the io.Reader engine), draining the body
	// with a single bulk io.Copy.
	clientTMGet S3ClientID = "sdk-go-tm-get"
	// clientTMStream uses GetObject (the io.Reader engine) but drains the body
	// chunk-by-chunk, writing each chunk straight through to the destination.
	clientTMStream S3ClientID = "sdk-go-tm-stream"
)

// defaultPartSizeBytes is the Transfer Manager part size. 8 MiB matches the other
// runners (CRT/Rust/C++) for apples-to-apples runs; override via PART_SIZE_MIB.
const defaultPartSizeBytes = 8 * 1024 * 1024

// uploadPatternSize (~31 MiB) is the reusable RAM-upload source buffer. Not a
// multiple of any swept part size, so consecutive parts differ and can't be deduped.
const uploadPatternSize = 31 * 1024 * 1024

// baselineConcurrency is the fixed Transfer Manager Concurrency for runs (64,
// matching the 64-vCPU test instance; see concurrencyForTargetThroughput).
const baselineConcurrency = 64

// concurrencyForTargetThroughput maps target throughput to Concurrency. Fixed
// for now; the arg is kept so the mapping can become throughput-aware later.
func concurrencyForTargetThroughput(targetThroughputGbps float64) int {
	return baselineConcurrency
}

// taskConcurrencyCap is the hard upper bound on simultaneously-running tasks;
// the fd-derived budget may lower it but never raise it (cf. Java's 1000).
const taskConcurrencyCap = 1000

// maxConcurrentTasks bounds simultaneously-running tasks (each holds fds: a
// socket, plus a disk file for on-disk workloads): min(fd budget, cap).
func maxConcurrentTasks() int {
	limit := taskConcurrencyCap
	if n, ok := fileDescriptorBudget(); ok && n < limit {
		limit = n
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// Runner executes a workload against S3 using the Go Transfer Manager.
type Runner struct {
	clientID   S3ClientID
	config     *BenchmarkConfig
	client     *transfermanager.Client
	partSize   int64  // resolved part size; also the streaming read buffer size
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
	// Override Concurrency via CONCURRENCY to sweep in-flight parallelism; applied
	// before the buffer sizing below so the GetObject window still tracks it.
	if v := os.Getenv("CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid CONCURRENCY %q: want a positive integer", v)
		}
		concurrency = n
	}
	// Override the part size via PART_SIZE_MIB to sweep request size; S3 requires
	// >=5 MiB parts for the multipart uploads this also feeds.
	partSize := int64(defaultPartSizeBytes)
	if v := os.Getenv("PART_SIZE_MIB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 5 {
			return nil, fmt.Errorf("invalid PART_SIZE_MIB %q: want an integer >= 5", v)
		}
		partSize = int64(n) * 1024 * 1024
	}
	// GetObject buffer sets the in-flight window (buffer/partSize parts, capped at
	// Concurrency); defaults to window==Concurrency, override via GET_BUFFER_MIB.
	getBufferBytes := int64(concurrency) * partSize
	if mib := os.Getenv("GET_BUFFER_MIB"); mib != "" {
		if v, err := strconv.Atoi(mib); err == nil && v > 0 {
			getBufferBytes = int64(v) * 1024 * 1024
		}
	}
	client := transfermanager.New(s3Client, func(o *transfermanager.Options) {
		o.Concurrency = concurrency
		o.PartSizeBytes = partSize
		o.GetObjectBufferSize = getBufferBytes
	})

	fmt.Fprintf(os.Stderr, "config: client=%s concurrency=%d partSize=%dMiB getBuf=%dMiB target=%.1fGb/s region=%s\n",
		clientID, concurrency, partSize/(1024*1024), getBufferBytes/(1024*1024), cfg.TargetThroughputGbps, cfg.Region)

	r := &Runner{
		clientID: clientID,
		config:   cfg,
		client:   client,
		partSize: partSize,
	}

	// For RAM uploads, pre-generate ONE ~31 MiB pattern buffer (not the full
	// object); each task streams from it via a patternReader, off the timed path.
	if !cfg.Workload.FilesOnDisk {
		var hasUpload bool
		for _, t := range cfg.Workload.Tasks {
			if t.Action == ActionUpload {
				hasUpload = true
				break
			}
		}
		if hasUpload {
			r.uploadData = newRandomData(uploadPatternSize)
		}
	}

	return r, nil
}

// Run performs every task once, concurrently (bounded by maxConcurrentTasks),
// and fails fast: the first error cancels the rest and is returned.
func (r *Runner) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	tasks := r.config.Workload.Tasks
	limit := min(maxConcurrentTasks(), len(tasks))
	sem := make(chan struct{}, limit)

	for i := range tasks {
		mu.Lock()
		failed := firstErr != nil
		mu.Unlock()
		if failed {
			break // stop launching once a task has failed
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(task TaskConfig) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := r.runTask(ctx, task); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel() // abort in-flight transfers
				}
				mu.Unlock()
			}
		}(tasks[i])
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
	in := &transfermanager.UploadObjectInput{
		Bucket: aws.String(r.config.Bucket),
		Key:    aws.String(task.Key),
	}
	if r.config.Workload.FilesOnDisk {
		f, err := os.Open(task.Key)
		if err != nil {
			return fmt.Errorf("failed opening upload file %q: %w", task.Key, err)
		}
		defer f.Close()
		in.Body = f
	} else {
		// Stream from the shared pattern instead of buffering the object. The
		// reader is non-seekable, so set ContentLength for the SDK's multipart sizing.
		in.Body = newPatternReader(r.uploadData, task.Size)
		in.ContentLength = aws.Int64(task.Size)
	}

	_, err := r.client.UploadObject(ctx, in)
	if err != nil {
		return fmt.Errorf("failed uploading %q: %w", task.Key, err)
	}
	return nil
}

func (r *Runner) download(ctx context.Context, task TaskConfig) error {
	switch r.clientID {
	case clientTMGet:
		return r.downloadViaGetObject(ctx, task)
	case clientTMStream:
		return r.downloadViaStream(ctx, task)
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

	out, err := r.client.DownloadObject(ctx, &transfermanager.DownloadObjectInput{
		Bucket:   aws.String(r.config.Bucket),
		Key:      aws.String(task.Key),
		WriterAt: w,
	})
	if err != nil {
		return fmt.Errorf("failed downloading %q: %w", task.Key, err)
	}
	// Guard against a silent partial transfer looking like a fast success.
	if got := aws.ToInt64(out.ContentLength); got != task.Size {
		return fmt.Errorf("downloaded %q: got %d bytes, want %d", task.Key, got, task.Size)
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

	n, err := io.Copy(dst, out.Body)
	if err != nil {
		return fmt.Errorf("failed reading body of %q: %w", task.Key, err)
	}
	// Guard against a silent partial transfer looking like a fast success.
	if n != task.Size {
		return fmt.Errorf("downloaded %q: got %d bytes, want %d", task.Key, n, task.Size)
	}
	return nil
}

// downloadViaStream drains the GetObject body through one reused part-sized
// buffer, writing each chunk straight to dst (no user-space accumulation).
func (r *Runner) downloadViaStream(ctx context.Context, task TaskConfig) error {
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

	buf := make([]byte, r.partSize)
	var total int64
	for {
		nr, rerr := out.Body.Read(buf)
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			if werr != nil {
				return fmt.Errorf("failed writing chunk of %q: %w", task.Key, werr)
			}
			if nw != nr {
				return fmt.Errorf("short write on %q: wrote %d of %d bytes", task.Key, nw, nr)
			}
			total += int64(nr)
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return fmt.Errorf("failed reading body of %q: %w", task.Key, rerr)
		}
	}

	// Guard against a silent partial transfer looking like a fast success.
	if total != task.Size {
		return fmt.Errorf("downloaded %q: got %d bytes, want %d", task.Key, total, task.Size)
	}
	return nil
}

// prepareRun does per-run setup before the timer starts: clear stale download
// files and make parent dirs; for uploads, verify the source exists.
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
