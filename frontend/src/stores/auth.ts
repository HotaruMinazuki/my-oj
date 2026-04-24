import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi } from '@/api/http'
import type { User } from '@/types'

export const useAuthStore = defineStore('auth', () => {
  const token = ref<string | null>(null)
  const user  = ref<User | null>(null)

  const isLoggedIn = computed(() => !!token.value)
  const isAdmin    = computed(() => user.value?.role === 'admin')

  function setSession(t: string, u: User) {
    token.value = t
    user.value  = u
    localStorage.setItem('oj_token', t)
    localStorage.setItem('oj_user', JSON.stringify(u))
  }

  function restoreSession() {
    const t = localStorage.getItem('oj_token')
    const u = localStorage.getItem('oj_user')
    if (t && u) {
      try {
        token.value = t
        user.value  = JSON.parse(u) as User
      } catch {
        localStorage.removeItem('oj_token')
        localStorage.removeItem('oj_user')
      }
    }
  }

  function logout() {
    token.value = null
    user.value  = null
    localStorage.removeItem('oj_token')
    localStorage.removeItem('oj_user')
  }

  async function login(username: string, password: string) {
    const res = await authApi.login(username, password)
    setSession(res.token, res.user)
    return res.user
  }

  async function register(data: { username: string; email: string; password: string; organization?: string }) {
    const res = await authApi.register(data)
    setSession(res.token, res.user)
    return res.user
  }

  return { token, user, isLoggedIn, isAdmin, setSession, restoreSession, logout, login, register }
})
