<template>
  <div class="auth-page">
    <div class="auth-card oj-fade-in">
      <!-- Logo / brand -->
      <div class="auth-brand">
        <span class="brand-logo">⚡</span>
        <span class="brand-name">OJ</span>
      </div>

      <h2 class="auth-title">创建账号</h2>
      <p class="auth-sub">加入 OJ，开始你的算法之旅</p>

      <el-form
        :model="form"
        :rules="rules"
        ref="formRef"
        label-position="top"
        class="auth-form"
        @submit.prevent="handleRegister"
      >
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            placeholder="用户名（3-32 个字符）"
            size="large"
            :prefix-icon="User"
            autocomplete="username"
          />
        </el-form-item>
        <el-form-item prop="email">
          <el-input
            v-model="form.email"
            placeholder="邮箱"
            size="large"
            :prefix-icon="Message"
            autocomplete="email"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            type="password"
            placeholder="密码（至少 6 位）"
            size="large"
            :prefix-icon="Lock"
            show-password
            autocomplete="new-password"
          />
        </el-form-item>
        <el-form-item>
          <el-input
            v-model="form.organization"
            placeholder="组织 / 学校（选填）"
            size="large"
            :prefix-icon="OfficeBuilding"
          />
        </el-form-item>

        <el-button
          type="primary"
          native-type="submit"
          size="large"
          :loading="loading"
          class="auth-submit"
        >
          注册
        </el-button>
      </el-form>

      <div class="auth-footer">
        已有账号？<router-link to="/login" class="link-text">立即登录</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { FormInstance } from 'element-plus'
import { User, Lock, Message, OfficeBuilding } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const auth    = useAuthStore()
const router  = useRouter()
const formRef = ref<FormInstance>()
const loading = ref(false)

const form = reactive({ username: '', email: '', password: '', organization: '' })
const rules = {
  username: [{ required: true, min: 3, max: 32, message: '请输入 3-32 个字符的用户名', trigger: 'blur' }],
  email:    [{ required: true, type: 'email' as const, message: '邮箱格式不正确', trigger: 'blur' }],
  password: [{ required: true, min: 6, message: '密码至少 6 位', trigger: 'blur' }],
}

async function handleRegister() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    await auth.register(form)
    ElMessage.success('注册成功，欢迎加入！')
    router.push('/')
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
  max-width: 440px;
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

.auth-form :deep(.el-form-item) { margin-bottom: 14px; }
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
