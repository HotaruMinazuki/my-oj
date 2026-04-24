<template>
  <div>
    <div class="page-header">
      <h2>比赛管理</h2>
      <el-button type="primary" :icon="Plus" @click="createDialog = true">新建比赛</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="contests" stripe v-loading="loading">
        <el-table-column label="ID"   prop="id"    width="70" />
        <el-table-column label="标题" prop="title" min-width="200" />
        <el-table-column label="类型" width="80">
          <template #default="{ row }"><el-tag size="small">{{ row.contest_type }}</el-tag></template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ fmt(row.start_time) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="300" align="center">
          <template #default="{ row }">
            <el-button size="small" type="primary" plain @click="openProblems(row)">
              管理题目
            </el-button>
            <router-link :to="`/contests/${row.id}`">
              <el-button size="small" plain>查看</el-button>
            </router-link>
            <router-link :to="`/admin/contests/${row.id}/unfreeze`">
              <el-button size="small" type="warning">滚榜</el-button>
            </router-link>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination">
        <el-pagination v-model:current-page="page" :total="total" layout="prev,pager,next,total" @current-change="fetch" />
      </div>
    </el-card>

    <!-- 新建比赛对话框 -->
    <el-dialog v-model="createDialog" title="新建比赛" width="640px">
      <el-form :model="form" :rules="rules" ref="formRef" label-width="110px">
        <el-form-item label="比赛名称" prop="title">
          <el-input v-model="form.title" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="赛制">
          <el-select v-model="form.contest_type" style="width:100%">
            <el-option label="ICPC" value="ICPC" />
            <el-option label="OI"   value="OI"   />
            <el-option label="IOI"  value="IOI"  />
          </el-select>
        </el-form-item>
        <el-form-item label="开始时间" prop="start_time">
          <el-date-picker v-model="form.start_time" type="datetime" style="width:100%" />
        </el-form-item>
        <el-form-item label="结束时间" prop="end_time">
          <el-date-picker v-model="form.end_time" type="datetime" style="width:100%" />
        </el-form-item>
        <el-form-item label="封榜时间">
          <el-date-picker v-model="form.freeze_time" type="datetime" style="width:100%" placeholder="不封榜则留空" />
        </el-form-item>
        <el-form-item label="公开">
          <el-switch v-model="form.is_public" />
        </el-form-item>
        <el-form-item label="允许补报名">
          <el-switch v-model="form.allow_late_register" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="handleCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 题目管理对话框 -->
    <el-dialog
      v-model="problemsDialog"
      :title="`题目管理 — ${problemsTarget?.title ?? ''}`"
      width="720px"
      destroy-on-close
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom:16px"
        description="将题目加入比赛后，选手方可在比赛页提交。Label 一般用 A/B/C… 作为展示编号。"
      />

      <!-- 已有题目 -->
      <div class="section-sub">已关联题目（{{ linkedProblems.length }}）</div>
      <el-table
        :data="linkedProblems"
        v-loading="loadingLinked"
        size="small"
        border
        style="margin-bottom:20px"
      >
        <el-table-column label="Label" prop="label" width="80" align="center">
          <template #default="{ row }">
            <span class="label-badge">{{ row.label }}</span>
          </template>
        </el-table-column>
        <el-table-column label="#"      prop="problem_id" width="60" align="center" />
        <el-table-column label="题目"   prop="title" min-width="200" />
        <el-table-column label="分值"   prop="max_score" width="80" align="center" />
        <el-table-column label="顺序"   prop="ordinal" width="70" align="center" />
        <el-table-column label="操作"   width="90" align="center">
          <template #default="{ row }">
            <el-button
              size="small"
              type="danger"
              link
              :icon="Delete"
              @click="handleRemoveProblem(row)"
            >
              移除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <span class="empty-hint">暂无题目，请在下方添加</span>
        </template>
      </el-table>

      <!-- 添加新题目 -->
      <div class="section-sub">添加题目</div>
      <div class="add-row">
        <el-select
          v-model="addForm.problem_id"
          filterable
          placeholder="选择题目（支持搜索）"
          style="flex:2"
          :loading="loadingAllProblems"
        >
          <el-option
            v-for="p in availableProblems"
            :key="p.id"
            :label="`#${p.id}  ${p.title}`"
            :value="p.id"
          />
        </el-select>
        <el-input
          v-model="addForm.label"
          placeholder="Label（如 A）"
          style="flex:1"
          maxlength="4"
        />
        <el-input-number
          v-model="addForm.max_score"
          :min="1"
          :max="1000"
          :step="10"
          controls-position="right"
          style="flex:1"
        />
        <el-button
          type="primary"
          :icon="Plus"
          :loading="adding"
          :disabled="!addForm.problem_id || !addForm.label"
          @click="handleAddProblem"
        >
          添加
        </el-button>
      </div>

      <template #footer>
        <el-button @click="problemsDialog = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { Plus, Delete } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'
import dayjs from 'dayjs'
import { contestApi, problemApi } from '@/api/http'
import type { Contest, Problem, ContestProblemSummary } from '@/types'

// ─── Main list ─────────────────────────────────────────────────────────────
const contests = ref<Contest[]>([])
const loading  = ref(false)
const page     = ref(1)
const total    = ref(0)

async function fetch() {
  loading.value = true
  try {
    const data = await contestApi.list(page.value, 20)
    contests.value = data.contests ?? []
    total.value    = data.total    ?? 0
  } finally { loading.value = false }
}

// ─── Create contest dialog ────────────────────────────────────────────────
const createDialog = ref(false)
const creating     = ref(false)
const formRef      = ref<FormInstance>()
const form = reactive({
  title: '', description: '', contest_type: 'ICPC',
  start_time: null as Date | null,
  end_time:   null as Date | null,
  freeze_time: null as Date | null,
  is_public: true, allow_late_register: false,
})
const rules = {
  title:      [{ required: true, message: '请填写比赛名称', trigger: 'blur' }],
  start_time: [{ required: true, message: '请选择开始时间', trigger: 'change' }],
  end_time:   [{ required: true, message: '请选择结束时间', trigger: 'change' }],
}

async function handleCreate() {
  await formRef.value?.validate()
  creating.value = true
  try {
    await contestApi.create({
      ...form,
      start_time:  form.start_time?.toISOString(),
      end_time:    form.end_time?.toISOString(),
      freeze_time: form.freeze_time?.toISOString() ?? null,
    } as any)
    createDialog.value = false
    ElMessage.success('比赛创建成功')
    fetch()
  } finally { creating.value = false }
}

// ─── Problem-linking dialog ───────────────────────────────────────────────
const problemsDialog     = ref(false)
const problemsTarget     = ref<Contest | null>(null)
const linkedProblems     = ref<ContestProblemSummary[]>([])
const loadingLinked      = ref(false)
const allProblems        = ref<Problem[]>([])
const loadingAllProblems = ref(false)
const adding             = ref(false)

const addForm = reactive({
  problem_id: null as number | null,
  label:      '',
  max_score:  100,
})

// Hide problems already linked from the dropdown
const availableProblems = computed(() => {
  const linkedIds = new Set(linkedProblems.value.map(p => p.problem_id))
  return allProblems.value.filter(p => !linkedIds.has(p.id))
})

async function openProblems(row: Contest) {
  problemsTarget.value = row
  problemsDialog.value = true
  addForm.problem_id   = null
  addForm.label        = suggestNextLabel()
  addForm.max_score    = 100
  await Promise.all([refreshLinked(), loadAllProblems()])
}

async function refreshLinked() {
  if (!problemsTarget.value) return
  loadingLinked.value = true
  try {
    const data = await contestApi.getProblems(problemsTarget.value.id)
    linkedProblems.value = data.problems ?? []
    addForm.label = suggestNextLabel()
  } finally { loadingLinked.value = false }
}

async function loadAllProblems() {
  if (allProblems.value.length > 0) return // cache
  loadingAllProblems.value = true
  try {
    // Load up to 200 problems — paginate if you have more
    const data = await problemApi.list(1, 200)
    allProblems.value = data.problems ?? []
  } finally { loadingAllProblems.value = false }
}

// Suggest the next label: A, B, C, …, Z, AA, AB, …
function suggestNextLabel(): string {
  const used = new Set(linkedProblems.value.map(p => p.label))
  for (let i = 0; i < 52; i++) {
    const ch = String.fromCharCode(65 + i)
    if (!used.has(ch)) return ch
  }
  return ''
}

async function handleAddProblem() {
  if (!problemsTarget.value || !addForm.problem_id || !addForm.label) return
  adding.value = true
  try {
    await contestApi.addProblem(problemsTarget.value.id, {
      problem_id: addForm.problem_id,
      label:      addForm.label,
      max_score:  addForm.max_score,
    })
    ElMessage.success('题目已添加')
    addForm.problem_id = null
    await refreshLinked()
  } finally { adding.value = false }
}

async function handleRemoveProblem(row: ContestProblemSummary) {
  if (!problemsTarget.value) return
  try {
    await ElMessageBox.confirm(
      `确认将题目 ${row.label} "${row.title}" 从比赛中移除？`,
      '确认移除',
      { type: 'warning', confirmButtonText: '移除', cancelButtonText: '取消' }
    )
  } catch { return }

  try {
    await contestApi.removeProblem(problemsTarget.value.id, row.problem_id)
    ElMessage.success('题目已移除')
    await refreshLinked()
  } catch { /* interceptor handles message */ }
}

// ─── Helpers ─────────────────────────────────────────────────────────────
const fmt = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')
function statusType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ running: 'success', frozen: 'warning', ended: 'info', ready: '', draft: 'info' } as any)[s] ?? ''
}
function statusLabel(s: string) {
  return { running: '进行中', frozen: '封榜', ended: '已结束', ready: '即将开始', draft: '草稿' }[s] ?? s
}

onMounted(fetch)
</script>

<style scoped>
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-header h2 { margin: 0; }
.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }

.section-sub {
  font-size: 13px;
  font-weight: 600;
  color: var(--oj-text-2);
  margin: 0 0 8px;
  padding-left: 2px;
}

.label-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px; height: 26px;
  background: var(--oj-primary);
  color: #fff;
  border-radius: 50%;
  font-weight: 700;
  font-size: 12px;
}

.add-row {
  display: flex;
  gap: 10px;
  align-items: center;
}
.empty-hint { color: var(--oj-text-3); font-size: 13px; }
</style>
