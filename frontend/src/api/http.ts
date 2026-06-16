import axios, { AxiosError } from 'axios'
import { ElMessage } from 'element-plus'

import type {
  AuthResponse, Contest, ContestProblemSummary, Paginated,
  Problem, RankingSnapshot, Submission, SubmissionListItem,
  User, UserPublic, UserSubmissionStats,
} from '@/types'

// ─── Axios instance ───────────────────────────────────────────────────────────
const http = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
})

http.interceptors.request.use(config => {
  const token = localStorage.getItem('oj_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

http.interceptors.response.use(
  res => res,
  (err: AxiosError<{ error?: string }>) => {
    const status = err.response?.status
    const msg = err.response?.data?.error || err.message || '网络错误'

    if (status === 401) {
      localStorage.removeItem('oj_token')
      localStorage.removeItem('oj_user')
      // 避免在登录页再跳转
      if (!location.pathname.startsWith('/login')) {
        ElMessage.warning('登录已过期，请重新登录')
        location.href = '/login'
      }
    } else if (status && status >= 500) {
      ElMessage.error(`服务器错误 (${status}): ${msg}`)
    } else if (status && status !== 404) {
      ElMessage.error(msg)
    }
    return Promise.reject(err)
  }
)

export default http

// ─── Auth API ─────────────────────────────────────────────────────────────────
export const authApi = {
  login: (username: string, password: string) =>
    http.post<AuthResponse>('/auth/login', { username, password }).then(r => r.data),

  register: (data: { username: string; email?: string; password: string; organization?: string }) =>
    http.post<AuthResponse>('/auth/register', data).then(r => r.data),

  me: () =>
    http.get<User>('/auth/me').then(r => r.data),

  // 邮箱验证码找回密码 (登录页 / 修改信息均可用, 无需登录)。
  requestPasswordReset: (identifier: string) =>
    http.post<{ message: string; email: string; smtp_enabled: boolean }>(
      '/auth/password-reset/request', { identifier }).then(r => r.data),

  confirmPasswordReset: (data: { identifier: string; code: string; new_password: string }) =>
    http.post('/auth/password-reset/confirm', data).then(r => r.data),
}

// ─── Problem API ──────────────────────────────────────────────────────────────
export const problemApi = {
  list: (page = 1, size = 20) =>
    http.get<Paginated<Problem>>('/problems', { params: { page, size } }).then(r => r.data),

  get: (id: number) =>
    http.get<Problem>(`/problems/${id}`).then(r => r.data),

  create: (data: Partial<Problem>) =>
    http.post<Problem>('/admin/problems', data).then(r => r.data),

  update: (id: number, data: Partial<Problem>) =>
    http.put(`/admin/problems/${id}`, data).then(r => r.data),

  remove: (id: number) =>
    http.delete(`/admin/problems/${id}`).then(r => r.data),

  // List the test cases currently registered for a problem (admin view).
  getTestcases: (id: number) =>
    http.get<{ test_cases: TestCaseInfo[]; count: number }>(`/admin/problems/${id}/testcases`).then(r => r.data),

  uploadTestcases: (id: number, file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return http.post<{ test_cases: number; files: string[]; warnings: string[] }>(
      `/admin/problems/${id}/testcases`, fd, {
        headers: { 'Content-Type': 'multipart/form-data' }
      }).then(r => r.data)
  },
}

export interface TestCaseInfo {
  test_case_id: number
  group_id: number
  ordinal: number
  input_path: string
  output_path?: string
  score: number
}

// ─── Contest API ──────────────────────────────────────────────────────────────
export const contestApi = {
  list: (page = 1, size = 20) =>
    http.get<Paginated<Contest>>('/contests', { params: { page, size } }).then(r => r.data),

  get: (id: number) =>
    http.get<{ contest: Contest; registered: boolean }>(`/contests/${id}`).then(r => r.data),

  getProblems: (id: number) =>
    http.get<{ problems: ContestProblemSummary[] }>(`/contests/${id}/problems`).then(r => r.data),

  getRanking: (id: number) =>
    http.get<RankingSnapshot>(`/contests/${id}/ranking`).then(r => r.data),

  register: (id: number) =>
    http.post(`/contests/${id}/register`).then(r => r.data),

  create: (data: Partial<Contest>) =>
    http.post<Contest>('/admin/contests', data).then(r => r.data),

  addProblem: (contestId: number, data: { problem_id: number; label: string; max_score?: number; ordinal?: number }) =>
    http.post(`/admin/contests/${contestId}/problems`, data).then(r => r.data),

  // Create a brand-new problem inside the contest (hidden until the contest ends).
  createProblem: (contestId: number, data: {
    label: string; title: string; statement?: string
    judge_type?: string; time_limit_ms?: number; mem_limit_kb?: number; max_score?: number
  }) =>
    http.post<{ problem_id: number }>(`/admin/contests/${contestId}/problems`, data).then(r => r.data),

  removeProblem: (contestId: number, problemId: number) =>
    http.delete(`/admin/contests/${contestId}/problems/${problemId}`).then(r => r.data),

  remove: (id: number) =>
    http.delete(`/admin/contests/${id}`).then(r => r.data),
}

// ─── Submission API ───────────────────────────────────────────────────────────
export const submissionApi = {
  // Submit endpoints return a compact ack: { id, status } (not the full Submission).
  submit: (contestId: number, data: { problem_id: number; language: string; source_code: string }) =>
    http.post<{ id: number; status: string }>(`/contests/${contestId}/submissions`, data).then(r => r.data),

  submitPractice: (data: { problem_id: number; language: string; source_code: string }) =>
    http.post<{ id: number; status: string }>('/submissions', data).then(r => r.data),

  get: (id: number) =>
    http.get<Submission>(`/submissions/${id}`).then(r => r.data),
}

// ─── User API (公开主页: 所有记录公开) ────────────────────────────────────────
export const userApi = {
  profile: (id: number) =>
    http.get<{ user: UserPublic; stats: UserSubmissionStats }>(`/users/${id}`).then(r => r.data),

  submissions: (id: number, page = 1, size = 20) =>
    http.get<{ submissions: SubmissionListItem[]; total: number }>(
      `/users/${id}/submissions`, { params: { page, size } }).then(r => r.data),

  contests: (id: number) =>
    http.get<{ contests: Contest[] }>(`/users/${id}/contests`).then(r => r.data),

  // Edit own profile (organization / 学校单位). `email` is optional and binds an
  // email when the account has none (one-shot — cannot change an existing one).
  updateMe: (data: { organization: string; email?: string }) =>
    http.put<{ organization: string; email?: string | null }>('/users/me', data).then(r => r.data),

  // Change own password (server verifies the current password).
  changePassword: (data: { old_password: string; new_password: string }) =>
    http.put('/users/me/password', data).then(r => r.data),
}

// ─── Admin API (用户管理 / 全部提交) ──────────────────────────────────────────
export const adminApi = {
  searchUsers: (q = '', page = 1, size = 20) =>
    http.get<{ users: User[]; total: number }>(
      '/admin/users', { params: { q, page, size } }).then(r => r.data),

  listSubmissions: (params: {
    page?: number; size?: number
    user_id?: number; problem_id?: number; contest_id?: number; status?: string
  } = {}) =>
    http.get<{ submissions: SubmissionListItem[]; total: number }>(
      '/admin/submissions', { params }).then(r => r.data),

  // 解榜: reveal a contest's frozen scoreboard after it ends.
  revealContest: (contestId: number) =>
    http.post(`/admin/contests/${contestId}/reveal`).then(r => r.data),

  // 赛后评测 (OI 挂机模式): batch-judge every withheld submission once the contest
  // has ended. Results then stream onto the scoreboard.
  judgeContest: (contestId: number) =>
    http.post<{ message: string; enqueued: number; skipped: number; total: number }>(
      `/admin/contests/${contestId}/judge`).then(r => r.data),

  // Download the resolver (滚榜) event-feed XML for a contest. Uses the axios
  // instance so the admin JWT is attached, then triggers a browser download.
  exportResolverXml: async (contestId: number) => {
    const res = await http.get(`/admin/contests/${contestId}/resolver.xml`, { responseType: 'blob' })
    const url = URL.createObjectURL(res.data as Blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `contest-${contestId}-event-feed.xml`
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  },
}
