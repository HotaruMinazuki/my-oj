<template>
  <div class="page oj-fade-in" v-loading="loading">
    <template v-if="user">
      <!-- ── 用户信息卡 ── -->
      <el-card shadow="never" class="profile-card">
        <div class="profile-head">
          <el-avatar :size="64" class="profile-avatar">
            {{ user.username?.[0]?.toUpperCase() }}
          </el-avatar>
          <div class="profile-meta">
            <div class="profile-name">
              {{ user.username }}
              <el-tag v-if="user.role === 'admin'" type="danger" size="small">管理员</el-tag>
            </div>
            <div class="profile-sub">
              <span v-if="user.organization">{{ user.organization }} · </span>
              注册于 {{ fmtDate(user.created_at) }}
            </div>
            <!-- 邮箱: 仅本人 / 管理员可见 (不对外公布) -->
            <div v-if="canSeeEmail" class="profile-email">
              <el-icon><Message /></el-icon>
              <span v-if="user.email">{{ user.email }}</span>
              <template v-else>
                <span class="empty-val">未绑定邮箱</span>
                <el-button v-if="isMe" link type="primary" @click="activeTab = 'settings'">立即绑定</el-button>
              </template>
            </div>
          </div>
          <div class="profile-stats">
            <div class="stat">
              <div class="stat-num">{{ stats?.solved ?? 0 }}</div>
              <div class="stat-label">解决题目</div>
            </div>
            <div class="stat">
              <div class="stat-num ac">{{ stats?.accepted ?? 0 }}</div>
              <div class="stat-label">AC 次数</div>
            </div>
            <div class="stat">
              <div class="stat-num">{{ stats?.total ?? 0 }}</div>
              <div class="stat-label">总提交</div>
            </div>
          </div>

        </div>
      </el-card>

      <!-- ── 历史记录 ── -->
      <el-card shadow="never" class="history-card">
        <el-tabs v-model="activeTab">
          <!-- 历史提交: 最近的在前 -->
          <el-tab-pane label="历史提交" name="submissions">
            <el-table :data="submissions" stripe v-loading="loadingSubs" @row-click="openSubmission" class="clickable">
              <el-table-column label="#" prop="id" width="80" />
              <el-table-column label="题目" min-width="180">
                <template #default="{ row }">
                  <router-link :to="`/problems/${row.problem_id}`" class="link-text" @click.stop>
                    {{ row.problem_title }}
                  </router-link>
                </template>
              </el-table-column>
              <el-table-column label="状态" width="130">
                <template #default="{ row }">
                  <el-tag :type="statusTagType(row.status)" size="small" effect="light">
                    {{ statusCn(row.status) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="得分" prop="score" width="70" align="center" />
              <el-table-column label="用时" width="90">
                <template #default="{ row }">{{ row.time_used_ms }}ms</template>
              </el-table-column>
              <el-table-column label="内存" width="90">
                <template #default="{ row }">{{ Math.round(row.mem_used_kb / 1024) }}MB</template>
              </el-table-column>
              <el-table-column label="语言" prop="language" width="90" />
              <el-table-column label="提交时间" width="170">
                <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
              </el-table-column>
            </el-table>
            <div class="pagination">
              <el-pagination
                v-model:current-page="subPage"
                :total="subTotal"
                :page-size="20"
                layout="prev,pager,next,total"
                @current-change="fetchSubmissions"
              />
            </div>
          </el-tab-pane>

          <!-- 历史比赛 -->
          <el-tab-pane label="历史比赛" name="contests">
            <el-table :data="contests" stripe v-loading="loadingContests" @row-click="openContest" class="clickable">
              <el-table-column label="#" prop="id" width="80" />
              <el-table-column label="比赛名称" prop="title" min-width="200" />
              <el-table-column label="赛制" width="90">
                <template #default="{ row }"><el-tag size="small">{{ row.contest_type }}</el-tag></template>
              </el-table-column>
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="contestStatusType(row.status)" size="small">{{ contestStatusCn(row.status) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="开始时间" width="170">
                <template #default="{ row }">{{ fmtTime(row.start_time) }}</template>
              </el-table-column>
              <el-table-column label="结束时间" width="170">
                <template #default="{ row }">{{ fmtTime(row.end_time) }}</template>
              </el-table-column>
              <template #empty>
                <span class="empty-hint">还没有参加过比赛</span>
              </template>
            </el-table>
          </el-tab-pane>

          <!-- 修改信息: 仅本人可见 (个人资料 + 修改密码) -->
          <el-tab-pane v-if="isMe" label="修改信息" name="settings">
            <div class="settings-pane">
              <!-- 个人资料 -->
              <section class="settings-section">
                <h4 class="settings-title">个人资料</h4>
                <el-form label-width="90px" class="settings-form">
                  <el-form-item label="学校/单位">
                    <el-input v-model="editOrg" maxlength="100" placeholder="用于比赛排行榜与滚榜展示" show-word-limit />
                  </el-form-item>
                  <el-form-item label="邮箱">
                    <!-- 已绑定: 只读 (一个邮箱只能绑定一个账号, 绑定后不可自助修改) -->
                    <div v-if="user?.email" class="bound-email">
                      <span>{{ user.email }}</span>
                      <span class="hint">已绑定，如需修改请联系管理员</span>
                    </div>
                    <!-- 未绑定: 可填写绑定 -->
                    <el-input v-else v-model="editEmail" placeholder="绑定后可用邮箱登录（选填）" />
                  </el-form-item>
                  <el-form-item>
                    <el-button type="primary" :loading="saving" @click="saveProfile">保存资料</el-button>
                  </el-form-item>
                </el-form>
              </section>

              <el-divider />

              <!-- 修改密码: 两种方式 -->
              <section class="settings-section">
                <h4 class="settings-title">修改密码</h4>
                <el-radio-group v-model="pwdMode" class="pwd-mode">
                  <el-radio-button label="password">原密码修改</el-radio-button>
                  <el-radio-button label="email">邮箱验证码</el-radio-button>
                </el-radio-group>

                <!-- 方式一: 原密码修改 -->
                <el-form
                  v-if="pwdMode === 'password'"
                  ref="pwdFormRef"
                  :model="pwdForm"
                  :rules="pwdRules"
                  label-width="90px"
                  class="settings-form"
                  @submit.prevent
                >
                  <el-form-item label="当前密码" prop="oldPassword">
                    <el-input v-model="pwdForm.oldPassword" type="password" show-password placeholder="请输入当前密码" autocomplete="current-password" />
                  </el-form-item>
                  <el-form-item label="新密码" prop="newPassword">
                    <el-input v-model="pwdForm.newPassword" type="password" show-password placeholder="至少 6 位" autocomplete="new-password" />
                  </el-form-item>
                  <el-form-item label="确认新密码" prop="confirmPassword">
                    <el-input v-model="pwdForm.confirmPassword" type="password" show-password placeholder="再次输入新密码" autocomplete="new-password" @keyup.enter="changePassword" />
                  </el-form-item>
                  <el-form-item>
                    <el-button type="primary" :loading="changingPwd" @click="changePassword">修改密码</el-button>
                  </el-form-item>
                </el-form>

                <!-- 方式二: 邮箱验证码修改 -->
                <template v-else>
                  <div v-if="!user?.email" class="pwd-tip">
                    当前账号未绑定邮箱，无法通过邮箱验证码修改。请先在上方「个人资料」绑定邮箱。
                  </div>
                  <el-form
                    v-else
                    ref="emailPwdFormRef"
                    :model="emailPwdForm"
                    :rules="emailPwdRules"
                    label-width="90px"
                    class="settings-form"
                    @submit.prevent
                  >
                    <el-form-item label="验证码" prop="code">
                      <div class="code-row">
                        <el-input v-model="emailPwdForm.code" maxlength="6" placeholder="发送至绑定邮箱的 6 位验证码" />
                        <el-button :disabled="cooldownLeft > 0 || sendingCode" :loading="sendingCode" @click="sendEmailCode">
                          {{ cooldownLeft > 0 ? `${cooldownLeft}s 后重发` : '发送验证码' }}
                        </el-button>
                      </div>
                    </el-form-item>
                    <el-form-item label="新密码" prop="newPassword">
                      <el-input v-model="emailPwdForm.newPassword" type="password" show-password placeholder="至少 6 位" autocomplete="new-password" />
                    </el-form-item>
                    <el-form-item label="确认新密码" prop="confirmPassword">
                      <el-input v-model="emailPwdForm.confirmPassword" type="password" show-password placeholder="再次输入新密码" autocomplete="new-password" @keyup.enter="changePasswordByEmail" />
                    </el-form-item>
                    <el-form-item>
                      <el-button type="primary" :loading="changingByEmail" @click="changePasswordByEmail">确认修改</el-button>
                    </el-form-item>
                  </el-form>
                </template>
              </section>
            </div>
          </el-tab-pane>
        </el-tabs>
      </el-card>
    </template>
    <el-empty v-else-if="!loading" description="用户不存在" />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { FormInstance } from 'element-plus'
import { Message } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { userApi, authApi } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { useCountdown } from '@/composables/useCountdown'
import type { Contest, SubmissionListItem, UserPublic, UserSubmissionStats } from '@/types'

const route  = useRoute()
const router = useRouter()
const auth   = useAuthStore()

const isMe = computed(() => auth.user?.id != null && auth.user.id === Number(route.params.id))
// 邮箱不对外公布: 仅本人或管理员可见 (后端也只在这两种情况下返回 email 字段)。
const canSeeEmail = computed(() => isMe.value || auth.isAdmin)

const user    = ref<UserPublic | null>(null)
const stats   = ref<UserSubmissionStats | null>(null)
const loading = ref(true)

const activeTab = ref('submissions')

const submissions  = ref<SubmissionListItem[]>([])
const loadingSubs  = ref(false)
const subPage      = ref(1)
const subTotal     = ref(0)

const contests        = ref<Contest[]>([])
const loadingContests = ref(false)

// ── 修改信息 (本人专属): 个人资料 ────────────────────────────────────────────
const editOrg   = ref('')
const editEmail = ref('')
const saving    = ref(false)

// 进入页面 / 切换用户后, 用当前资料回填表单。
function resetEditForm() {
  editOrg.value = user.value?.organization ?? ''
  editEmail.value = ''  // 仅未绑定时使用; 已绑定的邮箱只读展示
}

async function saveProfile() {
  const email = editEmail.value.trim()
  if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
    ElMessage.error('邮箱格式不正确')
    return
  }
  saving.value = true
  try {
    const payload: { organization: string; email?: string } = { organization: editOrg.value.trim() }
    // 仅在当前未绑定且填写了邮箱时尝试绑定。
    if (email && !user.value?.email) payload.email = email

    const res = await userApi.updateMe(payload)
    if (user.value) {
      user.value.organization = res.organization
      if (res.email !== undefined) user.value.email = res.email
    }
    // Keep the auth store / localStorage copy in sync.
    if (auth.user) {
      auth.user.organization = res.organization
      if (res.email !== undefined) auth.user.email = res.email
      localStorage.setItem('oj_user', JSON.stringify(auth.user))
    }
    ElMessage.success('已保存')
  } catch {
    // 错误信息已由 http 拦截器统一提示 (如邮箱已被占用)。
  } finally {
    saving.value = false
  }
}

// ── 修改密码 ─────────────────────────────────────────────────────────────────
const pwdMode     = ref<'password' | 'email'>('password')
const pwdFormRef  = ref<FormInstance>()
const pwdForm     = reactive({ oldPassword: '', newPassword: '', confirmPassword: '' })
const changingPwd = ref(false)
const pwdRules = {
  oldPassword:     [{ required: true, message: '请输入当前密码', trigger: 'blur' }],
  newPassword:     [{ required: true, min: 6, message: '新密码至少 6 位', trigger: 'blur' }],
  confirmPassword: [{ required: true, message: '请再次输入新密码', trigger: 'blur' }],
}

async function changePassword() {
  const valid = await pwdFormRef.value?.validate().catch(() => false)
  if (!valid) return
  if (pwdForm.newPassword !== pwdForm.confirmPassword) {
    ElMessage.error('两次输入的新密码不一致')
    return
  }
  changingPwd.value = true
  try {
    await userApi.changePassword({
      old_password: pwdForm.oldPassword,
      new_password: pwdForm.newPassword,
    })
    ElMessage.success('密码修改成功')
    pwdFormRef.value?.resetFields()
  } catch {
    // 错误信息由 http 拦截器统一提示 (如当前密码不正确)。
  } finally {
    changingPwd.value = false
  }
}

// ── 修改密码: 邮箱验证码方式 (复用找回密码端点, identifier 用当前用户名) ───────
const emailPwdFormRef = ref<FormInstance>()
const emailPwdForm    = reactive({ code: '', newPassword: '', confirmPassword: '' })
const emailPwdRules = {
  code:            [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  newPassword:     [{ required: true, min: 6, message: '新密码至少 6 位', trigger: 'blur' }],
  confirmPassword: [{ required: true, message: '请再次输入新密码', trigger: 'blur' }],
}
const sendingCode     = ref(false)
const changingByEmail = ref(false)

// 60s 重发倒计时 (复用倒计时 composable)
const cooldownUntil = ref<string | null>(null)
const { total: cooldownTotal } = useCountdown(cooldownUntil)
const cooldownLeft = computed(() => Math.ceil(cooldownTotal.value / 1000))

async function sendEmailCode() {
  if (!auth.user?.username) return
  sendingCode.value = true
  try {
    const res = await authApi.requestPasswordReset(auth.user.username)
    cooldownUntil.value = dayjs().add(60, 'second').toISOString()
    if (res.smtp_enabled) {
      ElMessage.success(`验证码已发送至 ${res.email}`)
    } else {
      ElMessage.success('验证码已生成（开发模式：请在服务端日志查看）')
    }
  } catch {
    // 错误信息已由 http 拦截器统一提示。
  } finally {
    sendingCode.value = false
  }
}

async function changePasswordByEmail() {
  const valid = await emailPwdFormRef.value?.validate().catch(() => false)
  if (!valid) return
  if (emailPwdForm.newPassword !== emailPwdForm.confirmPassword) {
    ElMessage.error('两次输入的新密码不一致')
    return
  }
  if (!auth.user?.username) return
  changingByEmail.value = true
  try {
    await authApi.confirmPasswordReset({
      identifier: auth.user.username,
      code: emailPwdForm.code.trim(),
      new_password: emailPwdForm.newPassword,
    })
    ElMessage.success('密码修改成功')
    emailPwdFormRef.value?.resetFields()
  } catch {
    // 错误信息已由 http 拦截器统一提示 (如验证码不正确)。
  } finally {
    changingByEmail.value = false
  }
}

async function fetchProfile() {
  loading.value = true
  user.value = null
  try {
    const data = await userApi.profile(Number(route.params.id))
    user.value  = data.user
    stats.value = data.stats
    if (isMe.value) resetEditForm()
  } finally {
    loading.value = false
  }
}

async function fetchSubmissions() {
  loadingSubs.value = true
  try {
    const data = await userApi.submissions(Number(route.params.id), subPage.value, 20)
    submissions.value = data.submissions ?? []
    subTotal.value    = data.total ?? 0
  } finally { loadingSubs.value = false }
}

async function fetchContests() {
  loadingContests.value = true
  try {
    const data = await userApi.contests(Number(route.params.id))
    contests.value = data.contests ?? []
  } finally { loadingContests.value = false }
}

function openSubmission(row: SubmissionListItem) {
  router.push(`/submissions/${row.id}`)
}
function openContest(row: Contest) {
  router.push(`/contests/${row.id}`)
}

// ── helpers ────────────────────────────────────────────────────────────────
const fmtDate = (t: string) => dayjs(t).format('YYYY-MM-DD')
const fmtTime = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')

function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (s === 'Accepted') return 'success'
  if (['Pending', 'Judging', 'Compiling'].includes(s)) return 'warning'
  if (s === 'Superseded') return 'info'
  return 'danger'
}
function statusCn(s: string) {
  const map: Record<string, string> = {
    Accepted: '通过', WrongAnswer: '答案错误',
    TimeLimitExceeded: '超时', MemoryLimitExceeded: '超内存',
    RuntimeError: '运行错误', CompileError: '编译错误',
    SystemError: '系统错误', Superseded: '已覆盖', Pending: '等待中',
    Judging: '评测中', Compiling: '编译中',
  }
  return map[s] ?? s
}
function contestStatusType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ running: 'success', frozen: 'warning', ended: 'info', ready: '', draft: 'info' } as any)[s] ?? ''
}
function contestStatusCn(s: string) {
  return { running: '进行中', frozen: '封榜', ended: '已结束', ready: '即将开始', draft: '草稿' }[s] ?? s
}

// 路由参数变化(查看其他用户)时整体重载
watch(() => route.params.id, () => {
  if (!route.params.id) return
  subPage.value = 1
  // 切换到别人主页时回到默认页 (修改信息 tab 仅本人可见)。
  activeTab.value = 'submissions'
  fetchProfile()
  fetchSubmissions()
  fetchContests()
}, { immediate: true })
</script>

<style scoped>
.profile-card { margin-bottom: 16px; }
.profile-head {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
}
.profile-avatar { background: var(--oj-primary); color: #fff; font-weight: 700; font-size: 26px; flex-shrink: 0; }
.profile-meta  { flex: 1; min-width: 160px; }
.profile-name  {
  font-size: 20px;
  font-weight: 700;
  display: flex;
  align-items: center;
  gap: 8px;
}
.profile-sub { color: var(--oj-text-3); font-size: 13px; margin-top: 4px; }
.profile-email {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--oj-text-3);
  font-size: 13px;
  margin-top: 4px;
}
.profile-email .empty-val { color: var(--oj-text-3); }
.bound-email { display: flex; flex-direction: column; line-height: 1.5; }
.bound-email .hint { color: var(--oj-text-3); font-size: 12px; }

.profile-stats { display: flex; gap: 28px; }
.stat { text-align: center; }
.stat-num   { font-size: 22px; font-weight: 700; color: var(--oj-primary); }
.stat-num.ac { color: var(--oj-success); }
.stat-label { font-size: 12px; color: var(--oj-text-3); margin-top: 2px; }

.history-card :deep(.el-table .el-table__row) { cursor: pointer; }
.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }
.empty-hint { color: var(--oj-text-3); font-size: 13px; }

/* 修改信息 tab */
.settings-pane { max-width: 460px; padding-top: 4px; }
.settings-section { margin-bottom: 4px; }
.settings-title { margin: 0 0 14px; font-size: 15px; font-weight: 600; color: var(--oj-text); }
.pwd-mode { margin-bottom: 16px; }
.code-row { display: flex; gap: 8px; width: 100%; }
.code-row .el-input { flex: 1; }
.pwd-tip { color: var(--oj-text-3); font-size: 13px; line-height: 1.6; padding: 4px 0 12px; }
</style>
