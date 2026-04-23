package judger

import (
	"archive/zip"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

// ─── Key/path conventions ─────────────────────────────────────────────────────

const (
	// etagFile is written inside each problem's local dir after a successful
	// extraction.  Its content is the MinIO ETag of the zip that was extracted.
	etagFile = ".etag"

	// etagTTL is how long we trust a cached ETag without re-validating against
	// MinIO.  Keeps the hot path allocation-free while bounding staleness.
	etagTTL = 5 * time.Minute

	// maxZipEntryBytes is the per-file decompression cap — protects against
	// zip-bomb payloads that decompress to gigabytes.
	maxZipEntryBytes = 512 * 1024 * 1024 // 512 MB per entry
)

// TestcaseZipKey returns the MinIO object key for a problem's testcase zip.
// Layout in bucket "testcases": testcases/{problemID}/data.zip
func TestcaseZipKey(problemID models.ID) string {
	return fmt.Sprintf("testcases/%d/data.zip", problemID)
}

// testcaseLocalDir returns the absolute path of the extracted testcase dir
// on the judger node.
func testcaseLocalDir(baseDir string, problemID models.ID) string {
	return filepath.Join(baseDir, strconv.FormatInt(int64(problemID), 10))
}

// ─── LRU cache ────────────────────────────────────────────────────────────────

// TestcaseCache manages per-problem testcase directories on the judger node.
//
// # Invariants
//
//   - At most maxEntries problem dirs exist on disk simultaneously.
//   - The least-recently-used entry is evicted when the cache is full.
//   - Within etagTTL of a successful validation, the local dir is returned
//     immediately without any network call.
//   - If the remote ETag changes, the zip is re-downloaded and re-extracted
//     atomically (tmp dir → rename) before the old dir is deleted.
//   - Concurrent callers requesting the same problemID coalesce: only one
//     download goroutine runs; others wait for its result.
//
// # Disk protection
//
//   - maxEntries bounds the number of cached problem dirs.
//   - Each zip entry is capped at maxZipEntryBytes (zip-bomb guard).
//   - Call Prune() once at startup to evict stale dirs from a previous run.
type TestcaseCache struct {
	baseDir    string // e.g. /tmp/oj-judge/testcases
	maxEntries int
	store      storage.ObjectStore
	log        *zap.Logger

	mu         sync.Mutex
	lruList    *list.List               // front = MRU, back = LRU; values are *tcEntry
	index      map[models.ID]*list.Element
	inProgress map[models.ID]*dlSlot    // in-flight downloads, keyed by problemID
}

type tcEntry struct {
	problemID   models.ID
	localDir    string
	etag        string    // ETag of the extracted zip (from .etag file)
	validatedAt time.Time // when we last confirmed etag == remote ETag
}

// dlSlot lets concurrent callers for the same problem wait for one download.
type dlSlot struct {
	done chan struct{} // closed when download completes
	dir  string
	err  error
}

// NewTestcaseCache creates a cache.  Call Prune() once before the scheduler starts.
func NewTestcaseCache(
	baseDir string,
	maxEntries int,
	store storage.ObjectStore,
	log *zap.Logger,
) *TestcaseCache {
	if maxEntries <= 0 {
		maxEntries = 200
	}
	return &TestcaseCache{
		baseDir:    baseDir,
		maxEntries: maxEntries,
		store:      store,
		log:        log,
		lruList:    list.New(),
		index:      make(map[models.ID]*list.Element),
		inProgress: make(map[models.ID]*dlSlot),
	}
}

// EnsureTestcases guarantees the testcase files for problemID are present and
// current on local disk.  Returns the absolute path to the extracted directory.
//
// Callers must resolve JudgeTestCase.InputPath / OutputPath relative to this dir:
//
//	localDir, err := cache.EnsureTestcases(ctx, task.ProblemID)
//	tc.InputPath = filepath.Join(localDir, tc.InputPath)
func (c *TestcaseCache) EnsureTestcases(ctx context.Context, problemID models.ID) (string, error) {
	// ── Fast path: recently validated, no network call needed ─────────────────
	c.mu.Lock()
	if elem, ok := c.index[problemID]; ok {
		e := elem.Value.(*tcEntry)
		if time.Since(e.validatedAt) < etagTTL {
			c.lruList.MoveToFront(elem)
			dir := e.localDir
			c.mu.Unlock()
			return dir, nil
		}
	}

	// ── Coalesce concurrent downloads for the same problem ────────────────────
	// If another worker is already refreshing this problem, wait for it.
	if slot, ok := c.inProgress[problemID]; ok {
		c.mu.Unlock()
		<-slot.done
		return slot.dir, slot.err
	}

	// We are the designated downloader for this problem.
	slot := &dlSlot{done: make(chan struct{})}
	c.inProgress[problemID] = slot
	c.mu.Unlock()

	// ── Validate and optionally re-download (no lock held — network I/O) ──────
	dir, err := c.refresh(ctx, problemID)

	// Broadcast result to any waiters, then update the LRU.
	c.mu.Lock()
	slot.dir, slot.err = dir, err
	delete(c.inProgress, problemID)
	if err == nil {
		c.promoteEntry(problemID, dir)
	}
	c.mu.Unlock()
	close(slot.done) // unblocks all waiting goroutines

	return dir, err
}

// refresh validates the remote ETag against the local copy, and re-downloads
// the zip only when they differ.
func (c *TestcaseCache) refresh(ctx context.Context, problemID models.ID) (string, error) {
	localDir := testcaseLocalDir(c.baseDir, problemID)
	key := TestcaseZipKey(problemID)

	// Stat the remote object to get its ETag.  A single HEAD request — cheap.
	info, err := c.store.Stat(ctx, storage.BucketTestcases, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("no testcase zip for problem %d (upload it first)", problemID)
		}
		return "", fmt.Errorf("stat testcase zip: %w", err)
	}
	remoteETag := info.ETag

	// Compare with locally stored ETag.
	localETag := readETagFile(localDir)
	if localETag != "" && localETag == remoteETag {
		// Local copy is current.  Refresh the in-memory TTL.
		c.mu.Lock()
		if elem, ok := c.index[problemID]; ok {
			elem.Value.(*tcEntry).validatedAt = time.Now()
			c.lruList.MoveToFront(elem)
		}
		c.mu.Unlock()
		c.log.Debug("testcase cache: ETag match, using local copy",
			zap.Int64("problem_id", problemID), zap.String("etag", remoteETag))
		return localDir, nil
	}

	// ETag mismatch (or first download) — fetch and extract the zip.
	c.log.Info("testcase cache: refreshing from MinIO",
		zap.Int64("problem_id", problemID),
		zap.String("remote_etag", remoteETag),
		zap.String("local_etag", localETag),
	)
	if err := c.downloadAndExtract(ctx, problemID, key, localDir, remoteETag); err != nil {
		return "", err
	}
	return localDir, nil
}

// downloadAndExtract downloads the testcase zip from MinIO and extracts it
// atomically into localDir.
//
// Atomic protocol:
//  1. Download zip → tmpZip  (in baseDir, so same filesystem as localDir)
//  2. Extract      → tmpDir  (localDir + ".tmp")
//  3. Write .etag  → tmpDir/.etag
//  4. RemoveAll    → localDir
//  5. Rename       → tmpDir → localDir
//
// If any step fails, tmps are cleaned up and localDir is left untouched (the
// old extraction, if present, remains valid).
func (c *TestcaseCache) downloadAndExtract(
	ctx context.Context,
	problemID models.ID,
	key, localDir, etag string,
) error {
	if err := os.MkdirAll(c.baseDir, 0o755); err != nil {
		return fmt.Errorf("mkdir testcase base dir: %w", err)
	}

	// ── Step 1: download to a temp file ───────────────────────────────────────
	tmpZip := filepath.Join(c.baseDir, fmt.Sprintf(".dl-%d.zip.tmp", problemID))
	defer os.Remove(tmpZip)

	if err := c.store.GetToFile(ctx, storage.BucketTestcases, key, tmpZip); err != nil {
		return fmt.Errorf("download testcase zip (problem %d): %w", problemID, err)
	}

	// ── Step 2: extract into a staging dir ────────────────────────────────────
	tmpDir := localDir + ".tmp"
	os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir staging dir: %w", err)
	}

	if err := extractZip(tmpZip, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("extract testcase zip (problem %d): %w", problemID, err)
	}

	// ── Step 3: write ETag into the staging dir ───────────────────────────────
	if err := os.WriteFile(filepath.Join(tmpDir, etagFile), []byte(etag), 0o644); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("write .etag file: %w", err)
	}

	// ── Step 4 + 5: atomically replace the old dir ────────────────────────────
	os.RemoveAll(localDir)
	if err := os.Rename(tmpDir, localDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("rename staging dir → local dir: %w", err)
	}

	c.log.Info("testcase cache: extracted",
		zap.Int64("problem_id", problemID),
		zap.String("dir", localDir),
		zap.String("etag", etag),
	)
	return nil
}

// ─── LRU management (all called with c.mu held) ───────────────────────────────

// promoteEntry adds or refreshes a cache entry, evicting LRU entries if needed.
func (c *TestcaseCache) promoteEntry(problemID models.ID, localDir string) {
	etag := readETagFile(localDir)
	if elem, ok := c.index[problemID]; ok {
		e := elem.Value.(*tcEntry)
		e.etag = etag
		e.validatedAt = time.Now()
		c.lruList.MoveToFront(elem)
		return
	}
	elem := c.lruList.PushFront(&tcEntry{
		problemID:   problemID,
		localDir:    localDir,
		etag:        etag,
		validatedAt: time.Now(),
	})
	c.index[problemID] = elem
	c.evictIfNeeded()
}

// evictIfNeeded removes LRU entries until the list is within maxEntries.
// Disk removal is done in a goroutine so the lock is not held during I/O.
func (c *TestcaseCache) evictIfNeeded() {
	for c.lruList.Len() > c.maxEntries {
		back := c.lruList.Back()
		if back == nil {
			return
		}
		e := c.lruList.Remove(back).(*tcEntry)
		delete(c.index, e.problemID)

		go func(dir string, pid models.ID) {
			if err := os.RemoveAll(dir); err != nil {
				c.log.Warn("testcase cache: evict failed",
					zap.Int64("problem_id", pid), zap.Error(err))
			} else {
				c.log.Info("testcase cache: evicted LRU entry",
					zap.Int64("problem_id", pid))
			}
		}(e.localDir, e.problemID)
	}
}

// ─── Startup pruning ──────────────────────────────────────────────────────────

// Prune scans baseDir, loads existing problem dirs into the LRU (newest first),
// and evicts dirs beyond maxEntries.  Call once before the scheduler starts.
//
// This cleans up leftover dirs from previous judger runs that exited abruptly,
// bounding disk usage across restarts.
func (c *TestcaseCache) Prune() {
	entries, err := os.ReadDir(c.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return // nothing to prune
		}
		c.log.Warn("testcase cache: prune: readdir failed", zap.Error(err))
		return
	}

	type dirEntry struct {
		problemID models.ID
		mtime     time.Time
		path      string
	}

	var dirs []dirEntry
	for _, de := range entries {
		// Skip hidden files (our own temp files) and non-directories.
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		id, err := strconv.ParseInt(de.Name(), 10, 64)
		if err != nil {
			continue // not a problem dir
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, dirEntry{
			problemID: models.ID(id),
			mtime:     fi.ModTime(),
			path:      filepath.Join(c.baseDir, de.Name()),
		})
	}

	// Sort newest-first so we keep the most recently used entries.
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].mtime.After(dirs[j].mtime)
	})

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, d := range dirs {
		etag := readETagFile(d.path)
		// Push to back: since we iterate newest-first the list is MRU→LRU.
		elem := c.lruList.PushBack(&tcEntry{
			problemID:   d.problemID,
			localDir:    d.path,
			etag:        etag,
			validatedAt: time.Time{}, // zero → will re-validate on first use
		})
		c.index[d.problemID] = elem
	}

	// Evict oldest dirs that exceed the limit (deletes asynchronously).
	c.evictIfNeeded()

	c.log.Info("testcase cache: startup prune complete",
		zap.Int("found", len(dirs)),
		zap.Int("kept", c.lruList.Len()),
		zap.Int("max", c.maxEntries),
	)
}

// ─── Zip extraction ───────────────────────────────────────────────────────────

// extractZip extracts all regular files from zipPath into destDir.
// Directory entries are created implicitly.  Symlinks are skipped.
//
// Security: zip-slip protection rejects any entry whose resolved path escapes
// destDir.  Each entry is capped at maxZipEntryBytes to resist zip bombs.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	destDir = filepath.Clean(destDir) + string(os.PathSeparator)

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue // created on demand when we mkdir for files
		}

		// Zip-slip guard: reject paths that walk outside destDir.
		destPath := filepath.Join(filepath.Clean(destDir), filepath.FromSlash(f.Name))
		if !strings.HasPrefix(destPath, destDir) {
			return fmt.Errorf("zip slip rejected: %q escapes destination", f.Name)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("mkdir for %q: %w", f.Name, err)
		}
		if err := extractZipEntry(f, destPath); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %q: %w", destPath, err)
	}
	defer out.Close()

	// Cap at maxZipEntryBytes; io.LimitReader makes reads beyond the cap return 0.
	// Any entry legitimately larger than 512 MB is a problem-setter error.
	n, err := io.Copy(out, io.LimitReader(rc, maxZipEntryBytes))
	if err != nil {
		return fmt.Errorf("extract %q: %w", destPath, err)
	}
	if n == maxZipEntryBytes {
		return fmt.Errorf("zip entry %q exceeds %d MB limit", f.Name, maxZipEntryBytes>>20)
	}
	return nil
}

// readETagFile reads the ETag from a dir's .etag file.
// Returns "" if the file is missing or unreadable.
func readETagFile(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, etagFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
