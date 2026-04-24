<template>
  <div>
    <!-- 状态栏 -->
    <div class="board-header">
      <div class="board-status">
        <el-tag v-if="frozen" type="warning" size="large" effect="dark">
          <el-icon><Lock /></el-icon> 封榜中
        </el-tag>
        <el-tag v-else type="success" size="large" effect="dark">
          <el-icon><CircleCheck /></el-icon> 实时排行
        </el-tag>
        <el-tag
          :type="wsStatus === 'open' ? 'success' : wsStatus === 'connecting' ? 'info' : 'danger'"
          size="small"
          class="ws-tag"
        >
          <span class="ws-dot" :class="wsStatus" />
          {{ wsStatus === 'open' ? '已连接' : wsStatus === 'connecting' ? '连接中' : '已断线' }}
        </el-tag>
      </div>
      <div class="board-legend">
        <span class="legend-item legend-ac">AC</span>
        <span class="legend-item legend-wa">WA</span>
        <span class="legend-item legend-pending">?</span>
      </div>
    </div>

    <div v-if="loading" class="board-loading">
      <el-icon class="is-loading"><Loading /></el-icon>
      <span>加载排行榜…</span>
    </div>

    <el-table
      v-else
      :data="rows"
      stripe
      :row-class-name="rowClass"
      style="width:100%"
    >
      <!-- 名次 -->
      <el-table-column label="名次" width="72" align="center">
        <template #default="{ row }">
          <span v-if="row.rank === 1" class="medal gold">🥇</span>
          <span v-else-if="row.rank === 2" class="medal silver">🥈</span>
          <span v-else-if="row.rank === 3" class="medal bronze">🥉</span>
          <span v-else class="rank-num">{{ row.rank }}</span>
        </template>
      </el-table-column>

      <!-- 选手 -->
      <el-table-column label="选手" min-width="160">
        <template #default="{ row }">
          <div class="contestant-cell">
            <span class="username">
              {{ row.username }}
              <el-tag v-if="row.user_id === myUserId" type="primary" size="small" class="me-tag">我</el-tag>
            </span>
            <span v-if="row.organization" class="org">{{ row.organization }}</span>
          </div>
        </template>
      </el-table-column>

      <!-- 每道题 -->
      <el-table-column
        v-for="label in problemLabels"
        :key="label"
        :label="label"
        width="90"
        align="center"
      >
        <template #default="{ row }">
          <div :class="['prob-cell', cellClass(row.problems?.[label])]">
            <template v-if="row.problems?.[label]?.solved">
              <div class="cell-top">+{{ row.problems[label].attempts }}</div>
              <div class="cell-bot">{{ row.problems[label].penalty }}′</div>
            </template>
            <template v-else-if="(row.problems?.[label]?.pending ?? 0) > 0">
              <span class="pend-icon">?</span>
            </template>
            <template v-else-if="(row.problems?.[label]?.attempts ?? 0) > 0">
              <span class="wa-icon">-{{ row.problems[label].attempts }}</span>
            </template>
            <template v-else>
              <span class="empty-dash">·</span>
            </template>
          </div>
        </template>
      </el-table-column>

      <!-- 总计 -->
      <el-table-column label="AC" prop="total_solved" width="64" align="center">
        <template #default="{ row }">
          <span class="total-solved">{{ row.total_solved }}</span>
        </template>
      </el-table-column>
      <el-table-column label="罚时" prop="total_penalty" width="80" align="center">
        <template #default="{ row }">
          <span class="total-penalty">{{ row.total_penalty }}′</span>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Lock, CircleCheck, Loading } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ contestId: number }>()

interface ProblemEntry {
  solved: boolean
  attempts: number
  pending: number
  penalty: number
}
interface Row {
  rank: number
  user_id: number
  username: string
  organization?: string
  problems: Record<string, ProblemEntry>
  total_solved: number
  total_penalty: number
}

const auth = useAuthStore()
const myUserId = computed(() => auth.user?.id ?? -1)

const rows          = ref<Row[]>([])
const problemLabels = ref<string[]>([])
const frozen        = ref(false)
const loading       = ref(true)
const wsStatus      = ref<'connecting' | 'open' | 'closed'>('connecting')
let ws: WebSocket | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null

function connect() {
  if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null }
  const protocol = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${protocol}://${location.host}/ws/ranking/${props.contestId}`)
  wsStatus.value = 'connecting'

  ws.onopen  = () => { wsStatus.value = 'open'; loading.value = false }
  ws.onclose = () => {
    wsStatus.value = 'closed'
    reconnectTimer = setTimeout(connect, 3000)
  }
  ws.onerror = () => { ws?.close() }

  ws.onmessage = (ev: MessageEvent) => {
    try {
      const msg = JSON.parse(ev.data)
      if (msg.type === 'snapshot') applySnapshot(msg.data)
      else if (msg.type === 'delta') applyDelta(msg.data)
    } catch { /* ignore */ }
  }
}

function applySnapshot(data: any) {
  rows.value          = data.contestants ?? []
  problemLabels.value = data.problems    ?? []
  frozen.value        = data.frozen      ?? false
  loading.value       = false
}

function applyDelta(data: any) {
  const idx = rows.value.findIndex(r => r.user_id === data.user_id)
  if (idx >= 0) rows.value[idx] = { ...rows.value[idx], ...data }
  else           rows.value.push(data)
  rows.value.sort((a, b) => a.rank - b.rank)
}

function rowClass({ row }: { row: Row }) {
  const classes: string[] = []
  if (row.rank === 1) classes.push('rank-gold')
  else if (row.rank === 2) classes.push('rank-silver')
  else if (row.rank === 3) classes.push('rank-bronze')
  if (row.user_id === myUserId.value) classes.push('rank-me')
  return classes.join(' ')
}

function cellClass(p?: ProblemEntry) {
  if (!p) return ''
  if (p.solved)       return 'cell-ac'
  if (p.pending > 0)  return 'cell-pending'
  if (p.attempts > 0) return 'cell-wa'
  return ''
}

onMounted(connect)
onBeforeUnmount(() => {
  if (reconnectTimer) clearTimeout(reconnectTimer)
  ws?.close()
})
</script>

<style scoped>
/* ── Header ── */
.board-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  flex-wrap: wrap;
  gap: 8px;
}
.board-status { display: flex; align-items: center; gap: 10px; }
.ws-dot {
  display: inline-block;
  width: 6px; height: 6px;
  border-radius: 50%;
  margin-right: 4px;
  background: #909399;
}
.ws-dot.open       { background: #67c23a; }
.ws-dot.connecting { background: #e6a23c; animation: blink 1s infinite; }
.ws-dot.closed     { background: #f56c6c; }
@keyframes blink { 50% { opacity: 0 } }
.ws-tag { border-radius: 12px; }

.board-legend { display: flex; gap: 8px; }
.legend-item  { padding: 2px 8px; border-radius: 4px; font-size: 12px; font-weight: 600; }
.legend-ac      { background: #d1f5d1; color: #1a7a1a; }
.legend-wa      { background: #fde2e2; color: #c0392b; }
.legend-pending { background: #fdf6ec; color: #b07d1a; }

/* ── Loading ── */
.board-loading { text-align: center; padding: 40px 0; color: var(--oj-text-3); display: flex; align-items: center; justify-content: center; gap: 8px; font-size: 15px; }

/* ── Rank cells ── */
.medal     { font-size: 20px; }
.rank-num  { font-weight: 700; color: var(--oj-text-2); }

/* ── Contestant ── */
.contestant-cell { display: flex; flex-direction: column; line-height: 1.4; }
.username  { font-weight: 600; display: flex; align-items: center; gap: 4px; }
.org       { font-size: 11px; color: var(--oj-text-3); }
.me-tag    { border-radius: 8px; }

/* ── Problem cells ── */
.prob-cell { border-radius: 4px; padding: 3px 4px; min-height: 32px; display: flex; flex-direction: column; align-items: center; justify-content: center; }
.cell-ac      { background: #d1f5d1; color: #1a7a1a; }
.cell-wa      { background: #fde2e2; color: #c0392b; }
.cell-pending { background: #fdf6ec; color: #b07d1a; }
.cell-top  { font-size: 11px; font-weight: 700; line-height: 1.3; }
.cell-bot  { font-size: 10px; line-height: 1.3; }
.pend-icon { font-size: 16px; font-weight: 700; }
.wa-icon   { font-size: 13px; font-weight: 700; }
.empty-dash{ color: #d0d3d9; font-size: 18px; }

/* ── Totals ── */
.total-solved  { font-weight: 700; font-size: 15px; color: var(--oj-success); }
.total-penalty { color: var(--oj-text-2); font-size: 13px; }

/* ── Row highlights ── */
:deep(.rank-gold td)   { background: #fffbe6 !important; }
:deep(.rank-silver td) { background: #f8f8f8 !important; }
:deep(.rank-bronze td) { background: #fff6ef !important; }
:deep(.rank-me td)     { background: #ecf5ff !important; outline: 2px solid #409eff; outline-offset: -2px; }
</style>
