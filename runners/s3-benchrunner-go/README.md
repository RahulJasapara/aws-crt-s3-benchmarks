# s3-benchrunner-go

s3-benchrunner for the [AWS SDK for Go v2 S3 Transfer Manager](https://github.com/aws/aws-sdk-go-v2/tree/main/feature/s3/transfermanager).

## Building

```sh
cd aws-crt-s3-benchmarks/runners/s3-benchrunner-go
go build
```

This produces the `s3-benchrunner-go` binary.

By default `go.mod` uses `replace` directives to build against a **local**
checkout of `aws-sdk-go-v2` at `../../../aws-sdk-go-v2` (so you can benchmark
in-progress changes to the Transfer Manager). Adjust or remove those directives
to build against a published release instead.

## Running

```
s3-benchrunner-go S3_CLIENT WORKLOAD BUCKET REGION TARGET_THROUGHPUT
```

*   `S3_CLIENT`: which download engine to use:
    *   `sdk-go-tm` — `DownloadObject` (the `io.WriterAt` engine)
    *   `sdk-go-tm-get` — `GetObject` (the `io.Reader` engine), draining the
        body with a single bulk `io.Copy`
    *   `sdk-go-tm-stream` — `GetObject` (the `io.Reader` engine), draining the
        body chunk-by-chunk through a single reused buffer

    (Uploads always use `UploadObject`; the client ID only affects downloads.)
*   `WORKLOAD`: path to a workload `.run.json` file (see [workloads/](../../workloads))
*   `BUCKET`: S3 bucket name
*   `REGION`: AWS Region (e.g. `us-west-2`)
*   `TARGET_THROUGHPUT`: target throughput in gigabits per second (e.g. `100.0`)

Run from the `FILES_DIR` used by `prep-s3-files.py`. Credentials are resolved
via the standard AWS credential chain.

### Concurrency

The Go Transfer Manager has no native "target throughput" mode (unlike the CRT
and Rust runners). `TARGET_THROUGHPUT` is currently mapped to a fixed
`Concurrency` baseline in `concurrencyForTargetThroughput` (see `runner.go`),
which is the single place to change when sweeping concurrency values.

For the `GetObject`-based engines (`sdk-go-tm-get`, `sdk-go-tm-stream`) the SDK's
in-flight window is `min(Concurrency, GetObjectBufferSize / PartSizeBytes)`, so
the runner sets `GetObjectBufferSize = Concurrency * PartSizeBytes` — otherwise
the 50 MiB default would cap those engines at ~6 parts in flight regardless of
`Concurrency`.

### Environment overrides

These sweep parameters without editing code or changing the harness's fixed
command line:

| Variable | Effect |
|---|---|
| `CONCURRENCY` | Overrides the `Concurrency` derived from `TARGET_THROUGHPUT` |
| `PART_SIZE_MIB` | Overrides the 8 MiB part size (S3 requires >= 5) |
| `GET_BUFFER_MIB` | Overrides `GetObjectBufferSize` (default `Concurrency * PartSizeBytes`) |
| `CPU_PROFILE` | Path to write a pprof CPU profile |
| `MEM_PROFILE` | Path to write a pprof heap snapshot, taken after the last run |

`PART_SIZE_MIB` does **not** affect `sdk-go-tm` downloads. In the Transfer
Manager's default `GetObjectParts` mode the download part size comes from the
object's upload-time part boundaries (the response `ContentLength`), never from
`PartSizeBytes` — so sweeping it changes nothing for that engine. It does affect
uploads and the range-based engines.

### Profiling

Set `CPU_PROFILE` to capture where the runner spends CPU. Capture spans every run
in the workload; idle time between runs burns no CPU and so adds no samples, so a
multi-repeat workload just yields more data.

```sh
CPU_PROFILE=/tmp/cpu.pprof CONCURRENCY=768 \
  ./s3-benchrunner-go sdk-go-tm workload.run.json BUCKET us-east-1 200
```

Then analyze:

```sh
go tool pprof -top /tmp/cpu.pprof         # hot functions, by self time
go tool pprof -top -cum /tmp/cpu.pprof    # by cumulative time (better for I/O paths)
go tool pprof -http=:8080 /tmp/cpu.pprof  # flame graph in a browser
```

A Go CPU profile samples user-space stacks, so it accounts for the process's
**user** CPU time. Kernel time — the `System` figure from `/usr/bin/time -v` — is
attributed to whichever Go function made the syscall but is not broken down
further; use `perf` or `strace -c` to go inside it.

## Status

Single-object upload/download in both on-disk and in-memory (RAM) modes.
Downloads are benchmarked via three distinct engines: `DownloadObject`
(`io.WriterAt`), `GetObject` with a bulk `io.Copy`, and `GetObject` with a
chunk-by-chunk streaming path that writes through a reused buffer. The part size
is pinned to 8 MiB to match the other runners for fair comparison, and every
download asserts the transferred byte count equals the task size (so a partial
transfer can't masquerade as a fast success).

Not yet implemented: directory APIs (`UploadDirectory` / `DownloadDirectory`),
the `--disable-directory` flag, checksum validation, and telemetry.
