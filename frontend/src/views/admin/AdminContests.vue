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
        <el-table-column label="操作" width="380" align="center">
          <template #default="{ row }">
            <el-button size="small" type="primary" plain @click="openProblems(row)">
              管理题目
            </el-button>
            <router-link :to="`/contests/${row.id}`">
              <el-button size="small" plain>查看</el-button>
            </router-link>
            <el-button size="small" plain :loading="exportingId === row.id" @click="exportXml(row)">
              滚榜XML
            </el-button>
            <el-button
              v-if="row.status === 'ended'"
              size="small"
              type="warning"
              :loading="revealingId === row.id"
              @click="reveal(row)"
            >
              解榜
            </el-button>
            <el-button size="small" type="danger" plain @click="handleDeleteContest(row)">
              删除
            </el-button>
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
        description="题目在本场比赛中创建：比赛进行中仅参赛者可见，比赛结束后自动进入题库公开。创建后请为每题上传测试数据，否则无法评测。"
      />

      <!-- 已有题目 -->
      <div class="section-sub">本场题目（{{ linkedProblems.length }}）</div>
      <el-table
        :data="linkedProblems"
        v-loading="loadingLinked"
        size="small"
        border
        style="margin-bottom:20px"
      >
        <el-table-column label="Label" prop="label" width="70" align="center">
          <template #default="{ row }">
            <span class="label-badge">{{ row.label }}</span>
          </template>
        </el-table-column>
        <el-table-column label="#"      prop="problem_id" width="56" align="center" />
        <el-table-column label="题目"   prop="title" min-width="180" />
        <el-table-column label="分值"   prop="max_score" width="70" align="center" />
        <el-table-column label="操作"   width="210" align="center">
          <template #default="{ row }">
            <el-button size="small" plain @click="openEditProblem(row)">编辑</el-button>
            <el-button size="small" plain @click="openUpload(row)">数据</el-button>
            <el-button
              size="small"
              type="danger"
              link
              :icon="Delete"
              @click="handleDeleteProblem(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <span class="empty-hint">暂无题目，请在下方新建</span>
        </template>
      </el-table>

      <!-- 新建题目 -->
      <div class="section-sub">在本场比赛中新建题目</div>
      <el-form :model="newProb" label-width="92px" class="newprob-form">
        <el-form-item label="Label">
          <el-input v-model="newProb.label" maxlength="4" style="width:120px" placeholder="如 A" />
        </el-form-item>
        <el-form-item label="题目名称">
          <el-input v-model="newProb.title" placeholder="题目标题" />
        </el-form-item>
        <el-form-item label="题面">
          <el-input v-model="newProb.statement" type="textarea" :rows="4" placeholder="支持 Markdown" />
        </el-form-item>
        <el-form-item label="评测类型">
          <el-select v-model="newProb.judge_type" style="width:170px">
            <el-option label="标准（diff）" value="standard" />
            <el-option label="Special Judge" value="special" />
            <el-option label="交互题" value="interactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="时限 / 内存">
          <el-input-number v-model="newProb.time_limit_ms" :min="100" :max="30000" :step="500" />
          <span class="unit">ms</span>
          <el-input-number v-model="newProb.mem_limit_kb" :min="16384" :max="1048576" :step="65536" style="margin-left:14px" />
          <span class="unit">KB</span>
        </el-form-item>
        <el-form-item label="分值">
          <el-input-number v-model="newProb.max_score" :min="1" :max="1000" :step="10" />
          <span class="unit">OI/IOI 计分用</span>
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :icon="Plus"
            :loading="creatingProb"
            :disabled="!newProb.title || !newProb.label"
            @click="handleCreateProblem"
          >
            创建并加入比赛
          </el-button>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="problemsDialog = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- 编辑题目对话框 -->
    <el-dialog
      v-model="editVisible"
      :title="`编辑题目 — ${editProb.label}: #${editProb.id}`"
      width="640px"
      append-to-body
    >
      <el-form :model="editProb" label-width="92px" v-loading="editLoading">
        <el-form-item label="题目名称">
          <el-input v-model="editProb.title" />
        </el-form-item>
        <el-form-item label="题面">
          <el-input v-model="editProb.statement" type="textarea" :rows="6" placeholder="支持 Markdown" />
        </el-form-item>
        <el-form-item label="评测类型">
          <el-select v-model="editProb.judge_type" style="width:170px">
            <el-option label="标准（diff）" value="standard" />
            <el-option label="Special Judge" value="special" />
            <el-option label="交互题" value="interactive" />
          </el-select>
        </el-form-item>
        <el-form-item label="时限 / 内存">
          <el-input-number v-model="editProb.time_limit_ms" :min="100" :max="30000" :step="500" />
          <span class="unit">ms</span>
          <el-input-number v-model="editProb.mem_limit_kb" :min="16384" :max="1048576" :step="65536" style="margin-left:14px" />
          <span class="unit">KB</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingEdit" @click="saveEditProblem">保存</el-button>
      </template>
    </el-dialog>

    <!-- 测试数据对话框 -->
    <el-dialog
      v-model="uploadVisible"
      :title="`测试数据 — ${uploadTarget?.label}: ${uploadTarget?.title ?? ''}`"
      width="520px"
      append-to-body
    >
      <div class="section-sub">当前测试数据（{{ currentCases.length }} 个测试点）</div>
      <el-table
        v-if="currentCases.length"
        :data="currentCases"
        size="small"
        border
        max-height="200"
        style="margin-bottom:16px"
      >
        <el-table-column label="#" prop="ordinal" width="60" align="center" />
        <el-table-column label="输入" prop="input_path" min-width="100" />
        <el-table-column label="输出" prop="output_path" min-width="100" />
        <el-table-column label="分值" prop="score" width="70" align="center" />
      </el-table>
      <el-empty v-else :image-size="60" description="暂无测试数据" style="padding:8px 0" />

      <el-alert type="warning" :closable="false" show-icon style="margin-bottom:16px">
        重新上传将<strong>覆盖</strong>现有全部测试数据。压缩包直接包含 1.in, 1.out, 2.in, 2.out … 的 .zip。
      </el-alert>
      <el-upload
        drag
        action="#"
        accept=".zip"
        :auto-upload="false"
        :on-change="onUploadChange"
        :limit="1"
      >
        <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
        <div class="el-upload__text">拖拽 .zip 文件到此，或 <em>点击选择</em></div>
      </el-upload>
      <template #footer>
        <el-button @click="uploadVisible = false">关闭</el-button>
        <el-button type="primary" :loading="uploading" @click="doUpload">上传并覆盖</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Plus, Delete, UploadFilled } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, UploadFile } from 'element-plus'
import dayjs from 'dayjs'
import { contestApi, problemApi, adminApi } from '@/api/http'
import type { TestCaseInfo } from '@/api/http'
import type { Contest, ContestProblemSummary } from '@/types'

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

// ─── Resolver XML export ──────────────────────────────────────────────────
const exportingId = ref<number | null>(null)
async function exportXml(row: Contest) {
  exportingId.value = row.id
  try {
    await adminApi.exportResolverXml(row.id)
    ElMessage.success('已导出 event-feed.xml，可直接喂给滚榜工具')
  } catch {
    ElMessage.error('导出失败')
  } finally {
    exportingId.value = null
  }
}

// ─── 解榜 (reveal frozen scoreboard) ───────────────────────────────────────
const revealingId = ref<number | null>(null)
async function reveal(row: Contest) {
  try {
    await ElMessageBox.confirm(
      `确认对「${row.title}」解榜？解榜后所有人都能看到封榜期间的最终结果，此操作不可撤销。`,
      '确认解榜',
      { type: 'warning', confirmButtonText: '解榜', cancelButtonText: '取消' }
    )
  } catch { return }

  revealingId.value = row.id
  try {
    await adminApi.revealContest(row.id)
    ElMessage.success('已解榜，排行榜已对所有人公开最终结果')
  } catch {
    ElMessage.error('解榜失败')
  } finally {
    revealingId.value = null
  }
}

// ─── Delete contest ────────────────────────────────────────────────────────
async function handleDeleteContest(row: Contest) {
  try {
    await ElMessageBox.confirm(
      `确认删除比赛「${row.title}」？比赛题目、报名记录、排行榜将一并移除；选手在本场的提交会转为练习提交保留。`,
      '确认删除比赛',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch { return }
  try {
    await contestApi.remove(row.id)
    ElMessage.success('比赛已删除')
    fetch()
  } catch { /* interceptor toasts */ }
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

// ─── Problem management dialog ────────────────────────────────────────────
const problemsDialog = ref(false)
const problemsTarget = ref<Contest | null>(null)
const linkedProblems = ref<ContestProblemSummary[]>([])
const loadingLinked  = ref(false)
const creatingProb   = ref(false)

const newProb = reactive({
  label:         '',
  title:         '',
  statement:     '',
  judge_type:    'standard',
  time_limit_ms: 2000,
  mem_limit_kb:  262144,
  max_score:     100,
})

async function openProblems(row: Contest) {
  problemsTarget.value = row
  problemsDialog.value = true
  resetNewProb()
  await refreshLinked()
}

function resetNewProb() {
  newProb.label         = suggestNextLabel()
  newProb.title         = ''
  newProb.statement     = ''
  newProb.judge_type    = 'standard'
  newProb.time_limit_ms = 2000
  newProb.mem_limit_kb  = 262144
  newProb.max_score     = 100
}

async function refreshLinked() {
  if (!problemsTarget.value) return
  loadingLinked.value = true
  try {
    const data = await contestApi.getProblems(problemsTarget.value.id)
    linkedProblems.value = data.problems ?? []
    newProb.label = suggestNextLabel()
  } finally { loadingLinked.value = false }
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

async function handleCreateProblem() {
  if (!problemsTarget.value || !newProb.title || !newProb.label) return
  creatingProb.value = true
  try {
    await contestApi.createProblem(problemsTarget.value.id, {
      label:         newProb.label,
      title:         newProb.title,
      statement:     newProb.statement,
      judge_type:    newProb.judge_type,
      time_limit_ms: newProb.time_limit_ms,
      mem_limit_kb:  newProb.mem_limit_kb,
      max_score:     newProb.max_score,
    })
    ElMessage.success('题目已创建并加入比赛，记得上传测试数据')
    await refreshLinked()
    resetNewProb()
  } finally { creatingProb.value = false }
}

// ─── Edit an in-contest problem ────────────────────────────────────────────
const editVisible = ref(false)
const editLoading = ref(false)
const savingEdit  = ref(false)
const editProb = reactive({
  id: 0, label: '', title: '', statement: '',
  judge_type: 'standard', time_limit_ms: 2000, mem_limit_kb: 262144,
})

async function openEditProblem(row: ContestProblemSummary) {
  editProb.id = row.problem_id
  editProb.label = row.label
  editVisible.value = true
  editLoading.value = true
  try {
    const p = await problemApi.get(row.problem_id)
    editProb.title         = p.title
    editProb.statement     = p.statement ?? ''
    editProb.judge_type    = p.judge_type
    editProb.time_limit_ms = p.time_limit_ms
    editProb.mem_limit_kb  = p.mem_limit_kb
  } finally { editLoading.value = false }
}

async function saveEditProblem() {
  if (!editProb.title) { ElMessage.warning('请填写题目名称'); return }
  savingEdit.value = true
  try {
    await problemApi.update(editProb.id, {
      title:         editProb.title,
      statement:     editProb.statement,
      judge_type:    editProb.judge_type as any,
      time_limit_ms: editProb.time_limit_ms,
      mem_limit_kb:  editProb.mem_limit_kb,
      is_public:     false, // contest problems stay private until the contest ends
    })
    editVisible.value = false
    ElMessage.success('已保存')
    await refreshLinked()
  } finally { savingEdit.value = false }
}

// ─── Testcase data (view current + overwrite upload) ───────────────────────
const uploadVisible = ref(false)
const uploadTarget  = ref<ContestProblemSummary | null>(null)
const uploadFile    = ref<File | null>(null)
const uploading     = ref(false)
const currentCases  = ref<TestCaseInfo[]>([])

async function openUpload(row: ContestProblemSummary) {
  uploadTarget.value = row
  uploadFile.value   = null
  currentCases.value = []
  uploadVisible.value = true
  try {
    const data = await problemApi.getTestcases(row.problem_id)
    currentCases.value = data.test_cases ?? []
  } catch { /* ignore */ }
}

function onUploadChange(file: UploadFile) {
  uploadFile.value = file.raw ?? null
}

async function doUpload() {
  if (!uploadTarget.value) return
  if (!uploadFile.value) { ElMessage.warning('请先选择 .zip 文件'); return }
  uploading.value = true
  try {
    const res = await problemApi.uploadTestcases(uploadTarget.value.problem_id, uploadFile.value)
    ElMessage.success(`上传成功，已登记 ${res?.test_cases ?? 0} 个测试点`)
    const data = await problemApi.getTestcases(uploadTarget.value.problem_id)
    currentCases.value = data.test_cases ?? []
    uploadFile.value = null
  } catch {
    ElMessage.error('上传失败')
  } finally {
    uploading.value = false
  }
}

async function handleDeleteProblem(row: ContestProblemSummary) {
  try {
    await ElMessageBox.confirm(
      `确认删除题目 ${row.label} "${row.title}"？将一并删除其测试数据与所有提交记录，不可恢复。`,
      '确认删除',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch { return }

  try {
    await problemApi.remove(row.problem_id)
    ElMessage.success('题目已删除')
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

.newprob-form { max-width: 560px; }
.unit { color: var(--oj-text-3); font-size: 12px; margin-left: 6px; }
.empty-hint { color: var(--oj-text-3); font-size: 13px; }
</style>
