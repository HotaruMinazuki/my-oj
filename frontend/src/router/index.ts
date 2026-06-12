import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: () => import('@/components/AppLayout.vue'),
      children: [
        { path: '', name: 'home',              component: () => import('@/views/HomeView.vue') },
        { path: 'problems',    name: 'problems',    component: () => import('@/views/ProblemsView.vue') },
        { path: 'problems/:id', name: 'problem',   component: () => import('@/views/ProblemDetailView.vue') },
        { path: 'contests',    name: 'contests',    component: () => import('@/views/ContestsView.vue') },
        { path: 'contests/:id', name: 'contest',   component: () => import('@/views/ContestDetailView.vue') },
        { path: 'contests/:id/ranking', name: 'ranking', component: () => import('@/views/ContestRankingView.vue') },
        // 所有记录公开: 提交详情、用户主页均无需登录
        { path: 'submissions/:id', name: 'submission', component: () => import('@/views/SubmissionDetailView.vue') },
        { path: 'users/:id', name: 'user-profile', component: () => import('@/views/UserProfileView.vue') },
        {
          path: 'admin',
          component: () => import('@/views/admin/AdminLayout.vue'),
          meta: { requiresAuth: true, requiresAdmin: true },
          children: [
            { path: '', name: 'admin',                   component: () => import('@/views/admin/AdminDashboard.vue') },
            { path: 'problems',  name: 'admin-problems', component: () => import('@/views/admin/AdminProblems.vue') },
            { path: 'contests',  name: 'admin-contests', component: () => import('@/views/admin/AdminContests.vue') },
            { path: 'users',       name: 'admin-users',       component: () => import('@/views/admin/AdminUsers.vue') },
            { path: 'submissions', name: 'admin-submissions', component: () => import('@/views/admin/AdminSubmissions.vue') },
            { path: 'contests/:id/unfreeze', name: 'admin-unfreeze', component: () => import('@/views/admin/AdminUnfreeze.vue') },
          ]
        }
      ]
    },
    { path: '/login',    name: 'login',    component: () => import('@/views/LoginView.vue') },
    { path: '/register', name: 'register', component: () => import('@/views/RegisterView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ]
})

// 部署后浏览器若仍持有旧 index.html，懒加载路由会去取已不存在的旧 chunk
// （404），vue-router 静默放弃导航，表现为菜单"点不开"。此时强制整页刷新，
// 用新 index.html 重新进入目标路由即可自愈。sessionStorage 防无限刷新。
router.onError((error, to) => {
  const msg = String((error as Error)?.message ?? '')
  if (/dynamically imported module|module script failed/i.test(msg)) {
    if (!sessionStorage.getItem('oj-chunk-reload')) {
      sessionStorage.setItem('oj-chunk-reload', '1')
      window.location.href = to.fullPath
    }
  }
})

router.afterEach(() => {
  sessionStorage.removeItem('oj-chunk-reload')
})

router.beforeEach((to, _from, next) => {
  const auth = useAuthStore()
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    return next({ name: 'login', query: { redirect: to.fullPath } })
  }
  if (to.meta.requiresAdmin && auth.user?.role !== 'admin') {
    return next({ name: 'home' })
  }
  next()
})

export default router
