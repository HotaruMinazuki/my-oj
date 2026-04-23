package models

// TestCase represents one input/output pair for a problem.
// File contents live on shared storage (NFS / MinIO); only paths are stored in DB.
type TestCase struct {
	ID        ID `db:"id"         json:"id"`
	ProblemID ID `db:"problem_id" json:"problem_id"`

	// GroupID enables IOI-style subtask grouping.
	// All test cases in the same group share the same subtask score pool.
	GroupID int `db:"group_id" json:"group_id"`
	// Ordinal controls evaluation order within a group (ascending).
	Ordinal int `db:"ordinal" json:"ordinal"`

	// Paths are absolute on the shared storage volume; never exposed to contestants.
	InputPath  string `db:"input_path"  json:"-"`
	OutputPath string `db:"output_path" json:"-"`

	// Score is the maximum points this test case can award (OI / IOI scoring).
	// For ICPC, all test cases effectively score 0 or full (handled by the Strategy).
	Score int `db:"score" json:"score"`

	// IsSample marks cases shown as examples in the problem statement.
	IsSample bool `db:"is_sample" json:"is_sample"`
}
