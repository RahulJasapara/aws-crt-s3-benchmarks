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
        body chunk-by-chunk through a reused buffer rather than buffering the
        whole object

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
