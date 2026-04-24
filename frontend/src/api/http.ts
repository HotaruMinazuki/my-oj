import axios, { AxiosError } from 'axios'
import { ElMessage } from 'element-plus'

import type {
  AuthResponse, Contest, ContestProblemSummary, Paginated,
  Problem, RankingSnapshot, Submission, User,
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

  register: (data: { username: string; email: string; password: string; organization?: string }) =>
    http.post<AuthResponse>('/auth/register', data).then(r => r.data),

  me: () =>
    http.get<User>('/auth/me').then(r => r.data),
}

// ─── Problem API ──────────────────────────────────────────────────────────────
export const problemApi = {
  list: (page = 1, size = 20) =>
    http.get<Paginated<Problem>>('/problems', { params: { page, size } }).then(r => r.data),

  get: (id: number) =>
    http.get<Problem>(`/problems/${id}`).then(r => r.data),

  create: (data: Partial<Problem>) =>
    http.post<Problem>('/admin/problems', data).then(r => r.data),

  uploadTestcases: (id: number, file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return http.post(`/admin/problems/${id}/testcases`, fd, {
      headers: { 'Content-Type': 'multipart/form-data' }
    }).then(r => r.data)
  },
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

  unfreezeNext: (id: number) =>
    http.post<{ done?: boolean }>(`/admin/contests/${id}/unfreeze-next`).then(r => r.data),
}

// ─── Submission API ───────────────────────────────────────────────────────────
export const submissionApi = {
  submit: (contestId: number, data: { problem_id: number; language: string; source: string }) =>
    http.post<Submission>(`/contests/${contestId}/submissions`, data).then(r => r.data),

  submitPractice: (data: { problem_id: number; language: string; source: string }) =>
    http.post<Submission>('/submissions', data).then(r => r.data),

  get: (id: number) =>
    http.get<Submission>(`/submissions/${id}`).then(r => r.data),
}
