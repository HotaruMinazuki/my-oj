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
        </el-tabs>
      </el-card>
    </template>
    <el-empty v-else-if="!loading" description="用户不存在" />
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { userApi } from '@/api/http'
import type { Contest, SubmissionListItem, UserPublic, UserSubmissionStats } from '@/types'

const route  = useRoute()
const router = useRouter()

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

async function fetchProfile() {
  loading.value = true
  user.value = null
  try {
    const data = await userApi.profile(Number(route.params.id))
    user.value  = data.user
    stats.value = data.stats
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
  return 'danger'
}
function statusCn(s: string) {
  const map: Record<string, string> = {
    Accepted: '通过', WrongAnswer: '答案错误',
    TimeLimitExceeded: '超时', MemoryLimitExceeded: '超内存',
    RuntimeError: '运行错误', CompileError: '编译错误',
    SystemError: '系统错误', Pending: '等待中',
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

.profile-stats { display: flex; gap: 28px; }
.stat { text-align: center; }
.stat-num   { font-size: 22px; font-weight: 700; color: var(--oj-primary); }
.stat-num.ac { color: var(--oj-success); }
.stat-label { font-size: 12px; color: var(--oj-text-3); margin-top: 2px; }

.history-card :deep(.el-table .el-table__row) { cursor: pointer; }
.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }
.empty-hint { color: var(--oj-text-3); font-size: 13px; }
</style>
