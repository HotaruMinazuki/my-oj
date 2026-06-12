<template>
  <div>
    <div class="page-header">
      <h2>提交记录</h2>
      <div class="filters">
        <el-input
          v-model.number="filterUserId"
          placeholder="用户 ID"
          clearable
          style="width: 110px"
          @keyup.enter="doFilter"
          @clear="doFilter"
        />
        <el-input
          v-model.number="filterProblemId"
          placeholder="题目 ID"
          clearable
          style="width: 110px"
          @keyup.enter="doFilter"
          @clear="doFilter"
        />
        <el-select v-model="filterStatus" placeholder="状态" clearable style="width: 140px" @change="doFilter">
          <el-option v-for="(label, s) in STATUS_CN" :key="s" :label="label" :value="s" />
        </el-select>
        <el-button type="primary" :icon="Search" @click="doFilter">筛选</el-button>
      </div>
    </div>

    <el-card shadow="never">
      <el-table :data="submissions" stripe v-loading="loading" @row-click="openSubmission" class="clickable">
        <el-table-column label="#" prop="id" width="80" />
        <el-table-column label="用户" width="140">
          <template #default="{ row }">
            <router-link :to="`/users/${row.user_id}`" class="link-text" @click.stop>
              {{ row.username }}
            </router-link>
          </template>
        </el-table-column>
        <el-table-column label="题目" min-width="160">
          <template #default="{ row }">
            <router-link :to="`/problems/${row.problem_id}`" class="link-text" @click.stop>
              {{ row.problem_title }}
            </router-link>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="130">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small" effect="light">
              {{ STATUS_CN[row.status] ?? row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="得分" prop="score" width="70" align="center" />
        <el-table-column label="用时" width="90">
          <template #default="{ row }">{{ row.time_used_ms }}ms</template>
        </el-table-column>
        <el-table-column label="语言" prop="language" width="90" />
        <el-table-column label="比赛" width="80" align="center">
          <template #default="{ row }">
            <router-link v-if="row.contest_id" :to="`/contests/${row.contest_id}`" class="link-text" @click.stop>
              #{{ row.contest_id }}
            </router-link>
            <span v-else class="empty-val">练习</span>
          </template>
        </el-table-column>
        <el-table-column label="提交时间" width="170">
          <template #default="{ row }">{{ fmt(row.created_at) }}</template>
        </el-table-column>
      </el-table>
      <div class="pagination">
        <el-pagination
          v-model:current-page="page"
          :total="total"
          :page-size="20"
          layout="prev,pager,next,total"
          @current-change="fetch"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Search } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { adminApi } from '@/api/http'
import type { SubmissionListItem } from '@/types'

const route  = useRoute()
const router = useRouter()

const submissions = ref<SubmissionListItem[]>([])
const loading     = ref(false)
const page        = ref(1)
const total       = ref(0)

const filterUserId    = ref<number | ''>('')
const filterProblemId = ref<number | ''>('')
const filterStatus    = ref('')

const STATUS_CN: Record<string, string> = {
  Accepted: '通过', WrongAnswer: '答案错误',
  TimeLimitExceeded: '超时', MemoryLimitExceeded: '超内存',
  RuntimeError: '运行错误', CompileError: '编译错误',
  SystemError: '系统错误', Pending: '等待中',
  Judging: '评测中', Compiling: '编译中',
}

async function fetch() {
  loading.value = true
  try {
    const data = await adminApi.listSubmissions({
      page: page.value,
      size: 20,
      user_id:    filterUserId.value    || undefined,
      problem_id: filterProblemId.value || undefined,
      status:     filterStatus.value    || undefined,
    })
    submissions.value = data.submissions ?? []
    total.value       = data.total ?? 0
  } finally { loading.value = false }
}

function doFilter() {
  page.value = 1
  fetch()
}

function openSubmission(row: SubmissionListItem) {
  router.push(`/submissions/${row.id}`)
}

function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  if (s === 'Accepted') return 'success'
  if (['Pending', 'Judging', 'Compiling'].includes(s)) return 'warning'
  return 'danger'
}

const fmt = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')

onMounted(() => {
  // 支持从用户管理页带 ?user_id= 跳转过来直接过滤
  const uid = Number(route.query.user_id)
  if (uid > 0) filterUserId.value = uid
  fetch()
})
</script>

<style scoped>
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; gap: 16px; flex-wrap: wrap; }
.page-header h2 { margin: 0; }
.filters { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }
.empty-val  { color: var(--oj-text-3); font-size: 12px; }
.clickable :deep(.el-table__row) { cursor: pointer; }
</style>
