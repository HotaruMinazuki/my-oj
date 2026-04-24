<template>
  <div class="page oj-fade-in" v-loading="loading">
    <template v-if="sub">
      <!-- ── Overview card ── -->
      <el-card shadow="never" class="overview-card">
        <template #header>
          <div class="ov-header">
            <div class="ov-title">
              <el-icon class="ov-icon"><Document /></el-icon>
              提交 <span class="sub-id">#{{ sub.id }}</span>
            </div>
            <div class="ov-status">
              <el-tag
                :type="statusTagType(sub.status)"
                size="large"
                effect="light"
                class="status-tag"
              >
                <el-icon v-if="isTerminal" class="s-icon">
                  <component :is="statusIcon(sub.status)" />
                </el-icon>
                <el-icon v-else class="is-loading s-icon"><Loading /></el-icon>
                {{ statusCn(sub.status) }}
              </el-tag>
            </div>
          </div>
        </template>

        <el-descriptions :column="4" border size="small" class="ov-desc">
          <el-descriptions-item label="语言">
            <el-tag size="small" effect="plain">{{ sub.language }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="用时">
            <span v-if="sub.time_used_ms">
              <el-icon><Timer /></el-icon> {{ sub.time_used_ms }}ms
            </span>
            <span v-else class="empty-val">—</span>
          </el-descriptions-item>
          <el-descriptions-item label="内存">
            <span v-if="sub.mem_used_kb">
              💾 {{ Math.round(sub.mem_used_kb / 1024) }}MB
            </span>
            <span v-else class="empty-val">—</span>
          </el-descriptions-item>
          <el-descriptions-item label="得分">
            <span v-if="sub.score != null" class="score">{{ sub.score }}</span>
            <span v-else class="empty-val">—</span>
          </el-descriptions-item>
        </el-descriptions>

        <!-- Compile log -->
        <div v-if="sub.compile_log" class="log-block">
          <div class="log-title">
            <el-icon><WarningFilled /></el-icon> 编译信息
          </div>
          <pre class="log-pre">{{ sub.compile_log }}</pre>
        </div>

        <!-- Judge message -->
        <div v-if="sub.judge_message" class="log-block">
          <div class="log-title">
            <el-icon><InfoFilled /></el-icon> 评测信息
          </div>
          <pre class="log-pre">{{ sub.judge_message }}</pre>
        </div>
      </el-card>

      <!-- ── Test case results ── -->
      <el-card v-if="sub.test_case_results?.length" shadow="never" class="tc-card">
        <template #header>
          <div class="tc-head">
            <span class="tc-title">
              <el-icon><List /></el-icon> 逐测试点详情
            </span>
            <span class="tc-summary">
              通过 <strong class="ac-count">{{ acCount }}</strong>
              / {{ sub.test_case_results.length }} 个测试点
            </span>
          </div>
        </template>
        <el-table :data="sub.test_case_results" stripe size="small" style="width:100%">
          <el-table-column label="#" prop="test_case_id" width="56" align="center" />
          <el-table-column label="组" prop="group_id" width="56" align="center" />
          <el-table-column label="状态" width="160">
            <template #default="{ row }">
              <el-tag :type="statusTagType(row.status)" size="small" effect="light">
                {{ statusCn(row.status) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="用时" width="100">
            <template #default="{ row }">
              <span :class="{ 'tle-val': row.status === 'TimeLimitExceeded' }">
                {{ row.time_used_ms }}ms
              </span>
            </template>
          </el-table-column>
          <el-table-column label="内存" width="100">
            <template #default="{ row }">{{ Math.round(row.mem_used_kb / 1024) }}MB</template>
          </el-table-column>
          <el-table-column label="得分" prop="score" width="72" align="center" />
          <el-table-column label="Checker 输出" prop="checker_output" min-width="200" show-overflow-tooltip />
        </el-table>
      </el-card>

      <!-- Polling indicator -->
      <div v-if="!isTerminal" class="polling-row">
        <el-icon class="is-loading poll-spin"><Loading /></el-icon>
        <span>评测中，自动刷新…</span>
        <el-progress
          :percentage="pollProgress"
          :show-text="false"
          status="striped"
          striped-flow
          :duration="1"
          style="width:120px"
        />
      </div>
    </template>
    <el-empty v-else-if="!loading" description="提交不存在" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import {
  Document, Timer, Loading,
  CircleCheck, CircleClose, WarningFilled, InfoFilled, List,
} from '@element-plus/icons-vue'
import { submissionApi } from '@/api/http'
import { TERMINAL_STATUSES } from '@/types'
import type { Submission } from '@/types'

const route   = useRoute()
const sub     = ref<Submission | null>(null)
const loading = ref(true)
let   timer: ReturnType<typeof setInterval> | null = null
let   pollCount = 0
const MAX_POLL  = 60

const isTerminal = computed(() => sub.value ? TERMINAL_STATUSES.includes(sub.value.status) : true)
const pollProgress = computed(() => Math.min(100, Math.round((pollCount / MAX_POLL) * 100)))

const acCount = computed(() =>
  (sub.value?.test_case_results ?? []).filter((r: any) => r.status === 'Accepted').length
)

async function fetchSub() {
  try {
    sub.value = await submissionApi.get(Number(route.params.id))
    pollCount++
    if (isTerminal.value && timer) {
      clearInterval(timer)
      timer = null
    }
    if (pollCount >= MAX_POLL && timer) {
      clearInterval(timer)
      timer = null
    }
  } finally {
    loading.value = false
  }
}

// ── Status helpers ─────────────────────────────────────────────────────────
function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (s === 'Accepted') return 'success'
  if (['Pending', 'Judging', 'Compiling'].includes(s)) return 'warning'
  return 'danger'
}
function statusIcon(s: string) {
  if (s === 'Accepted') return CircleCheck
  return CircleClose
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

onMounted(() => {
  fetchSub()
  timer = setInterval(fetchSub, 1500)
})
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>

<style scoped>
/* ── Overview card ── */
.overview-card { margin-bottom: 16px; }
.ov-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}
.ov-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
  font-weight: 600;
}
.ov-icon { color: var(--oj-primary); }
.sub-id  { color: var(--oj-primary); }
.status-tag { font-size: 15px; padding: 0 14px; height: 36px; }
.s-icon { margin-right: 4px; }

.ov-desc { margin-top: 4px; }
.empty-val { color: var(--oj-text-3); }
.score { font-weight: 700; font-size: 15px; color: var(--oj-primary); }

/* ── Logs ── */
.log-block { margin-top: 16px; }
.log-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--oj-text-2);
  margin-bottom: 6px;
}
.log-pre {
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 12px 14px;
  border-radius: var(--oj-radius);
  font-size: 12px;
  font-family: ui-monospace, monospace;
  overflow: auto;
  max-height: 200px;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}

/* ── Test cases ── */
.tc-card { margin-bottom: 16px; }
.tc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.tc-title {
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.tc-summary { font-size: 13px; color: var(--oj-text-3); }
.ac-count   { color: var(--oj-success); }
.tle-val    { color: var(--oj-danger); font-weight: 600; }

/* ── Polling ── */
.polling-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  margin-top: 20px;
  color: var(--oj-text-3);
  font-size: 14px;
}
.poll-spin { font-size: 16px; }
</style>
