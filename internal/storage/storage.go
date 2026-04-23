// Package storage defines the object-storage abstraction used across the OJ platform.
//
// All source code uploaded by contestants is persisted in an S3-compatible store
// (MinIO in self-hosted deployments).  Test-case input/output files may live on
// the same store or on a shared NFS mount — the judger does not care which, as
// long as it can reach them by the time RunTestCase is called.
//
// # Key schema for contestant source files
//
//	Bucket : submissions
//	Key    : sources/{userID}/{problemID}/{uuid}.{ext}
//
// The key is stored verbatim in models.Submission.SourceCodePath and propagated
// into models.JudgeTask.SourceCodePath.  No local filesystem path ever appears
// in either field; the judger downloads via ObjectStore.GetToFile.
package storage

import (
	"context"
	"io"
	"time"
)

// Bucket names used by the platform.
const (
	// BucketSubmissions holds contestant source code.
	BucketSubmissions = "submissions"
	// BucketTestcases holds problem input/output files uploaded by problem setters.
	BucketTestcases = "testcases"
)

// ObjectInfo carries metadata returned by Stat.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	LastModified time.Time
	// ETag is the MD5 hex digest for single-part uploads (how we always store
	// source zips and testcase zips).  Used by the testcase cache to detect
	// whether a remote zip has changed without re-downloading the body.
	ETag string
}

// ObjectStore is the unified interface for reading and writing objects.
// All implementations must be safe for concurrent use.
type ObjectStore interface {
	// Put uploads body as an object.  size must equal len(body); pass -1 only when
	// the size is genuinely unknown (chunked upload) — MinIO will buffer the whole
	// stream in that case, which is expensive.
	Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error

	// Get downloads the object and returns a ReadCloser the caller must close.
	// Returns ErrNotFound if the object does not exist.
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, error)

	// GetToFile downloads the object directly into a local file at destPath.
	// Creates or truncates the file; parent directory must already exist.
	GetToFile(ctx context.Context, bucket, key, destPath string) error

	// PutFile uploads a local file at srcPath.  Computes the size from the file
	// metadata — no need for the caller to stat the file first.
	PutFile(ctx context.Context, bucket, key, srcPath string, contentType string) error

	// Stat returns metadata without downloading the object body.
	Stat(ctx context.Context, bucket, key string) (ObjectInfo, error)

	// Delete removes an object.  Silently succeeds if the object does not exist.
	Delete(ctx context.Context, bucket, key string) error

	// EnsureBucket creates the bucket if it does not already exist.
	// Idempotent; safe to call at startup.
	EnsureBucket(ctx context.Context, bucket string) error
}
