<template>
  <div class="app-root">
    <!-- ── Top nav ── -->
    <header class="nav-header">
      <div class="nav-inner">
        <!-- Brand -->
        <router-link to="/" class="brand">
          ⚡ <span class="brand-text">OJ</span>
        </router-link>

        <!-- Desktop links -->
        <nav class="nav-links hide-on-mobile">
          <router-link to="/problems" class="nav-link">题库</router-link>
          <router-link to="/contests" class="nav-link">比赛</router-link>
          <router-link v-if="auth.isAdmin" to="/admin" class="nav-link">管理后台</router-link>
        </nav>

        <div class="nav-spacer" />

        <!-- Desktop user area -->
        <div class="nav-user hide-on-mobile">
          <template v-if="auth.isLoggedIn">
            <el-dropdown trigger="click" @command="handleCommand">
              <span class="user-trigger">
                <el-avatar :size="28" class="user-avatar">
                  {{ auth.user?.username?.[0]?.toUpperCase() }}
                </el-avatar>
                <span class="user-name">{{ auth.user?.username }}</span>
                <el-tag v-if="auth.isAdmin" type="danger" size="small" class="admin-tag">管理员</el-tag>
                <el-icon class="caret"><ArrowDown /></el-icon>
              </span>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="logout" :icon="SwitchButton">
                    退出登录
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
          <template v-else>
            <router-link to="/login">
              <el-button size="small">登录</el-button>
            </router-link>
            <router-link to="/register">
              <el-button size="small" type="primary">注册</el-button>
            </router-link>
          </template>
        </div>

        <!-- Mobile hamburger -->
        <el-button
          class="hamburger show-on-mobile"
          :icon="menuOpen ? Close : Menu"
          circle
          text
          @click="menuOpen = !menuOpen"
        />
      </div>
    </header>

    <!-- ── Mobile drawer ── -->
    <el-drawer
      v-model="menuOpen"
      direction="rtl"
      :with-header="false"
      size="220px"
      class="mobile-drawer"
    >
      <div class="drawer-body">
        <div class="drawer-links">
          <router-link to="/"        class="drawer-link" @click="menuOpen = false">首页</router-link>
          <router-link to="/problems" class="drawer-link" @click="menuOpen = false">题库</router-link>
          <router-link to="/contests" class="drawer-link" @click="menuOpen = false">比赛</router-link>
          <router-link v-if="auth.isAdmin" to="/admin" class="drawer-link" @click="menuOpen = false">
            管理后台
          </router-link>
        </div>
        <el-divider />
        <template v-if="auth.isLoggedIn">
          <div class="drawer-user">
            <el-avatar :size="36" class="user-avatar">
              {{ auth.user?.username?.[0]?.toUpperCase() }}
            </el-avatar>
            <span>{{ auth.user?.username }}</span>
          </div>
          <el-button type="danger" plain size="small" style="width:100%;margin-top:12px" @click="handleLogout">
            退出登录
          </el-button>
        </template>
        <template v-else>
          <router-link to="/login" @click="menuOpen = false">
            <el-button style="width:100%;margin-bottom:8px">登录</el-button>
          </router-link>
          <router-link to="/register" @click="menuOpen = false">
            <el-button type="primary" style="width:100%">注册</el-button>
          </router-link>
        </template>
      </div>
    </el-drawer>

    <!-- ── Main content ── -->
    <main class="main-content">
      <div class="page-wrap">
        <router-view />
      </div>
    </main>

    <!-- ── Footer ── -->
    <footer class="app-footer">
      <div class="footer-inner">
        <span>⚡ OJ — Online Judge System</span>
        <span class="footer-sep">·</span>
        <span>Powered by Go + Vue 3</span>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useRouter } from 'vue-router'
import { ArrowDown, Menu, Close, SwitchButton } from '@element-plus/icons-vue'

const auth    = useAuthStore()
const router  = useRouter()
const menuOpen = ref(false)

function handleCommand(cmd: string) {
  if (cmd === 'logout') handleLogout()
}
function handleLogout() {
  auth.logout()
  menuOpen.value = false
  router.push('/login')
}
</script>

<style scoped>
/* ── Root ── */
.app-root {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--oj-bg);
}

/* ── Nav ── */
.nav-header {
  background: var(--oj-nav-bg);
  position: sticky;
  top: 0;
  z-index: 200;
  height: 60px;
  box-shadow: 0 1px 0 rgba(255,255,255,.05);
}
.nav-inner {
  max-width: var(--oj-max-w);
  margin: 0 auto;
  padding: 0 20px;
  height: 100%;
  display: flex;
  align-items: center;
  gap: 24px;
}
.brand {
  color: #fff;
  font-size: 20px;
  font-weight: 800;
  text-decoration: none;
  letter-spacing: .5px;
  white-space: nowrap;
  flex-shrink: 0;
}
.brand-text { letter-spacing: 2px; }

.nav-links { display: flex; gap: 4px; }
.nav-link {
  color: var(--oj-nav-text);
  text-decoration: none;
  font-size: 14px;
  font-weight: 500;
  padding: 6px 12px;
  border-radius: var(--oj-radius);
  transition: color .15s, background .15s;
}
.nav-link:hover { color: var(--oj-nav-active); background: rgba(255,255,255,.08); }
.nav-link.router-link-active { color: var(--oj-nav-active); background: rgba(255,255,255,.1); }

.nav-spacer { flex: 1; }

.nav-user { display: flex; align-items: center; gap: 10px; }
.user-trigger {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  color: var(--oj-nav-text);
  padding: 4px 8px;
  border-radius: var(--oj-radius);
  transition: background .15s;
}
.user-trigger:hover { background: rgba(255,255,255,.08); }
.user-avatar { background: var(--oj-primary); color: #fff; font-weight: 700; flex-shrink: 0; }
.user-name  { font-size: 14px; }
.admin-tag  { border-radius: 8px; }
.caret      { font-size: 12px; color: var(--oj-nav-text); }

/* ── Hamburger ── */
.hamburger { color: var(--oj-nav-text) !important; font-size: 20px !important; }
.show-on-mobile { display: none !important; }
@media (max-width: 768px) {
  .hide-on-mobile  { display: none !important; }
  .show-on-mobile  { display: inline-flex !important; }
}

/* ── Mobile drawer ── */
.drawer-body    { padding: 24px 16px; height: 100%; display: flex; flex-direction: column; }
.drawer-links   { display: flex; flex-direction: column; gap: 4px; margin-bottom: 8px; }
.drawer-link {
  color: var(--oj-text);
  text-decoration: none;
  font-size: 15px;
  font-weight: 500;
  padding: 10px 12px;
  border-radius: var(--oj-radius);
  transition: background .15s;
}
.drawer-link:hover { background: var(--oj-bg); }
.drawer-link.router-link-active { background: #ecf5ff; color: var(--oj-primary); }
.drawer-user { display: flex; align-items: center; gap: 10px; font-weight: 500; }

/* ── Main ── */
.main-content { flex: 1; padding: 24px 20px; }
.page-wrap    { max-width: var(--oj-max-w); margin: 0 auto; }

/* ── Footer ── */
.app-footer { background: var(--oj-nav-bg); padding: 16px 20px; }
.footer-inner {
  max-width: var(--oj-max-w);
  margin: 0 auto;
  color: #666d7a;
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.footer-sep { color: #3a3f4b; }

@media (max-width: 768px) {
  .main-content { padding: 16px 12px; }
}
</style>
