package handler

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/core/ranking"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

const (
	maxUploadBytes   = 500 * 1024 * 1024 // 500 MB max zip upload
	maxZipValidBytes = 4 * 1024          // read this many bytes for magic-byte check
)

// zipMagic is the signature of a valid ZIP file.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// AdminHandler exposes privileged contest- and problem-management endpoints.
type AdminHandler struct {
	rankingService *ranking.RankingService
	store          storage.ObjectStore
	log            *zap.Logger
}

func NewAdminHandler(
	rankingService *ranking.RankingService,
	store storage.ObjectStore,
	log *zap.Logger,
) *AdminHandler {
	return &AdminHandler{rankingService: rankingService, store: store, log: log}
}

// ─── POST /api/v1/admin/contests/:contest_id/unfreeze-next ───────────────────

// UnfreezeNext reveals one frozen submission for the lowest-ranked team.
// Call repeatedly during the post-contest 滚榜 ceremony.
//
// 200 → delta published (contains OldRank/NewRank for frontend animation).
// 204 → nothing left to reveal.
func (h *AdminHandler) UnfreezeNext(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}

	delta, err := h.rankingService.UnfreezeNext(c.Request.Context(), contestID)
	if err != nil {
		h.log.Error("UnfreezeNext failed", zap.Error(err), zap.Int64("contest_id", contestID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if delta == nil {
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, delta)
}

// ─── POST /api/v1/admin/problems/:id/testcases ───────────────────────────────

// UploadTestcases accepts a multipart-form field named "file" containing a ZIP
// archive, validates it, and uploads it to MinIO at:
//
//	bucket : testcases
//	key    : testcases/{problemID}/data.zip
//
// Expected ZIP layout (flat, no subdirectories required):
//
//	1.in   1.out
//	2.in   2.out
//	...
//
// The judger will extract the zip and resolve JudgeTestCase.InputPath /
// OutputPath relative to the extracted directory.
//
// On success, the response includes the object key and a listing of files
// found in the zip, so the caller can verify the upload is complete.
func (h *AdminHandler) UploadTestcases(c *gin.Context) {
	problemIDStr := c.Param("id")
	problemID64, err := strconv.ParseInt(problemIDStr, 10, 64)
	if err != nil || problemID64 <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}
	problemID := models.ID(problemID64)

	// Enforce max upload size before reading the multipart body.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBytes)

	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "field 'file' is required (multipart/form-data)"})
		return
	}

	// ── Validate filename extension ───────────────────────────────────────────
	if !strings.EqualFold(filepath.Ext(fh.Filename), ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "uploaded file must have a .zip extension"})
		return
	}

	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot open uploaded file"})
		return
	}
	defer f.Close()

	// ── Validate ZIP magic bytes ──────────────────────────────────────────────
	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil || string(magic) != string(zipMagic) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is not a valid ZIP archive"})
		return
	}
	// Seek back to the beginning so the full file can be uploaded.
	if seeker, ok := f.(interface{ Seek(int64, int) (int64, error) }); ok {
		if _, err := seeker.Seek(0, 0); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "seek error"})
			return
		}
	}

	// ── Upload to MinIO ───────────────────────────────────────────────────────
	key := fmt.Sprintf("testcases/%d/data.zip", problemID)
	ctx := c.Request.Context()

	if err := h.store.Put(ctx, storage.BucketTestcases, key, f, fh.Size, "application/zip"); err != nil {
		h.log.Error("upload testcase zip", zap.Error(err),
			zap.Int64("problem_id", problemID), zap.String("key", key))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage upload failed"})
		return
	}

	// ── Validate the zip contents by scanning the uploaded object ─────────────
	// We re-open from MinIO for listing so we don't buffer the whole file in RAM.
	rc, err := h.store.Get(ctx, storage.BucketTestcases, key)
	if err != nil {
		// Upload succeeded but we can't verify — that's OK; just skip the listing.
		c.JSON(http.StatusCreated, gin.H{
			"key":     key,
			"size":    fh.Size,
			"warning": "uploaded but could not list zip contents",
		})
		return
	}
	defer rc.Close()

	files, warns := listZipContents(rc, fh.Size)
	c.JSON(http.StatusCreated, gin.H{
		"key":      key,
		"size":     fh.Size,
		"files":    files,
		"warnings": warns,
	})
}

// listZipContents reads rc into memory (capped at 32 MB), then lists filenames
// in the zip and warns about missing .out counterparts.
// Large zips skip the listing and return a warning instead.
func listZipContents(rc io.Reader, size int64) (files []string, warns []string) {
	const listSizeLimit = 32 * 1024 * 1024 // 32 MB in-memory cap for listing
	if size > listSizeLimit {
		return nil, []string{"zip content listing skipped (file > 32 MB); upload succeeded"}
	}

	buf, err := io.ReadAll(io.LimitReader(rc, listSizeLimit+1))
	if err != nil {
		return nil, []string{fmt.Sprintf("read zip for listing: %v", err)}
	}

	// bytes.NewReader implements io.ReaderAt, which zip.NewReader requires.
	r, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, []string{fmt.Sprintf("zip parse error: %v", err)}
	}

	outSet := make(map[string]bool)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		files = append(files, f.Name)
		if strings.HasSuffix(f.Name, ".out") {
			outSet[strings.TrimSuffix(f.Name, ".out")] = true
		}
	}

	// Warn about .in files with no matching .out (OK for interactive problems).
	for _, fname := range files {
		if strings.HasSuffix(fname, ".in") {
			base := strings.TrimSuffix(fname, ".in")
			if !outSet[base] {
				warns = append(warns, fmt.Sprintf(
					"no matching .out for %s.in (expected for standard/special; OK for interactive)", fname))
			}
		}
	}
	if len(files) == 0 {
		warns = append(warns, "zip is empty — no test cases found")
	}
	return files, warns
}
