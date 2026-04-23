package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned by Get/Stat when the object does not exist.
var ErrNotFound = errors.New("storage: object not found")

// MinioConfig holds the connection parameters for a MinIO (or S3-compatible) endpoint.
type MinioConfig struct {
	// Endpoint is the host:port (or host) of the MinIO server.
	// Examples: "minio:9000", "s3.amazonaws.com"
	Endpoint string
	// AccessKey and SecretKey are the credentials.
	AccessKey string
	SecretKey string
	// UseSSL controls whether TLS is used.  Set false for in-cluster plain-text.
	UseSSL bool
	// Region is optional; defaults to "us-east-1" for MinIO compatibility.
	Region string
}

// MinioStore implements ObjectStore on top of the official minio-go client.
// All methods are safe for concurrent use.
type MinioStore struct {
	client *minio.Client
	region string
}

// NewMinio creates a MinioStore and verifies connectivity via BucketExists.
func NewMinio(cfg MinioConfig) (*MinioStore, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("minio.New: %w", err)
	}

	return &MinioStore{client: client, region: cfg.Region}, nil
}

// ─── ObjectStore implementation ───────────────────────────────────────────────

// Put uploads body as an object with the given content type.
// size must be the exact byte length; pass -1 only when unknown (buffered by MinIO).
func (s *MinioStore) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, bucket, key, body, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("minio Put %s/%s: %w", bucket, key, err)
	}
	return nil
}

// PutFile opens srcPath, stats it for the size, and uploads it to bucket/key.
func (s *MinioStore) PutFile(ctx context.Context, bucket, key, srcPath string, contentType string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("minio PutFile open %s: %w", srcPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("minio PutFile stat %s: %w", srcPath, err)
	}

	return s.Put(ctx, bucket, key, f, fi.Size(), contentType)
}

// Get downloads object bucket/key and returns a ReadCloser.
// The caller must close the reader.  Returns ErrNotFound if missing.
func (s *MinioStore) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, s.wrapErr(err, bucket, key)
	}
	// Probe the object to surface "not found" early rather than at first Read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, s.wrapErr(err, bucket, key)
	}
	return obj, nil
}

// GetToFile downloads bucket/key and writes it to a local file at destPath.
// Creates or truncates the file atomically via a temp file + rename.
func (s *MinioStore) GetToFile(ctx context.Context, bucket, key, destPath string) error {
	rc, err := s.Get(ctx, bucket, key)
	if err != nil {
		return err
	}
	defer rc.Close()

	// Write to a temp file in the same directory, then rename.
	// This avoids leaving a partial file if the download is interrupted.
	tmp := destPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("minio GetToFile create tmp %s: %w", tmp, err)
	}

	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("minio GetToFile copy %s/%s → %s: %w", bucket, key, destPath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("minio GetToFile close tmp: %w", err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("minio GetToFile rename: %w", err)
	}
	return nil
}

// Stat returns object metadata without downloading the body.
// Returns ErrNotFound if the object does not exist.
func (s *MinioStore) Stat(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, s.wrapErr(err, bucket, key)
	}
	return ObjectInfo{
		Key:          info.Key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		LastModified: info.LastModified,
		ETag:         strings.Trim(info.ETag, `"`), // MinIO wraps ETag in quotes; strip them
	}, nil
}

// Delete removes an object.  Silently succeeds if the object does not exist.
func (s *MinioStore) Delete(ctx context.Context, bucket, key string) error {
	err := s.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("minio Delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

// EnsureBucket creates the bucket if it does not already exist.
// Idempotent; safe to call at startup for each known bucket.
func (s *MinioStore) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("minio EnsureBucket exists check %s: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
		// Handle the race where another instance created it between our check and here.
		if isAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("minio EnsureBucket make %s: %w", bucket, err)
	}
	return nil
}

// ─── Error helpers ────────────────────────────────────────────────────────────

func (s *MinioStore) wrapErr(err error, bucket, key string) error {
	if isNotFound(err) {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, bucket, key)
	}
	return fmt.Errorf("minio %s/%s: %w", bucket, key, err)
}

func isNotFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" ||
			resp.StatusCode == 404
	}
	return false
}

func isAlreadyExists(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "BucketAlreadyOwnedByYou" ||
			resp.Code == "BucketAlreadyExists"
	}
	return false
}

// PresignedGetURL returns a time-limited URL for direct browser downloads.
// Useful for letting the judge dashboard link directly to source files.
func (s *MinioStore) PresignedGetURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, bucket, key, expiry, url.Values{})
	if err != nil {
		return "", fmt.Errorf("minio presign %s/%s: %w", bucket, key, err)
	}
	return u.String(), nil
}
