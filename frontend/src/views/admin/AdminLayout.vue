<template>
  <el-container class="admin-wrap">
    <el-aside width="200px" class="admin-aside">
      <div class="aside-title">管理后台</div>
      <el-menu :router="true" :default-active="$route.path" background-color="#1d2129" text-color="#c9cdd4" active-text-color="#409eff">
        <el-menu-item index="/admin">
          <el-icon><DataBoard /></el-icon><span>概览</span>
        </el-menu-item>
        <el-menu-item index="/admin/problems">
          <el-icon><Document /></el-icon><span>题目管理</span>
        </el-menu-item>
        <el-menu-item index="/admin/contests">
          <el-icon><Trophy /></el-icon><span>比赛管理</span>
        </el-menu-item>
        <el-menu-item index="/">
          <el-icon><House /></el-icon><span>回到主站</span>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-main class="admin-main">
      <router-view />
    </el-main>
  </el-container>
</template>

<style scoped>
.admin-wrap   { min-height: 100vh; }
.admin-aside  { background: #1d2129; }
.aside-title  { color: #fff; font-size: 16px; font-weight: 700; padding: 20px 20px 12px; letter-spacing: 1px; }
.admin-main   { background: #f5f7fa; padding: 24px; }
</style>
