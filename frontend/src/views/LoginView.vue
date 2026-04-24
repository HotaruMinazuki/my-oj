<template>
  <div class="auth-page">
    <div class="auth-card oj-fade-in">
      <!-- Logo / brand -->
      <div class="auth-brand">
        <span class="brand-logo">⚡</span>
        <span class="brand-name">OJ</span>
      </div>

      <h2 class="auth-title">欢迎回来</h2>
      <p class="auth-sub">登录以参加比赛、提交代码</p>

      <el-form
        :model="form"
        :rules="rules"
        ref="formRef"
        label-position="top"
        class="auth-form"
        @submit.prevent="handleLogin"
      >
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            placeholder="用户名"
            size="large"
            :prefix-icon="User"
            autocomplete="username"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码"
            size="large"
            :prefix-icon="Lock"
            show-password
            autocomplete="current-password"
            @keyup.enter="handleLogin"
          />
        </el-form-item>

        <el-button
          type="primary"
          native-type="submit"
          size="large"
          :loading="loading"
          class="auth-submit"
        >
          登录
        </el-button>
      </el-form>

      <div class="auth-footer">
        还没有账号？<router-link to="/register" class="link-text">立即注册</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { FormInstance } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const auth    = useAuthStore()
const router  = useRouter()
const route   = useRoute()
const formRef = ref<FormInstance>()
const loading = ref(false)

const form = reactive({ username: '', password: '' })
const rules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码',   trigger: 'blur' }],
}

async function handleLogin() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.auth-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #1d2129 0%, #2c3345 100%);
  padding: 24px;
}

.auth-card {
  width: 100%;
  max-width: 420px;
  background: #fff;
  border-radius: var(--oj-radius-lg);
  padding: 40px 36px 32px;
  box-shadow: 0 20px 60px rgba(0,0,0,.3);
}

.auth-brand {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-bottom: 20px;
}
.brand-logo { font-size: 32px; }
.brand-name {
  font-size: 28px;
  font-weight: 800;
  letter-spacing: 3px;
  color: var(--oj-text);
}

.auth-title {
  text-align: center;
  margin: 0 0 4px;
  font-size: 22px;
  font-weight: 700;
  color: var(--oj-text);
}
.auth-sub {
  text-align: center;
  margin: 0 0 28px;
  color: var(--oj-text-3);
  font-size: 13px;
}

.auth-form :deep(.el-form-item) { margin-bottom: 16px; }
.auth-form :deep(.el-form-item__label) { display: none; }

.auth-submit {
  width: 100%;
  margin-top: 8px;
  font-size: 15px;
  letter-spacing: .5px;
}

.auth-footer {
  text-align: center;
  margin-top: 20px;
  color: var(--oj-text-3);
  font-size: 14px;
}
</style>
