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
        { path: 'submissions/:id', name: 'submission', component: () => import('@/views/SubmissionDetailView.vue'), meta: { requiresAuth: true } },
        {
          path: 'admin',
          component: () => import('@/views/admin/AdminLayout.vue'),
          meta: { requiresAuth: true, requiresAdmin: true },
          children: [
            { path: '', name: 'admin',                   component: () => import('@/views/admin/AdminDashboard.vue') },
            { path: 'problems',  name: 'admin-problems', component: () => import('@/views/admin/AdminProblems.vue') },
            { path: 'contests',  name: 'admin-contests', component: () => import('@/views/admin/AdminContests.vue') },
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
