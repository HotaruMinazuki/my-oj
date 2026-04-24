<template>
  <div class="page oj-fade-in">
    <!-- Header -->
    <div class="pg-head">
      <div>
        <h2 class="pg-title">题库</h2>
        <p class="pg-sub">共 {{ total }} 道题目</p>
      </div>
      <el-input
        v-model="search"
        placeholder="搜索题目…"
        style="width:260px"
        clearable
        :prefix-icon="Search"
        @change="fetchProblems"
        @clear="fetchProblems"
      />
    </div>

    <el-card shadow="never">
      <el-table :data="problems" stripe v-loading="loading" style="width:100%">
        <el-table-column label="#" prop="id" width="72" align="center">
          <template #default="{ row }">
            <span class="prob-id">{{ row.id }}</span>
          </template>
        </el-table-column>
        <el-table-column label="题目名称" min-width="240">
          <template #default="{ row }">
            <router-link :to="`/problems/${row.id}`" class="link-text prob-title">
              {{ row.title }}
            </router-link>
          </template>
        </el-table-column>
        <el-table-column label="评测类型" width="110" align="center">
          <template #default="{ row }">
            <el-tag size="small" :type="judgeTagType(row.judge_type)" effect="light">
              {{ judgeLabel(row.judge_type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="时限" width="96" align="center">
          <template #default="{ row }">
            <span class="limit-val">{{ row.time_limit_ms }}ms</span>
          </template>
        </el-table-column>
        <el-table-column label="内存" width="96" align="center">
          <template #default="{ row }">
            <span class="limit-val">{{ Math.round(row.mem_limit_kb / 1024) }}MB</span>
          </template>
        </el-table-column>
        <el-table-column label="" width="80" align="center">
          <template #default="{ row }">
            <router-link :to="`/problems/${row.id}`">
              <el-button size="small" type="primary" plain>做题</el-button>
            </router-link>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="size"
          :total="total"
          layout="prev, pager, next, total"
          background
          @change="fetchProblems"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { problemApi } from '@/api/http'
import type { Problem } from '@/types'

const problems = ref<Problem[]>([])
const loading  = ref(false)
const search   = ref('')
const page     = ref(1)
const size     = ref(20)
const total    = ref(0)

async function fetchProblems() {
  loading.value = true
  try {
    const data      = await problemApi.list(page.value, size.value)
    problems.value  = data.problems ?? []
    total.value     = data.total    ?? 0
  } finally {
    loading.value = false
  }
}

function judgeLabel(t: string) {
  return { standard: '标准', special: '特判', interactive: '交互', communication: '通信' }[t] ?? t
}
function judgeTagType(t: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ special: 'warning', interactive: 'success', communication: 'danger' } as any)[t] ?? ''
}

onMounted(fetchProblems)
</script>

<style scoped>
.pg-head  { display: flex; align-items: flex-end; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
.pg-title { margin: 0; font-size: 24px; font-weight: 700; }
.pg-sub   { margin: 4px 0 0; color: var(--oj-text-3); font-size: 13px; }

.prob-id    { font-variant-numeric: tabular-nums; color: var(--oj-text-3); font-size: 13px; }
.prob-title { font-weight: 500; }
.limit-val  { color: var(--oj-text-2); font-size: 13px; font-variant-numeric: tabular-nums; }

.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }
</style>
