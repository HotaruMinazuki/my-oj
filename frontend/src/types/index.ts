// ─── Shared domain types ─────────────────────────────────────────────────────
// Mirrors the Go structs in internal/models/*.go.

export type ID = number

export type UserRole = 'admin' | 'contestant' | 'guest'

export interface User {
  id: ID
  username: string
  // Optional: an account may have no bound email (null/undefined when unbound).
  email?: string | null
  role: UserRole
  organization?: string
  created_at?: string
  updated_at?: string
}

export type Language =
  | 'C' | 'C++17' | 'C++20' | 'Java21' | 'Python3' | 'Go' | 'Rust'

// 当前判题镜像实际可用的语言(与 configs/languages.yaml 一致)。
// Java21/Go/Rust 需先在 Dockerfile.judger 安装运行时后才能恢复。
export const ALL_LANGUAGES: Language[] = [
  'C++17', 'C++20', 'C', 'Python3',
]

export type JudgeType = 'standard' | 'special' | 'interactive' | 'communication'

export interface Problem {
  id: ID
  title: string
  statement?: string
  time_limit_ms: number
  mem_limit_kb: number
  judge_type: JudgeType
  allowed_langs?: Language[]
  is_public: boolean
  author_id: ID
  created_at: string
  updated_at: string
}

export type ContestType = 'ICPC' | 'OI' | 'IOI' | 'Team' | 'Custom'
export type ContestStatus = 'draft' | 'ready' | 'running' | 'frozen' | 'ended'

export interface Contest {
  id: ID
  title: string
  description?: string
  contest_type: ContestType
  status: ContestStatus
  start_time: string
  end_time: string
  freeze_time?: string | null
  is_public: boolean
  allow_late_register: boolean
  organizer_id: ID
  created_at: string
  updated_at: string
}

export interface ContestProblemSummary {
  problem_id: ID
  label: string
  title: string
  max_score: number
  ordinal: number
  time_limit_ms: number
  mem_limit_kb: number
}

export type SubmissionStatus =
  | 'Pending' | 'Compiling' | 'Judging'
  | 'Accepted' | 'WrongAnswer'
  | 'TimeLimitExceeded' | 'MemoryLimitExceeded'
  | 'RuntimeError' | 'CompileError' | 'SystemError'
  | 'Superseded' // OI: overridden by a later submission to the same problem; not judged

export const TERMINAL_STATUSES: SubmissionStatus[] = [
  'Accepted', 'WrongAnswer', 'TimeLimitExceeded', 'MemoryLimitExceeded',
  'RuntimeError', 'CompileError', 'SystemError', 'Superseded',
]

export interface TestCaseResult {
  test_case_id: ID
  group_id: number
  status: SubmissionStatus
  time_used_ms: number
  mem_used_kb: number
  score: number
  checker_output?: string
}

export interface Submission {
  id: ID
  user_id: ID
  problem_id: ID
  contest_id?: ID | null
  // Response-only hint set by the detail endpoint; ICPC → hide per-submission score.
  contest_type?: ContestType
  language: Language
  status: SubmissionStatus
  score: number
  time_used_ms: number
  mem_used_kb: number
  compile_log?: string
  judge_message?: string
  test_case_results?: TestCaseResult[]
  judge_node_id?: string
  created_at: string
  updated_at: string
}

// ─── User profile (公开主页) ──────────────────────────────────────────────────

// UserPublic is the public view of a user. `email` is private and only present
// in the response when the requester is the account owner or an admin.
export interface UserPublic {
  id: ID
  username: string
  email?: string | null
  role: UserRole
  organization?: string
  created_at: string
}

export interface UserSubmissionStats {
  total: number
  accepted: number
  solved: number
}

// SubmissionListItem is the lightweight row used by history listings
// (per-user history and the admin global list). No compile log / testcase data.
export interface SubmissionListItem {
  id: ID
  user_id: ID
  username: string
  problem_id: ID
  problem_title: string
  contest_id?: ID | null
  language: Language
  status: SubmissionStatus
  score: number
  time_used_ms: number
  mem_used_kb: number
  created_at: string
}

// ─── Ranking (WebSocket payloads) ────────────────────────────────────────────
export interface RankingProblemCell {
  solved: boolean
  attempts: number
  pending: number
  penalty: number
  score: number        // OI/IOI: points earned on this problem
  first_blood?: boolean
}

export interface RankingRow {
  rank: number
  user_id: ID
  username: string
  organization?: string
  problems: Record<string, RankingProblemCell>
  total_solved: number
  total_penalty: number
  total_score: number  // OI/IOI: total points
}

export interface RankingSnapshot {
  contestants: RankingRow[]
  problems: string[]
  frozen: boolean
  // contest_type drives rendering: ICPC → solved/penalty; OI/IOI → score-only.
  contest_type?: ContestType
}

// ─── API envelopes ───────────────────────────────────────────────────────────
export interface Paginated<T> {
  problems?: T[]
  contests?: T[]
  total: number
  page: number
  size: number
}

export interface AuthResponse {
  token: string
  user: User
}

// ─── UI helpers ──────────────────────────────────────────────────────────────
export type ElTagType = 'success' | 'warning' | 'info' | 'danger' | 'primary' | ''

export function statusTagType(s: SubmissionStatus): ElTagType {
  if (s === 'Accepted') return 'success'
  if (s === 'Pending' || s === 'Judging' || s === 'Compiling') return 'warning'
  if (s === 'Superseded') return 'info'
  return 'danger'
}

export function contestStatusTagType(s: ContestStatus): ElTagType {
  return ({
    running: 'success', frozen: 'warning', ended: 'info',
    ready: 'primary', draft: 'info',
  } as const)[s] ?? ''
}

export function contestStatusLabel(s: ContestStatus): string {
  return ({
    running: '进行中', frozen: '封榜中', ended: '已结束',
    ready: '即将开始', draft: '草稿',
  } as const)[s] ?? s
}

export function judgeTypeLabel(t: JudgeType): string {
  return ({
    standard: '标准', special: '特判', interactive: '交互', communication: '通信',
  } as const)[t] ?? t
}

export function judgeTypeTagType(t: JudgeType): ElTagType {
  return ({
    standard: '', special: 'warning', interactive: 'success', communication: 'danger',
  } as const)[t] ?? ''
}
