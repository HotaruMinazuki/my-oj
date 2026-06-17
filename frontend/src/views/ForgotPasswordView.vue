<template>
  <div class="auth-page">
    <div class="auth-card oj-fade-in">
      <div class="auth-brand">
        <span class="brand-name">OJ</span>
      </div>

      <h2 class="auth-title">找回密码</h2>

      <el-form
        :model="form"
        :rules="rules"
        ref="formRef"
        label-position="top"
        class="auth-form"
        @submit.prevent="handleReset"
      >
        <el-form-item prop="identifier">
          <el-input
            v-model="form.identifier"
            placeholder="用户名 / 邮箱"
            size="large"
            :prefix-icon="User"
            autocomplete="username"
          />
        </el-form-item>
        <el-form-item prop="code">
          <el-input v-model="form.code" placeholder="6 位验证码" size="large" maxlength="6" :prefix-icon="Key">
            <template #append>
              <el-button
                :disabled="cooldownLeft > 0 || sending || !form.identifier.trim()"
                :loading="sending"
                @click="sendCode"
              >
                {{ cooldownLeft > 0 ? `${cooldownLeft}s 后重发` : '发送验证码' }}
              </el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item prop="newPassword">
          <el-input
            v-model="form.newPassword"
            type="password"
            placeholder="新密码（至少 6 位）"
            size="large"
            :prefix-icon="Lock"
            show-password
            autocomplete="new-password"
          />
        </el-form-item>
        <el-form-item prop="confirmPassword">
          <el-input
            v-model="form.confirmPassword"
            type="password"
            placeholder="确认新密码"
            size="large"
            :prefix-icon="Lock"
            show-password
            autocomplete="new-password"
            @keyup.enter="handleReset"
          />
        </el-form-item>

        <el-button
          type="primary"
          native-type="submit"
          size="large"
          :loading="resetting"
          class="auth-submit"
        >
          重置密码
        </el-button>
      </el-form>

      <div class="auth-footer">
        想起来了？<router-link to="/login" class="link-text">返回登录</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { FormInstance } from 'element-plus'
import { User, Lock, Key } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { authApi } from '@/api/http'
import { useCountdown } from '@/composables/useCountdown'

const router  = useRouter()
const formRef = ref<FormInstance>()
const sending   = ref(false)
const resetting = ref(false)

const form = reactive({ identifier: '', code: '', newPassword: '', confirmPassword: '' })
const rules = {
  identifier:      [{ required: true, message: '请输入用户名或邮箱', trigger: 'blur' }],
  code:            [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  newPassword:     [{ required: true, min: 6, message: '新密码至少 6 位', trigger: 'blur' }],
  confirmPassword: [{ required: true, message: '请再次输入新密码', trigger: 'blur' }],
}

// 60s 重发倒计时 (复用倒计时 composable)
const cooldownUntil = ref<string | null>(null)
const { total } = useCountdown(cooldownUntil)
const cooldownLeft = computed(() => Math.ceil(total.value / 1000))

async function sendCode() {
  const identifier = form.identifier.trim()
  if (!identifier) {
    ElMessage.warning('请先输入用户名或邮箱')
    return
  }
  sending.value = true
  try {
    const res = await authApi.requestPasswordReset(identifier)
    cooldownUntil.value = dayjs().add(60, 'second').toISOString()
    if (res.smtp_enabled) {
      ElMessage.success(`验证码已发送至 ${res.email}`)
    } else {
      ElMessage.success('验证码已生成（开发模式：请在服务端日志查看）')
    }
  } catch {
    // 错误信息已由 http 拦截器统一提示 (如账号不存在 / 未绑定邮箱)。
  } finally {
    sending.value = false
  }
}

async function handleReset() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (form.newPassword !== form.confirmPassword) {
    ElMessage.error('两次输入的新密码不一致')
    return
  }
  resetting.value = true
  try {
    await authApi.confirmPasswordReset({
      identifier: form.identifier.trim(),
      code: form.code.trim(),
      new_password: form.newPassword,
    })
    ElMessage.success('密码已重置，请使用新密码登录')
    router.push('/login')
  } catch {
    // 错误信息已由 http 拦截器统一提示。
  } finally {
    resetting.value = false
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
.auth-brand { display: flex; align-items: center; justify-content: center; gap: 6px; margin-bottom: 20px; }
.brand-logo { font-size: 32px; }
.brand-name { font-size: 28px; font-weight: 800; letter-spacing: 3px; color: var(--oj-text); }
.auth-title { text-align: center; margin: 0 0 4px; font-size: 22px; font-weight: 700; color: var(--oj-text); }
.auth-sub { text-align: center; margin: 0 0 28px; color: var(--oj-text-3); font-size: 13px; }
.auth-form :deep(.el-form-item) { margin-bottom: 16px; }
.auth-form :deep(.el-form-item__label) { display: none; }
.auth-submit { width: 100%; margin-top: 8px; font-size: 15px; letter-spacing: .5px; }
.auth-footer { text-align: center; margin-top: 20px; color: var(--oj-text-3); font-size: 14px; }
</style>
