package judger

import (
	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
)

// VerdictParser interprets a checker/interactor's stderr into a structured Verdict.
// Defined here (not in the interactive sub-package) to avoid import cycles.
type VerdictParser func(checkerStderr string, contestant sandbox.ExecResult) *Verdict

// execStatusToSubmission maps a low-level sandbox ExecStatus to the user-facing
// SubmissionStatus. Only called when ExecStatus != ExecOK.
func execStatusToSubmission(s sandbox.ExecStatus) models.SubmissionStatus {
	switch s {
	case sandbox.ExecTLE, sandbox.ExecWallTLE:
		return models.StatusTLE
	case sandbox.ExecMLE:
		return models.StatusMLE
	case sandbox.ExecRE, sandbox.ExecSCViol:
		// Seccomp violation is surfaced as RE to contestants; logged internally.
		return models.StatusRE
	case sandbox.ExecSE:
		return models.StatusSE
	default:
		return models.StatusRE
	}
}

// aggregate computes the final submission status and total score from per-test-case
// results. This is the default aggregation used by the judger before the scoring
// Strategy (Module 3) applies contest-specific transformations.
//
// Priority order (highest wins): SE > TLE > MLE > RE > WA > AC.
func aggregate(results []models.TestCaseResult, jt models.JudgeType) (models.SubmissionStatus, int) {
	if len(results) == 0 {
		return models.StatusSE, 0
	}

	// For OI-style problems every test case is always run; sum up scores.
	// For ICPC-style problems score is binary (0 or 1 per problem, not per test case).
	isScored := jt == models.JudgeStandard || jt == models.JudgeSpecial ||
		jt == models.JudgeInteractive || jt == models.JudgeCommunication

	var totalScore int
	worst := models.StatusAccepted

	priority := func(s models.SubmissionStatus) int {
		switch s {
		case models.StatusSE:
			return 6
		case models.StatusTLE:
			return 5
		case models.StatusMLE:
			return 4
		case models.StatusRE:
			return 3
		case models.StatusWrongAnswer:
			return 2
		case models.StatusAccepted:
			return 1
		default:
			return 0
		}
	}

	for _, r := range results {
		if priority(r.Status) > priority(worst) {
			worst = r.Status
		}
		if isScored {
			totalScore += r.Score
		}
	}

	// If overall status is AC, award full accumulated score.
	// Otherwise score is still accumulated (for OI partial credit display).
	return worst, totalScore
}
