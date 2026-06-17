package nsjail

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupOOMKilled(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "oom_kill positive",
			content: "low 0\nhigh 0\nmax 12\noom 3\noom_kill 1\n",
			want: true,
		},
		{
			name:    "oom_group_kill positive",
			content: "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\noom_group_kill 2\n",
			want:    true,
		},
		{
			name:    "no kill",
			content: "low 0\nhigh 0\nmax 5\noom 0\noom_kill 0\n",
			want:    false,
		},
		{
			name:    "high pressure but never killed",
			content: "low 0\nhigh 42\nmax 7\noom 0\noom_kill 0\noom_group_kill 0\n",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := cgroupOOMKilled(dir); got != tc.want {
				t.Fatalf("cgroupOOMKilled = %v, want %v", got, tc.want)
			}
		})
	}
}

// A missing memory.events must degrade to false, never panic — measurement may
// not break a run.
func TestCgroupOOMKilledMissingFile(t *testing.T) {
	if cgroupOOMKilled(t.TempDir()) {
		t.Fatal("expected false when memory.events is absent")
	}
}

func TestReadCgroupCPUMs(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int64
	}{
		{
			name:    "typical",
			content: "usage_usec 1500000\nuser_usec 1400000\nsystem_usec 100000\n",
			want:    1500, // 1.5 s
		},
		{
			name:    "usage not first line",
			content: "nr_periods 0\nnr_throttled 0\nusage_usec 250750\nuser_usec 250000\n",
			want:    250,
		},
		{
			name:    "zero usage",
			content: "usage_usec 0\nuser_usec 0\nsystem_usec 0\n",
			want:    0,
		},
		{
			name:    "no usage field",
			content: "user_usec 5000\nsystem_usec 5000\n",
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readCgroupCPUMs(dir); got != tc.want {
				t.Fatalf("readCgroupCPUMs = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReadCgroupCPUMsMissingFile(t *testing.T) {
	if got := readCgroupCPUMs(t.TempDir()); got != 0 {
		t.Fatalf("expected 0 when cpu.stat is absent, got %d", got)
	}
}
