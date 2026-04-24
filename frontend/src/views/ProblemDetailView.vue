<template>
  <div class="page oj-fade-in" v-loading="loading">
    <template v-if="problem">
      <el-row :gutter="20">
        <!-- ── Left: problem statement ── -->
        <el-col :xs="24" :md="13">
          <el-card shadow="never" class="prob-card">
            <template #header>
              <div class="prob-header">
                <h2 class="prob-title">{{ problem.title }}</h2>
                <div class="limits">
                  <el-tag>
                    <el-icon><Timer /></el-icon> {{ problem.time_limit_ms }}ms
                  </el-tag>
                  <el-tag type="success">
                    💾 {{ Math.round(problem.mem_limit_kb / 1024) }}MB
                  </el-tag>
                  <el-tag :type="judgeTagType(problem.judge_type)">
                    {{ judgeLabel(problem.judge_type) }}
                  </el-tag>
                </div>
              </div>
            </template>
            <div class="markdown-body" v-html="renderedStatement" />
          </el-card>
        </el-col>

        <!-- ── Right: code submission ── -->
        <el-col :xs="24" :md="11">
          <el-card shadow="never" class="submit-card">
            <template #header>
              <div class="submit-header">
                <span>提交代码</span>
                <el-select
                  v-model="lang"
                  size="small"
                  style="width:120px"
                  @change="onLangChange"
                >
                  <el-option v-for="l in availableLangs" :key="l" :label="l" :value="l" />
                </el-select>
              </div>
            </template>

            <CodeEditor
              v-model="code"
              :language="lang"
              :draft-key="draftKey"
              height="360px"
              @submit="handleSubmit"
            />

            <div class="submit-row">
              <el-tooltip
                v-if="!auth.isLoggedIn"
                content="请先登录"
                placement="top"
              >
                <el-button
                  type="primary"
                  size="large"
                  disabled
                  style="flex:1"
                >
                  登录后提交
                </el-button>
              </el-tooltip>
              <el-button
                v-else
                type="primary"
                size="large"
                :loading="submitting"
                style="flex:1"
                @click="handleSubmit"
              >
                提交代码
              </el-button>
            </div>

            <!-- Last submission status -->
            <template v-if="lastSubmission">
              <el-divider>
                <span class="divider-text">最近提交</span>
              </el-divider>
              <div class="last-sub">
                <el-tag :type="statusTagType(lastSubmission.status)" size="large" effect="light">
                  <el-icon v-if="isTerminal" class="status-icon">
                    <component :is="statusIcon(lastSubmission.status)" />
                  </el-icon>
                  <el-icon v-else class="is-loading status-icon"><Loading /></el-icon>
                  {{ statusCn(lastSubmission.status) }}
                </el-tag>
                <span v-if="lastSubmission.time_used_ms" class="sub-metric">
                  <el-icon><Timer /></el-icon> {{ lastSubmission.time_used_ms }}ms
                </span>
                <span v-if="lastSubmission.mem_used_kb" class="sub-metric">
                  💾 {{ Math.round(lastSubmission.mem_used_kb / 1024) }}MB
                </span>
                <router-link :to="`/submissions/${lastSubmission.id}`" class="link-text sub-detail">
                  详情 →
                </router-link>
              </div>
              <div v-if="lastSubmission.compile_log" class="compile-block">
                <div class="compile-title">编译信息</div>
                <pre class="compile-pre">{{ lastSubmission.compile_log }}</pre>
              </div>
            </template>
          </el-card>
        </el-col>
      </el-row>
    </template>
    <el-empty v-else-if="!loading" description="题目不存在" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Timer, Loading, CircleCheck, CircleClose, WarningFilled } from '@element-plus/icons-vue'
import MarkdownIt from 'markdown-it'
import { problemApi, submissionApi } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import { TERMINAL_STATUSES } from '@/types'
import type { Problem, Submission } from '@/types'
import CodeEditor from '@/components/CodeEditor.vue'

const LANGS = ['C++17', 'C++20', 'C', 'Java21', 'Python3', 'Go', 'Rust']

const route  = useRoute()
const auth   = useAuthStore()
const md     = new MarkdownIt({ html: false, linkify: true, typographer: true })

const problem        = ref<Problem | null>(null)
const loading        = ref(true)
const code           = ref('// 在此输入代码\n')
const lang           = ref('C++17')
const submitting     = ref(false)
const lastSubmission = ref<Submission | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

const availableLangs = computed(() => problem.value?.allowed_langs?.length ? problem.value.allowed_langs : LANGS)
const isTerminal     = computed(() => lastSubmission.value ? TERMINAL_STATUSES.includes(lastSubmission.value.status) : true)

// Draft key includes problem ID + language so each language has its own draft
const draftKey = computed(() =>
  problem.value ? `problem-${problem.value.id}-${lang.value}` : undefined
)

function onLangChange() {
  // draftKey automatically updates → CodeEditor watcher loads new draft
}

const renderedStatement = computed(() =>
  problem.value ? md.render(problem.value.statement || '（暂无题面）') : ''
)

async function fetchProblem() {
  try {
    problem.value = await problemApi.get(Number(route.params.id))
  } finally {
    loading.value = false
  }
}

async function handleSubmit() {
  if (!auth.isLoggedIn) { ElMessage.warning('请先登录'); return }
  if (!code.value.trim()) { ElMessage.warning('请输入代码'); return }
  submitting.value = true
  try {
    const res = await submissionApi.submitPractice({
      problem_id:  problem.value!.id,
      language:    lang.value,
      source_code: code.value,
    })
    // The submit ack only contains {id,status}; let the first poll fill the rest.
    lastSubmission.value = null
    startPoll(res.id)
    ElMessage.success('提交成功，评测中…')
  } finally {
    submitting.value = false
  }
}

function startPoll(id: number) {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = setInterval(async () => {
    const sub = await submissionApi.get(id)
    lastSubmission.value = sub
    if (TERMINAL_STATUSES.includes(sub.status)) {
      clearInterval(pollTimer!)
      pollTimer = null
    }
  }, 1500)
}

// ── Status helpers ─────────────────────────────────────────────────────────
function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (s === 'Accepted') return 'success'
  if (['Pending', 'Judging', 'Compiling'].includes(s)) return 'warning'
  return 'danger'
}
function statusIcon(s: string) {
  if (s === 'Accepted') return CircleCheck
  if (['Pending', 'Judging', 'Compiling'].includes(s)) return WarningFilled
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
function judgeLabel(t: string) {
  return { standard: '标准', special: '特判', interactive: '交互', communication: '通信' }[t] ?? t
}
function judgeTagType(t: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ special: 'warning', interactive: 'success', communication: 'danger' } as any)[t] ?? ''
}

onMounted(fetchProblem)
onBeforeUnmount(() => { if (pollTimer) clearInterval(pollTimer) })
</script>

<style scoped>
/* ── Problem header ── */
.prob-header { display: flex; flex-direction: column; gap: 8px; }
.prob-title  { margin: 0; font-size: 20px; font-weight: 700; }
.limits      { display: flex; gap: 8px; flex-wrap: wrap; }

/* ── Cards ── */
.prob-card, .submit-card { height: 100%; }

/* ── Markdown ── */
.markdown-body { line-height: 1.8; color: var(--oj-text); }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3) {
  font-weight: 700; margin: 1em 0 .5em;
}
.markdown-body :deep(pre) {
  background: #f5f7fa;
  border: 1px solid var(--oj-border);
  padding: 12px;
  border-radius: var(--oj-radius);
  overflow: auto;
  font-size: 13px;
}
.markdown-body :deep(code) {
  font-family: ui-monospace, 'Cascadia Code', monospace;
  background: #f5f7fa;
  padding: 2px 5px;
  border-radius: 3px;
  font-size: 13px;
}
.markdown-body :deep(table) { border-collapse: collapse; width: 100%; }
.markdown-body :deep(td), .markdown-body :deep(th) {
  border: 1px solid var(--oj-border);
  padding: 6px 10px;
}
.markdown-body :deep(th) { background: var(--oj-bg); }

/* ── Submit card ── */
.submit-header { display: flex; align-items: center; justify-content: space-between; }
.submit-row    { display: flex; margin-top: 12px; gap: 8px; }

/* ── Last submission ── */
.divider-text { font-size: 12px; color: var(--oj-text-3); }
.last-sub {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-top: 4px;
}
.status-icon { margin-right: 4px; }
.sub-metric  { display: flex; align-items: center; gap: 4px; font-size: 13px; color: var(--oj-text-2); }
.sub-detail  { font-size: 13px; }
.compile-block { margin-top: 12px; }
.compile-title { font-size: 12px; color: var(--oj-text-3); margin-bottom: 4px; }
.compile-pre {
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 12px;
  border-radius: var(--oj-radius);
  font-size: 12px;
  overflow: auto;
  max-height: 160px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
