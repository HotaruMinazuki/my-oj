<template>
  <div>
    <div class="page-header">
      <h2>用户管理</h2>
      <el-input
        v-model="query"
        placeholder="搜索用户名 / 邮箱 / 单位"
        clearable
        style="width: 280px"
        :prefix-icon="Search"
        @keyup.enter="doSearch"
        @clear="doSearch"
      />
    </div>

    <el-card shadow="never">
      <el-table :data="users" stripe v-loading="loading">
        <el-table-column label="ID" prop="id" width="70" />
        <el-table-column label="用户名" min-width="140">
          <template #default="{ row }">
            <router-link :to="`/users/${row.id}`" class="link-text">{{ row.username }}</router-link>
          </template>
        </el-table-column>
        <el-table-column label="邮箱" min-width="200">
          <template #default="{ row }">
            <span v-if="row.email">{{ row.email }}</span>
            <span v-else class="empty-val">未绑定</span>
          </template>
        </el-table-column>
        <el-table-column label="单位" prop="organization" min-width="140">
          <template #default="{ row }">
            <span v-if="row.organization">{{ row.organization }}</span>
            <span v-else class="empty-val">—</span>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="100">
          <template #default="{ row }">
            <el-tag :type="row.role === 'admin' ? 'danger' : ''" size="small">
              {{ row.role === 'admin' ? '管理员' : '选手' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="注册时间" width="170">
          <template #default="{ row }">{{ fmt(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" align="center">
          <template #default="{ row }">
            <router-link :to="`/users/${row.id}`">
              <el-button size="small" type="primary" plain>查看主页</el-button>
            </router-link>
            <router-link :to="`/admin/submissions?user_id=${row.id}`">
              <el-button size="small" plain>提交</el-button>
            </router-link>
          </template>
        </el-table-column>
        <template #empty>
          <span class="empty-hint">没有匹配的用户</span>
        </template>
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
import { Search } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { adminApi } from '@/api/http'
import type { User } from '@/types'

const users   = ref<User[]>([])
const loading = ref(false)
const query   = ref('')
const page    = ref(1)
const total   = ref(0)

async function fetch() {
  loading.value = true
  try {
    const data = await adminApi.searchUsers(query.value.trim(), page.value, 20)
    users.value = data.users ?? []
    total.value = data.total ?? 0
  } finally { loading.value = false }
}

function doSearch() {
  page.value = 1
  fetch()
}

const fmt = (t: string) => dayjs(t).format('YYYY-MM-DD HH:mm')

onMounted(fetch)
</script>

<style scoped>
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; gap: 16px; flex-wrap: wrap; }
.page-header h2 { margin: 0; }
.pagination { display: flex; justify-content: flex-end; margin-top: 16px; }
.empty-val  { color: var(--oj-text-3); }
.empty-hint { color: var(--oj-text-3); font-size: 13px; }
</style>
