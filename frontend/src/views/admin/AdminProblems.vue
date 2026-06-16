<template>
  <div>
    <div class="page-header">
      <h2>题目管理</h2>
      <el-button type="primary" :icon="Plus" @click="createDialog = true">新建题目</el-button>
    </div>

    <!-- 题目列表 -->
    <el-card shadow="never" style="margin-bottom:20px">
      <el-table :data="problems" stripe v-loading="loading">
        <el-table-column label="ID" prop="id" width="70" />
        <el-table-column label="标题" prop="title" min-width="200" />
        <el-table-column label="评测类型" width="100">
          <template #default="{ row }">
            <el-tag size="small">{{ row.judge_type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="公开" width="80" align="center">
          <template #default="{ row }">
            <el-tag :type="row.is_public ? 'success' : 'info'" size="small">
              {{ row.is_public ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="320" align="center">
          <template #default="{ row }">
            <el-button size="small" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" @click="openUpload(row)">数据</el-button>
            <router-link :to="`/problems/${row.id}`">
              <el-button size="small" type="primary" plain>预览</el-button>
            </router-link>
            <el-button size="small" type="danger" plain @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination">
        <el-pagination v-model:current-page="page" :total="total" layout="prev,pager,next,total" @current-change="fetch" />
      </div>
    </el-card>

    <!-- 新建题目对话框 -->
    <el-dialog v-model="createDialog" title="新建题目" width="680px">
      <el-form :model="form" :rules="rules" ref="formRef" label-width="100px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" />
        </el-form-item>
        <el-form-item label="评测类型">
          <el-select v-model="form.judge_type" style="width:100%">
            <el-option label="标准（diff）" value="standard" />
            <el-option label="Special Judge" value="special" />
            <el-option label="交互题" value="interactive" />
            <el-option label="通信题" value="communication" />
          </el-select>
        </el-form-item>
        <el-form-item label="时限(ms)">
          <el-input-number v-model="form.time_limit_ms" :min="100" :max="30000" :step="500" />
        </el-form-item>
        <el-form-item label="内存(KB)">
          <el-input-number v-model="form.mem_limit_kb" :min="16384" :max="1048576" :step="65536" />
        </el-form-item>
        <el-form-item label="公开">
          <el-switch v-model="form.is_public" />
        </el-form-item>
        <el-form-item label="题面(Markdown)">
          <el-input v-model="form.statement" type="textarea" :rows="8" placeholder="支持 Markdown..." />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="handleCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 编辑题目对话框 -->
    <el-dialog v-model="editDialog" :title="`编辑题目 — #${editForm.id}`" width="680px">
      <el-form :model="editForm" label-width="100px" v-loading="editLoading">
        <el-form-item label="标题">
          <el-input v-model="editForm.title" />
        </el-form-item>
        <el-form-item label="评测类型">
          <el-select v-model="editForm.judge_type" style="width:100%">
            <el-option label="标准（diff）" value="standard" />
            <el-option label="Special Judge" value="special" />
            <el-option label="交互题" value="interactive" />
            <el-option label="通信题" value="communication" />
          </el-select>
        </el-form-item>
        <el-form-item label="时限(ms)">
          <el-input-number v-model="editForm.time_limit_ms" :min="100" :max="30000" :step="500" />
        </el-form-item>
        <el-form-item label="内存(KB)">
          <el-input-number v-model="editForm.mem_limit_kb" :min="16384" :max="1048576" :step="65536" />
        </el-form-item>
        <el-form-item label="公开">
          <el-switch v-model="editForm.is_public" />
        </el-form-item>
        <el-form-item label="题面(Markdown)">
          <el-input v-model="editForm.statement" type="textarea" :rows="8" placeholder="支持 Markdown..." />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="editing" @click="handleEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 测试数据对话框 -->
    <el-dialog v-model="uploadDialog" :title="`测试数据 — ${uploadTarget?.title}`" width="520px">
      <!-- 当前数据 -->
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

      <div class="score-row">
        <span class="score-label">本题满分(分值)</span>
        <el-input-number v-model="uploadScore" :min="1" :max="100000" :step="10" />
        <span class="score-hint">OI/IOI 计分：满分平均分配到各测试点（整数，余数给最后一个点）</span>
      </div>

      <el-upload
        drag
        action="#"
        accept=".zip"
        :auto-upload="false"
        :on-change="onFileChange"
        :limit="1"
      >
        <el-icon class="el-icon--upload"><UploadFilled /></el-icon>
        <div class="el-upload__text">拖拽 .zip 文件到此，或 <em>点击选择</em></div>
      </el-upload>
      <template #footer>
        <el-button @click="uploadDialog = false">关闭</el-button>
        <el-button type="primary" :loading="uploading" @click="handleUpload">上传并覆盖</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Plus, UploadFilled } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, UploadFile } from 'element-plus'
import { problemApi } from '@/api/http'
import type { TestCaseInfo } from '@/api/http'
import type { Problem, JudgeType } from '@/types'

const problems      = ref<Problem[]>([])
const loading       = ref(false)
const page          = ref(1)
const total         = ref(0)

const createDialog  = ref(false)
const creating      = ref(false)
const formRef       = ref<FormInstance>()
const form = reactive({
  title: '', statement: '', judge_type: 'standard' as JudgeType,
  time_limit_ms: 2000, mem_limit_kb: 262144, is_public: false,
})
const rules = { title: [{ required: true, message: '请填写标题', trigger: 'blur' }] }

const uploadDialog  = ref(false)
const uploadTarget  = ref<Problem | null>(null)
const uploadFile    = ref<File | null>(null)
const uploading     = ref(false)
const currentCases  = ref<TestCaseInfo[]>([])
const uploadScore   = ref(100)

// ── Edit problem ─────────────────────────────────────────────────────────────
const editDialog  = ref(false)
const editing     = ref(false)
const editLoading = ref(false)
const editForm = reactive({
  id: 0, title: '', statement: '', judge_type: 'standard' as JudgeType,
  time_limit_ms: 2000, mem_limit_kb: 262144, is_public: false,
})

async function fetch() {
  loading.value = true
  try {
    const data = await problemApi.list(page.value, 20)
    problems.value = data.problems ?? []
    total.value    = data.total    ?? 0
  } finally { loading.value = false }
}

async function handleCreate() {
  await formRef.value?.validate()
  creating.value = true
  try {
    await problemApi.create({ ...form })
    createDialog.value = false
    ElMessage.success('题目创建成功')
    fetch()
  } finally { creating.value = false }
}

async function openEdit(row: Problem) {
  editForm.id = row.id
  editDialog.value = true
  editLoading.value = true
  try {
    // Fetch the full problem (list rows omit the statement).
    const p = await problemApi.get(row.id)
    editForm.title         = p.title
    editForm.statement     = p.statement ?? ''
    editForm.judge_type    = p.judge_type
    editForm.time_limit_ms = p.time_limit_ms
    editForm.mem_limit_kb  = p.mem_limit_kb
    editForm.is_public     = p.is_public
  } finally { editLoading.value = false }
}

async function handleEdit() {
  if (!editForm.title) { ElMessage.warning('请填写标题'); return }
  editing.value = true
  try {
    await problemApi.update(editForm.id, {
      title:         editForm.title,
      statement:     editForm.statement,
      judge_type:    editForm.judge_type,
      time_limit_ms: editForm.time_limit_ms,
      mem_limit_kb:  editForm.mem_limit_kb,
      is_public:     editForm.is_public,
    })
    editDialog.value = false
    ElMessage.success('已保存')
    fetch()
  } finally { editing.value = false }
}

async function handleDelete(row: Problem) {
  try {
    await ElMessageBox.confirm(
      `确认删除题目「${row.title}」？将一并删除其测试数据、比赛关联与所有提交记录，不可恢复。`,
      '确认删除',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch { return }
  try {
    await problemApi.remove(row.id)
    ElMessage.success('题目已删除')
    fetch()
  } catch { /* interceptor toasts */ }
}

async function openUpload(row: Problem) {
  uploadTarget.value = row
  uploadFile.value   = null
  currentCases.value = []
  uploadScore.value  = 100
  uploadDialog.value = true
  try {
    const data = await problemApi.getTestcases(row.id)
    currentCases.value = data.test_cases ?? []
    // Pre-fill 满分 with the existing total so a re-upload keeps it unless changed.
    const sum = currentCases.value.reduce((acc, t) => acc + (t.score ?? 0), 0)
    if (sum > 0) uploadScore.value = sum
  } catch { /* ignore */ }
}
function onFileChange(file: UploadFile) {
  uploadFile.value = file.raw ?? null
}
async function handleUpload() {
  if (!uploadFile.value) { ElMessage.warning('请先选择文件'); return }
  if (!uploadTarget.value) { ElMessage.warning('目标题目丢失'); return }
  uploading.value = true
  try {
    const res = await problemApi.uploadTestcases(uploadTarget.value.id, uploadFile.value, uploadScore.value)
    ElMessage.success(`上传成功：${res?.test_cases ?? 0} 个测试点，满分 ${res?.total_score ?? uploadScore.value} 分`)
    // Refresh current-data view to reflect the overwrite.
    const data = await problemApi.getTestcases(uploadTarget.value.id)
    currentCases.value = data.test_cases ?? []
    uploadFile.value = null
  } finally { uploading.value = false }
}

onMounted(fetch)
</script>

<style scoped>
.page-header { display:flex; align-items:center; justify-content:space-between; margin-bottom:16px; }
.page-header h2 { margin:0; }
.pagination { display:flex; justify-content:flex-end; margin-top:16px; }
.section-sub { font-size:13px; font-weight:600; color:var(--oj-text-2); margin:0 0 8px; }
.score-row { display:flex; align-items:center; flex-wrap:wrap; gap:10px; margin-bottom:16px; }
.score-label { font-size:13px; font-weight:600; color:var(--oj-text-2); }
.score-hint { font-size:12px; color:var(--oj-text-3); }
</style>
