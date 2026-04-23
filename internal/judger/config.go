package judger

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/your-org/my-oj/internal/models"
)

// LangConfig holds everything the Compiler and Orchestrator need for one language.
// These configs are loaded from configs/languages.yaml at startup — never hardcoded.
type LangConfig struct {
	Language models.Language `yaml:"language"`
	// SourceFile is the filename written into the sandbox work dir (e.g., "main.cpp").
	SourceFile string `yaml:"source_file"`
	// CompileCmd is the full compile invocation run inside the sandbox.
	// nil or empty means skip the compile step (useful for precompiled binaries in tests).
	CompileCmd []string `yaml:"compile_cmd"`
	// RunCmd is the full command used to execute the contestant's program.
	// RunCmd[0] is the executable; RunCmd[1:] are the fixed arguments.
	// Example: ["./main"] for C++, ["python3", "main.py"] for Python.
	RunCmd []string `yaml:"run_cmd"`
	// TimeLimitMultiplier scales the problem's time limit for this language.
	// C++ = 1.0, Java = 2.0, Python = 3.0 are typical values.
	TimeLimitMultiplier float64 `yaml:"time_limit_multiplier"`
}

// JudgerConfig holds operational settings for the judger node.
type JudgerConfig struct {
	// Workers is the number of concurrent judging goroutines.
	// Recommended: equal to the number of isolated CPU cores reserved for judging.
	Workers int `yaml:"workers"`
	// WorkBaseDir is the host directory under which per-task sandbox dirs are created.
	WorkBaseDir string `yaml:"work_base_dir"`
	// GlobalTimeoutSec is the hard wall-clock limit per task.
	// Tasks exceeding this are killed and reported as SystemError.
	// Should be significantly larger than any single problem's time limit.
	GlobalTimeoutSec int `yaml:"global_timeout_sec"`

	GlobalTimeout time.Duration // resolved from GlobalTimeoutSec after loading
}

// LanguagesFile is the top-level structure of configs/languages.yaml.
type LanguagesFile struct {
	Languages []LangConfig `yaml:"languages"`
}

// LoadLangConfigs reads and parses configs/languages.yaml.
func LoadLangConfigs(path string) ([]LangConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadLangConfigs: read %s: %w", path, err)
	}
	var f LanguagesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("LoadLangConfigs: parse yaml: %w", err)
	}
	for i, lc := range f.Languages {
		if len(lc.RunCmd) == 0 {
			return nil, fmt.Errorf("LoadLangConfigs: language %q missing run_cmd", lc.Language)
		}
		if lc.TimeLimitMultiplier == 0 {
			f.Languages[i].TimeLimitMultiplier = 1.0
		}
	}
	return f.Languages, nil
}
