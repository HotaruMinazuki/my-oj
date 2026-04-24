<template>
  <div class="page oj-fade-in" v-loading="loading">
    <template v-if="contest">
      <!-- ── Header card ── -->
      <el-card shadow="never" class="ch-card">
        <div class="ch-body">
          <div class="ch-info">
            <div class="ch-tags">
              <el-tag :type="statusTagType(contest.status)" effect="light">
                <span class="status-dot" :class="contest.status" />{{ statusLabel(contest.status) }}
              </el-tag>
              <el-tag effect="plain">{{ contest.contest_type }}</el-tag>
            </div>
            <h2 class="ch-title">{{ contest.title }}</h2>
            <p v-if="contest.description" class="ch-desc">{{ contest.description }}</p>
            <div class="ch-meta">
              <span class="meta-item">
                <el-icon><Clock /></el-icon>
                {{ fmt(contest.start_time) }} — {{ fmt(contest.end_time) }}
              </span>
              <span v-if="contest.freeze_time" class="meta-item">
                <el-icon><Lock /></el-icon>
                封榜: {{ fmt(contest.freeze_time) }}
              </span>
            </div>

            <!-- Live countdown -->
            <div v-if="contest.status === 'ready'" class="ch-countdown ready">
              <el-icon><Timer /></el-icon>
              距比赛开始：<strong class="cd-val">{{ cdFormatted }}</strong>
            </div>
            <div v-else-if="contest.status === 'running' || contest.status === 'frozen'" class="ch-countdown running">
              <el-icon><Timer /></el-icon>
              距比赛结束：<strong class="cd-val">{{ cdFormatted }}</strong>
            </div>
            <div v-else-if="contest.status === 'ended'" class="ch-countdown ended">
              <el-icon><CircleCheck /></el-icon>
              比赛已结束
            </div>
          </div>

          <div class="ch-actions">
            <router-link :to="`/contests/${contest.id}/ranking`">
              <el-button :icon="Histogram" type="info" plain>排行榜</el-button>
            </router-link>
            <el-button
              v-if="!registered && contest.status !== 'ended'"
              type="primary"
              :loading="registering"
              @click="handleRegister"
            >
              {{ auth.isLoggedIn ? '报名参赛' : '登录后报名' }}
            </el-button>
            <el-tag v-else-if="registered" type="success" size="large" effect="plain">
              <el-icon><CircleCheck /></el-icon> 已报名
            </el-tag>
          </div>
        </div>
      </el-card>

      <!-- ── Problem list ── -->
      <el-card shadow="never" class="problems-card">
        <template #header>
          <div class="section-head">
            <span class="section-title">
              <el-icon><DocumentCopy /></el-icon> 题目列表
            </span>
            <span v-if="!registered && contest.status !== 'ended'" class="register-hint">
              报名后可提交
            </span>
          </div>
        </template>
        <el-skeleton v-if="loadingProblems" :rows="4" animated />
        <el-empty v-else-if="problems.length === 0" description="暂无题目" />
        <el-table v-else :data="problems" stripe style="width:100%">
          <el-table-column label="题号" prop="label" width="80" align="center">
            <template #default="{ row }">
              <span class="label-badge">{{ row.label }}</span>
            </template>
          </el-table-column>
          <el-table-column label="题目名称" min-width="220">
            <template #default="{ row }">
              <router-link :to="`/problems/${row.problem_id}`" class="link-text">
                {{ row.title }}
              </router-link>
            </template>
          </el-table-column>
          <el-table-column label="时限" width="100" align="center">
            <template #default="{ row }">{{ row.time_limit_ms }}ms</template>
          </el-table-column>
          <el-table-column label="内存" width="100" align="center">
            <template #default="{ row }">{{ Math.round(row.mem_limit_kb / 1024) }}MB</template>
          </el-table-column>
          <el-table-column label="提交" width="110" align="center">
            <template #default="{ row }">
              <el-button
                size="small"
                type="primary"
                :disabled="!registered || contest.status === 'ended'"
                @click="openSubmit(row)"
              >
                提交代码
              </el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- ── Submit dialog ── -->
      <el-dialog
        v-model="submitVisible"
        :title="`提交 — ${submitTarget?.label}: ${submitTarget?.title}`"
        width="740px"
        destroy-on-close
      >
        <div class="dialog-lang-row">
          <span class="dialog-lang-label">语言</span>
          <el-select v-model="submitLang" style="width:180px" size="large">
            <el-option v-for="l in LANGS" :key="l" :label="l" :value="l" />
          </el-select>
        </div>
        <CodeEditor
          v-model="submitCode"
          :language="submitLang"
          :draft-key="draftKey"
          height="380px"
          @submit="handleSubmit"
        />
        <template #footer>
          <el-button @click="submitVisible = false">取消</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            提交 <kbd style="font-size:11px;margin-left:4px;opacity:.7">Ctrl+↵</kbd>
          </el-button>
        </template>
      </el-dialog>
    </template>
    <el-empty v-else-if="!loading" description="比赛不存在" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Clock, Lock, Timer, CircleCheck, Histogram, DocumentCopy,
} from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { contestApi, submissionApi } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { useCountdown } from '@/composables/useCountdown'
import type { Contest, ContestProblemSummary } from '@/types'
import CodeEditor from '@/components/CodeEditor.vue'

const LANGS = ['C++17', 'C++20', 'C', 'Java21', 'Python3', 'Go', 'Rust']

const route  = useRoute()
const router = useRouter()
const auth   = useAuthStore()

const contest         = ref<Contest | null>(null)
const registered      = ref(false)
const problems        = ref<ContestProblemSummary[]>([])
const loading         = ref(true)
const loadingProblems = ref(true)
const registering     = ref(false)

// Countdown: point at start_time for "ready", end_time for "running/frozen"
const cdTarget = computed<string | null>(() => {
  if (!contest.value) return null
  if (contest.value.status === 'ready')   return contest.value.start_time
  if (contest.value.status === 'running' || contest.value.status === 'frozen')
    return contest.value.end_time
  return null
})
const { formatted: cdFormatted } = useCountdown(cdTarget)

const submitVisible = ref(false)
const submitTarget  = ref<ContestProblemSummary | null>(null)
const submitLang    = ref('C++17')
const submitCode    = ref('// 在此输入代码\n')
const submitting    = ref(false)

const draftKey = computed(() =>
  submitTarget.value
    ? `contest-${contest.value?.id}-prob-${submitTarget.value.problem_id}-${submitLang.value}`
    : undefined
)

async function fetchContest() {
  try {
    const data = await contestApi.get(Number(route.params.id))
    contest.value    = data.contest
    registered.value = data.registered
  } finally {
    loading.value = false
  }
}

async function fetchProblems() {
  try {
    const data = await contestApi.getProblems(Number(route.params.id))
    problems.value = data.problems ?? []
  } finally {
    loadingProblems.value = false
  }
}

async function handleRegister() {
  if (!auth.isLoggedIn) { router.push('/login'); return }
  registering.value = true
  try {
    await contestApi.register(Number(route.params.id))
    registered.value = true
    ElMessage.success('报名成功！')
  } finally {
    registering.value = false
  }
}

function openSubmit(row: ContestProblemSummary) {
  if (!auth.isLoggedIn) { router.push('/login'); return }
  submitTarget.value  = row
  submitCode.value    = '// 在此输入代码\n'
  submitLang.value    = 'C++17'
  submitVisible.value = true
}

async function handleSubmit() {
  if (!submitCode.value.trim()) { ElMessage.warning('请输入代码'); return }
  submitting.value = true
  try {
    const res = await submissionApi.submit(Number(route.params.id), {
      problem_id: submitTarget.value!.problem_id,
      language:   submitLang.value,
      source:     submitCode.value,
    })
    submitVisible.value = false
    ElMessage.success('提交成功！')
    router.push(`/submissions/${res.id}`)
  } finally {
    submitting.value = false
  }
}

const fmt = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')

function statusLabel(s: string) {
  return { running: '进行中', frozen: '封榜', ended: '已结束', ready: '即将开始', draft: '草稿' }[s] ?? s
}
function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ running: 'success', frozen: 'warning', ended: 'info', ready: '', draft: 'info' } as any)[s] ?? ''
}

onMounted(() => { fetchContest(); fetchProblems() })
</script>

<style scoped>
/* ── Contest header card ── */
.ch-card { margin-bottom: 16px; }
.ch-body { display: flex; justify-content: space-between; align-items: flex-start; gap: 24px; flex-wrap: wrap; }
.ch-info { flex: 1; min-width: 0; }
.ch-tags { display: flex; gap: 6px; margin-bottom: 10px; flex-wrap: wrap; }
.ch-title { margin: 0 0 8px; font-size: 22px; font-weight: 700; }
.ch-desc  { margin: 0 0 10px; color: var(--oj-text-2); font-size: 14px; }

.status-dot {
  display: inline-block;
  width: 6px; height: 6px;
  border-radius: 50%;
  margin-right: 4px;
  background: var(--oj-text-3);
  vertical-align: middle;
}
.status-dot.running, .status-dot.frozen { background: #67c23a; animation: blink 1.4s infinite; }
.status-dot.ready   { background: #409eff; }
.status-dot.ended   { background: #c0c4cc; }
@keyframes blink { 50% { opacity: .3 } }

.ch-meta { display: flex; flex-wrap: wrap; gap: 16px; color: var(--oj-text-3); font-size: 13px; margin-bottom: 10px; }
.meta-item { display: flex; align-items: center; gap: 4px; }

.ch-countdown { display: flex; align-items: center; gap: 6px; font-size: 14px; margin-top: 4px; }
.ch-countdown.ready   { color: #409eff; }
.ch-countdown.running { color: var(--oj-success); }
.ch-countdown.ended   { color: var(--oj-text-3); }
.cd-val { font-variant-numeric: tabular-nums; letter-spacing: .5px; }

.ch-actions { display: flex; flex-direction: column; gap: 10px; align-items: flex-end; flex-shrink: 0; }

/* ── Problem list ── */
.problems-card { margin-bottom: 16px; }
.section-head  { display: flex; align-items: center; justify-content: space-between; }
.section-title { font-size: 16px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.register-hint { font-size: 12px; color: var(--oj-text-3); }

.label-badge {
  background: var(--oj-primary);
  color: #fff;
  border-radius: 50%;
  width: 28px; height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  font-size: 13px;
}

/* ── Submit dialog ── */
.dialog-lang-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}
.dialog-lang-label { color: var(--oj-text-2); font-size: 14px; white-space: nowrap; }
</style>
