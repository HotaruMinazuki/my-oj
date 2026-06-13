package handler

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

const (
	maxUploadBytes   = 500 * 1024 * 1024 // 500 MB max zip upload
	maxZipValidBytes = 4 * 1024          // read this many bytes for magic-byte check
)

// zipMagic is the signature of a valid ZIP file.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// TestcaseAdminRepo persists test-case metadata parsed from an uploaded zip.
// Without these rows the API server builds JudgeTasks with zero test cases and
// every submission ends in SystemError, so the upload endpoint must keep the
// DB in sync with the zip stored in MinIO.
type TestcaseAdminRepo interface {
	ReplaceTestCases(ctx context.Context, problemID models.ID, cases []models.JudgeTestCase) error
}

// AdminHandler exposes privileged contest- and problem-management endpoints.
type AdminHandler struct {
	store     storage.ObjectStore
	testcases TestcaseAdminRepo
	log       *zap.Logger
}

func NewAdminHandler(
	store storage.ObjectStore,
	testcases TestcaseAdminRepo,
	log *zap.Logger,
) *AdminHandler {
	return &AdminHandler{store: store, testcases: testcases, log: log}
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
	if _, err := f.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "seek error"})
		return
	}

	// ── Parse entries BEFORE upload so a malformed zip is rejected outright ───
	// multipart.File implements io.ReaderAt, so no extra buffering is needed.
	zr, err := zip.NewReader(f, fh.Size)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("cannot parse zip: %v", err)})
		return
	}
	cases, files, warns, parseErr := parseTestcaseEntries(zr)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": parseErr.Error(), "files": files})
		return
	}

	if _, err := f.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "seek error"})
		return
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

	// ── Sync test-case rows into the DB ───────────────────────────────────────
	// The submission flow loads test cases from the DB to build the JudgeTask;
	// without these rows nothing would be judged.
	if err := h.testcases.ReplaceTestCases(ctx, problemID, cases); err != nil {
		h.log.Error("replace test cases", zap.Error(err), zap.Int64("problem_id", problemID))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "zip stored but registering test cases in DB failed; re-upload to retry",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key":        key,
		"size":       fh.Size,
		"files":      files,
		"test_cases": len(cases),
		"warnings":   warns,
	})
}

var (
	tcInRe  = regexp.MustCompile(`^(\d+)\.in$`)
	tcOutRe = regexp.MustCompile(`^(\d+)\.out$`)
)

// parseTestcaseEntries maps zip entries (flat layout: "1.in", "1.out", ...) to
// JudgeTestCase rows. Per-case score is an equal share of 100 (remainder goes
// to the last case) so OI/IOI scoring works without per-case configuration.
func parseTestcaseEntries(zr *zip.Reader) (cases []models.JudgeTestCase, files, warns []string, err error) {
	ins := map[int]string{}
	outs := map[int]string{}

	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := zf.Name
		files = append(files, name)
		if m := tcInRe.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			ins[n] = name
		} else if m := tcOutRe.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			outs[n] = name
		} else {
			warns = append(warns, fmt.Sprintf("ignored entry %q (expected N.in / N.out at zip root)", name))
		}
	}

	if len(ins) == 0 {
		return nil, files, warns, fmt.Errorf(
			"no N.in entries found at zip root; layout must be flat: 1.in 1.out 2.in 2.out ...")
	}

	nums := make([]int, 0, len(ins))
	for n := range ins {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	base, rem := 100/len(nums), 100%len(nums)
	for i, n := range nums {
		outPath := outs[n]
		if outPath == "" {
			warns = append(warns, fmt.Sprintf(
				"%d.in has no matching %d.out (required for standard/special judge; OK for interactive)", n, n))
		}
		score := base
		if i == len(nums)-1 {
			score += rem
		}
		cases = append(cases, models.JudgeTestCase{
			GroupID:    1,
			Ordinal:    n,
			InputPath:  ins[n],
			OutputPath: outPath,
			Score:      score,
		})
	}
	for n := range outs {
		if _, ok := ins[n]; !ok {
			warns = append(warns, fmt.Sprintf("%d.out has no matching %d.in; entry ignored", n, n))
		}
	}
	return cases, files, warns, nil
}
