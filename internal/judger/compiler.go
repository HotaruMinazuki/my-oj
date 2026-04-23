package judger

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

const (
	compileWallTimeLimitMs = 30_000     // 30 s; generous for Java/Kotlin
	compileMemLimitKB      = 512 * 1024 // 512 MB
	compileMaxLogBytes     = 64 * 1024  // 64 KB; truncate compiler noise beyond this
)

// CompileResult is the outcome of the compile stage.
type CompileResult struct {
	// Success is true when the compiler exited 0 and produced a runnable artifact.
	Success bool
	// RunCmd is the command used to execute the compiled artifact inside the sandbox.
	RunCmd []string
	// Log is the compiler's combined stdout+stderr, truncated to compileMaxLogBytes.
	Log string
}

// Compiler performs language-agnostic source download + compilation inside a sandbox.
type Compiler struct {
	configs map[models.Language]LangConfig
	store   storage.ObjectStore
}

// NewCompiler creates a Compiler.
// store is used to download source code from object storage before compiling.
func NewCompiler(cfgs []LangConfig, store storage.ObjectStore) *Compiler {
	m := make(map[models.Language]LangConfig, len(cfgs))
	for _, c := range cfgs {
		m[c.Language] = c
	}
	return &Compiler{configs: m, store: store}
}

// Compile is Stage 1 of the judge pipeline.
//
//  1. Downloads the source from MinIO (BucketSubmissions, key = sourceKey)
//     into workDir under the language-specific filename (e.g. "main.cpp").
//  2. For interpreted languages with no compile step, returns immediately.
//  3. Runs the compiler binary inside the sandbox session.
//  4. Returns a CompileResult with the run command on success.
//
// The sandbox session is NOT released here; the caller owns its lifetime.
// workDir must already exist; the sandbox must be mounted there.
func (c *Compiler) Compile(
	ctx context.Context,
	lang models.Language,
	sourceKey string, // MinIO object key in BucketSubmissions
	workDir string,
	session sandbox.Session,
) (*CompileResult, error) {
	cfg, ok := c.configs[lang]
	if !ok {
		return nil, fmt.Errorf("compiler: unsupported language %q", lang)
	}

	// ── Step 1: Download source from MinIO into the sandbox work dir ──────────
	// The destination filename is language-specific (e.g. "main.cpp", "Main.java").
	// The compiler binary expects to find it under this exact name.
	destPath := filepath.Join(workDir, cfg.SourceFile)
	if err := c.store.GetToFile(ctx, storage.BucketSubmissions, sourceKey, destPath); err != nil {
		return nil, fmt.Errorf("compiler: download source %q: %w", sourceKey, err)
	}

	// ── Step 2: Skip compile for interpreted / syntax-check-only languages ─────
	// Python, for example, has CompileCmd = ["python3", "-m", "py_compile", "main.py"]
	// which is only a syntax check; RunCmd is ["python3", "-u", "main.py"].
	// Languages with CompileCmd == nil (none currently) skip even the syntax check.
	if len(cfg.CompileCmd) == 0 {
		return &CompileResult{Success: true, RunCmd: cfg.RunCmd}, nil
	}

	// ── Step 3: Run compiler inside the sandbox ───────────────────────────────
	compileCtx, cancel := context.WithTimeout(ctx, time.Duration(compileWallTimeLimitMs)*time.Millisecond)
	defer cancel()

	var logBuf bytes.Buffer

	result, err := session.Execute(compileCtx, &sandbox.ExecRequest{
		Executable: cfg.CompileCmd[0],
		Args:       cfg.CompileCmd[1:],
		// Merge stdout+stderr so the contestant sees everything in one log.
		Stdout: &logBuf,
		Stderr: &logBuf,
		Limits: sandbox.ResourceLimits{
			// No CPU time limit for compilation — wall time is the cap.
			// javac/g++ can spend CPU in bursts; enforcing CPU limit causes false CEs.
			WallTimeLimitMs:   compileWallTimeLimitMs,
			MemLimitKB:        compileMemLimitKB,
			MaxOpenFiles:      512,
			MaxChildProcesses: 64, // compilers fork heavily (preprocessor, linker, asm)
		},
	})

	log := logBuf.String()
	if len(log) > compileMaxLogBytes {
		log = log[:compileMaxLogBytes] + "\n...(output truncated)"
	}

	if err != nil {
		// Sandbox-level failure during compilation — treat as SE, not CE.
		return &CompileResult{Success: false, Log: log},
			fmt.Errorf("compiler: sandbox error: %w", err)
	}

	if result.ExitCode != 0 || result.Status != sandbox.ExecOK {
		// Non-zero exit = genuine compile error (CE).
		return &CompileResult{Success: false, Log: log}, nil
	}

	return &CompileResult{Success: true, RunCmd: cfg.RunCmd, Log: log}, nil
}
