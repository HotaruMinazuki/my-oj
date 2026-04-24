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
        <el-table-column label="操作" width="200" align="center">
          <template #default="{ row }">
            <el-button size="small" @click="openUpload(row)">上传测试数据</el-button>
            <router-link :to="`/problems/${row.id}`">
              <el-button size="small" type="primary" plain>预览</el-button>
            </router-link>
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

    <!-- 上传测试数据对话框 -->
    <el-dialog v-model="uploadDialog" :title="`上传测试数据 — ${uploadTarget?.title}`" width="480px">
      <el-alert type="info" :closable="false" show-icon style="margin-bottom:16px">
        压缩包格式：直接包含 1.in, 1.out, 2.in, 2.out … 的 .zip 文件
      </el-alert>
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
        <el-button @click="uploadDialog = false">取消</el-button>
        <el-button type="primary" :loading="uploading" @click="handleUpload">上传</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Plus, UploadFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import type { FormInstance, UploadFile } from 'element-plus'
import { problemApi } from '@/api/http'
import type { Problem } from '@/types'

const problems      = ref<Problem[]>([])
const loading       = ref(false)
const page          = ref(1)
const total         = ref(0)

const createDialog  = ref(false)
const creating      = ref(false)
const formRef       = ref<FormInstance>()
const form = reactive({
  title: '', statement: '', judge_type: 'standard',
  time_limit_ms: 2000, mem_limit_kb: 262144, is_public: false,
})
const rules = { title: [{ required: true, message: '请填写标题', trigger: 'blur' }] }

const uploadDialog  = ref(false)
const uploadTarget  = ref<Problem | null>(null)
const uploadFile    = ref<File | null>(null)
const uploading     = ref(false)

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

function openUpload(row: Problem) {
  uploadTarget.value = row
  uploadFile.value   = null
  uploadDialog.value = true
}
function onFileChange(file: UploadFile) {
  uploadFile.value = file.raw ?? null
}
async function handleUpload() {
  if (!uploadFile.value) { ElMessage.warning('请先选择文件'); return }
  uploading.value = true
  try {
    await problemApi.uploadTestcases(uploadTarget.value.id, uploadFile.value)
    uploadDialog.value = false
    ElMessage.success('测试数据上传成功')
  } finally { uploading.value = false }
}

onMounted(fetch)
</script>

<style scoped>
.page-header { display:flex; align-items:center; justify-content:space-between; margin-bottom:16px; }
.page-header h2 { margin:0; }
.pagination { display:flex; justify-content:flex-end; margin-top:16px; }
</style>
