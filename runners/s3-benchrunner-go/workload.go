package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// workloadVersion is the .run.json schema version this runner understands.
// If a workload's version differs, the runner must skip it (see main.go).
const workloadVersion = 2

// TaskAction is the type of transfer a task performs.
type TaskAction string

const (
	ActionUpload   TaskAction = "upload"
	ActionDownload TaskAction = "download"
)

// TaskConfig is a single transfer within a workload.
type TaskConfig struct {
	Action TaskAction `json:"action"`
	// Key in the S3 bucket. When FilesOnDisk is true, this is also the
	// relative file path on disk.
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

// WorkloadConfig is the parsed contents of a .run.json file.
type WorkloadConfig struct {
	Version        int          `json:"version"`
	Comment        string       `json:"comment"`
	FilesOnDisk    bool         `json:"filesOnDisk"`
	Checksum       *string      `json:"checksum"`
	MaxRepeatCount int          `json:"maxRepeatCount"`
	MaxRepeatSecs  float64      `json:"maxRepeatSecs"`
	Tasks          []TaskConfig `json:"tasks"`
}

// loadWorkload reads and parses a .run.json file.
func loadWorkload(path string) (*WorkloadConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed opening %q: %w", path, err)
	}

	var w WorkloadConfig
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("failed parsing %q (different version maybe?): %w", path, err)
	}
	return &w, nil
}

// bytesPerRun is the total number of bytes transferred in one pass over all tasks.
func (w *WorkloadConfig) bytesPerRun() int64 {
	var total int64
	for _, t := range w.Tasks {
		total += t.Size
	}
	return total
}
