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
        <el-table-column label="操作" width="200" align="center">
          <template #default="{ row }">
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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Plus } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import type { FormInstance } from 'element-plus'
import dayjs from 'dayjs'
import { contestApi } from '@/api/http'
import type { Contest } from '@/types'

const contests = ref<Contest[]>([])
const loading  = ref(false)
const page     = ref(1)
const total    = ref(0)

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

async function fetch() {
  loading.value = true
  try {
    const data = await contestApi.list(page.value, 20)
    contests.value = data.contests ?? []
    total.value    = data.total    ?? 0
  } finally { loading.value = false }
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
    })
    createDialog.value = false
    ElMessage.success('比赛创建成功')
    fetch()
  } finally { creating.value = false }
}

const fmt = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')
function statusType(s: string) {
  return { running:'success', frozen:'warning', ended:'info', ready:'', draft:'info' }[s] ?? ''
}
function statusLabel(s: string) {
  return { running:'进行中', frozen:'封榜', ended:'已结束', ready:'即将开始', draft:'草稿' }[s] ?? s
}

onMounted(fetch)
</script>

<style scoped>
.page-header { display:flex; align-items:center; justify-content:space-between; margin-bottom:16px; }
.page-header h2 { margin:0; }
.pagination { display:flex; justify-content:flex-end; margin-top:16px; }
</style>
