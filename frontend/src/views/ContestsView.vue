<template>
  <div class="page oj-fade-in">
    <!-- Page header -->
    <div class="pg-head">
      <div>
        <h2 class="pg-title">比赛列表</h2>
        <p class="pg-sub">ICPC / OI 赛制，实时排行榜</p>
      </div>
    </div>

    <!-- Filter tabs -->
    <div class="filter-bar">
      <el-radio-group v-model="filter" size="small" @change="applyFilter">
        <el-radio-button label="all">全部</el-radio-button>
        <el-radio-button label="running">进行中</el-radio-button>
        <el-radio-button label="ready">即将开始</el-radio-button>
        <el-radio-button label="ended">已结束</el-radio-button>
      </el-radio-group>
    </div>

    <!-- Contest cards -->
    <div v-loading="loading" style="min-height:120px">
      <el-empty v-if="!loading && filtered.length === 0" description="暂无比赛" />

      <div v-else class="contest-grid">
        <el-card
          v-for="c in filtered"
          :key="c.id"
          shadow="hover"
          class="contest-card"
        >
          <div class="cc-body">
            <div class="cc-left">
              <div class="cc-top">
                <el-tag :type="statusTagType(c.status)" size="small" effect="light" class="cc-status">
                  <span class="status-dot" :class="c.status" />{{ statusLabel(c.status) }}
                </el-tag>
                <el-tag size="small" effect="plain">{{ c.contest_type }}</el-tag>
              </div>
              <router-link :to="`/contests/${c.id}`" class="cc-title link-text">
                {{ c.title }}
              </router-link>
              <div class="cc-times">
                <el-icon><Clock /></el-icon>
                <span>{{ fmt(c.start_time) }}</span>
                <span class="sep">→</span>
                <span>{{ fmt(c.end_time) }}</span>
              </div>
              <!-- Countdown for upcoming / running -->
              <div v-if="c.status === 'ready'" class="cc-countdown ready-cd">
                <el-icon><Timer /></el-icon>
                距开始 <CountdownText :target="c.start_time" />
              </div>
              <div v-else-if="c.status === 'running' || c.status === 'frozen'" class="cc-countdown running-cd">
                <el-icon><Timer /></el-icon>
                距结束 <CountdownText :target="c.end_time" />
              </div>
            </div>
            <div class="cc-right">
              <router-link :to="`/contests/${c.id}`">
                <el-button
                  :type="c.status === 'running' || c.status === 'frozen' ? 'primary' : 'default'"
                  size="small"
                >
                  {{ c.status === 'ended' ? '查看' : '进入' }}
                </el-button>
              </router-link>
            </div>
          </div>
        </el-card>
      </div>
    </div>

    <!-- Pagination -->
    <div v-if="total > pageSize" class="pagination">
      <el-pagination
        v-model:current-page="page"
        :total="total"
        :page-size="pageSize"
        layout="prev, pager, next, total"
        background
        @current-change="fetchContests"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, defineComponent, h } from 'vue'
import { Clock, Timer } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { contestApi } from '@/api/http'
import type { Contest } from '@/types'
import { useCountdown } from '@/composables/useCountdown'

// ── Inline micro-component: single countdown display ──────────────────────
const CountdownText = defineComponent({
  props: { target: { type: String, required: true } },
  setup(props) {
    const t = ref(props.target)
    const { formatted, expired } = useCountdown(t)
    return () => h('strong', { class: 'cd-value' }, expired.value ? '已结束' : formatted.value)
  },
})

const contests = ref<Contest[]>([])
const loading  = ref(false)
const page     = ref(1)
const total    = ref(0)
const pageSize = 20
const filter   = ref<'all' | 'running' | 'ready' | 'ended'>('all')

const filtered = computed(() => {
  if (filter.value === 'all') return contests.value
  return contests.value.filter(c => {
    if (filter.value === 'running') return c.status === 'running' || c.status === 'frozen'
    return c.status === filter.value
  })
})

function applyFilter() {
  // Filter is local (no server-side filter in current API), just reset page
  page.value = 1
}

async function fetchContests() {
  loading.value = true
  try {
    const data = await contestApi.list(page.value, pageSize)
    contests.value = data.contests ?? []
    total.value    = data.total    ?? 0
  } finally {
    loading.value = false
  }
}

const fmt = (t: string) => dayjs(t).format('MM-DD HH:mm')

function statusLabel(s: string) {
  return { running: '进行中', frozen: '封榜', ended: '已结束', ready: '即将开始', draft: '草稿' }[s] ?? s
}

function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  const map: Record<string, '' | 'success' | 'warning' | 'info' | 'danger'> = {
    running: 'success', frozen: 'warning', ended: 'info', ready: '', draft: 'info',
  }
  return map[s] ?? ''
}

onMounted(fetchContests)
</script>

<style scoped>
.pg-head  { display: flex; justify-content: space-between; align-items: flex-end; margin-bottom: 20px; }
.pg-title { margin: 0; font-size: 24px; font-weight: 700; }
.pg-sub   { margin: 4px 0 0; color: var(--oj-text-3); font-size: 13px; }

.filter-bar { margin-bottom: 20px; }

/* ── Contest card grid ── */
.contest-grid { display: flex; flex-direction: column; gap: 12px; }

.contest-card { border-radius: var(--oj-radius-lg) !important; transition: transform .15s, box-shadow .15s; }
.contest-card:hover { transform: translateY(-2px); box-shadow: var(--oj-shadow) !important; }

.cc-body { display: flex; justify-content: space-between; align-items: center; gap: 16px; }
.cc-left { flex: 1; min-width: 0; }

.cc-top { display: flex; gap: 6px; margin-bottom: 6px; align-items: center; }
.status-dot {
  display: inline-block;
  width: 6px; height: 6px;
  border-radius: 50%;
  margin-right: 4px;
  background: var(--oj-text-3);
}
.status-dot.running, .status-dot.frozen { background: #67c23a; animation: blink 1.4s infinite; }
.status-dot.ready   { background: #409eff; }
.status-dot.ended   { background: #c0c4cc; }
@keyframes blink { 50% { opacity: .3 } }

.cc-title { display: block; font-size: 16px; font-weight: 600; margin-bottom: 6px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.cc-times { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--oj-text-3); flex-wrap: wrap; }
.sep { color: var(--oj-border); }

.cc-countdown { display: flex; align-items: center; gap: 4px; margin-top: 6px; font-size: 13px; }
.ready-cd   { color: #409eff; }
.running-cd { color: var(--oj-success); }
.cc-countdown :deep(.cd-value) { margin-left: 2px; font-variant-numeric: tabular-nums; }

.cc-right { flex-shrink: 0; }

.pagination { display: flex; justify-content: center; margin-top: 24px; }
</style>
