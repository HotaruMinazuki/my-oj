<template>
  <div class="home oj-fade-in">
    <!-- ── Hero ── -->
    <section class="hero">
      <div class="hero-inner">
        <div class="hero-badge">ICPC / OI / IOI 多赛制</div>
        <h1 class="hero-title">在线算法评测系统</h1>
        <p class="hero-sub">多语言沙盒评测 · 实时 WebSocket 排行榜 · 封榜 & 滚榜</p>
        <div class="hero-btns">
          <router-link to="/problems">
            <el-button type="primary" size="large" :icon="EditPen">开始刷题</el-button>
          </router-link>
          <router-link to="/contests">
            <el-button size="large" :icon="Trophy">查看比赛</el-button>
          </router-link>
        </div>
      </div>
    </section>

    <!-- ── Stats row ── -->
    <div class="stats-row">
      <div v-for="s in stats" :key="s.label" class="stat-card">
        <el-icon class="stat-icon" :style="{ color: s.color }">
          <component :is="s.icon" />
        </el-icon>
        <div class="stat-value">{{ s.value }}</div>
        <div class="stat-label">{{ s.label }}</div>
      </div>
    </div>

    <!-- ── Active / upcoming contests ── -->
    <section class="section">
      <div class="sec-head">
        <h3 class="sec-title"><el-icon><Trophy /></el-icon> 近期比赛</h3>
        <router-link to="/contests" class="link-text sec-more">查看全部 →</router-link>
      </div>
      <div v-loading="loadingContests" style="min-height:80px">
        <el-empty v-if="!loadingContests && contests.length === 0" description="暂无比赛" />
        <div v-else class="home-contest-list">
          <router-link
            v-for="c in contests"
            :key="c.id"
            :to="`/contests/${c.id}`"
            class="hc-item"
          >
            <div class="hc-left">
              <el-tag :type="statusTagType(c.status)" size="small" effect="light" class="hc-status">
                {{ statusLabel(c.status) }}
              </el-tag>
              <span class="hc-title">{{ c.title }}</span>
            </div>
            <div class="hc-right">
              <span class="hc-time">
                {{ fmt(c.start_time) }}
              </span>
              <el-icon class="hc-arrow"><ArrowRight /></el-icon>
            </div>
          </router-link>
        </div>
      </div>
    </section>

    <!-- ── Supported languages ── -->
    <section class="section">
      <div class="sec-head">
        <h3 class="sec-title"><el-icon><Monitor /></el-icon> 支持的语言</h3>
      </div>
      <div class="lang-grid">
        <div v-for="l in LANGUAGES" :key="l.name" class="lang-pill">
          <span class="lang-dot" :style="{ background: l.color }" />
          {{ l.name }}
        </div>
      </div>
    </section>

    <!-- ── Feature highlights ── -->
    <section class="section features">
      <div v-for="f in features" :key="f.title" class="feature-card">
        <el-icon class="feat-icon" :style="{ color: f.color }">
          <component :is="f.icon" />
        </el-icon>
        <div class="feat-title">{{ f.title }}</div>
        <div class="feat-desc">{{ f.desc }}</div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  EditPen, Trophy, ArrowRight, Monitor,
  DocumentCopy, Histogram, UserFilled, CircleCheck, Timer, Lock,
} from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { contestApi } from '@/api/http'
import type { Contest } from '@/types'

const LANGUAGES = [
  { name: 'C',       color: '#5c6bc0' },
  { name: 'C++17',   color: '#26c6da' },
  { name: 'C++20',   color: '#00acc1' },
  { name: 'Java 21', color: '#ef5350' },
  { name: 'Python 3',color: '#ffa726' },
  { name: 'Go',      color: '#26a69a' },
  { name: 'Rust',    color: '#8d6e63' },
]

const features = [
  { icon: CircleCheck, color: '#67c23a', title: '实时评测',   desc: '沙盒隔离，毫秒级返回，支持 special judge 和交互题' },
  { icon: Histogram,   color: '#409eff', title: 'WebSocket 排行', desc: '增量推送，封榜/滚榜，可视化高亮当前用户' },
  { icon: Lock,        color: '#e6a23c', title: '封榜 & 滚榜', desc: 'ICPC 风格最后一小时封榜，管理员一键逐行解冻' },
  { icon: Timer,       color: '#f56c6c', title: '多赛制支持', desc: 'ICPC / OI / IOI，灵活配置罚时与计分规则' },
]

const contests        = ref<Contest[]>([])
const loadingContests = ref(true)

// Stats (populated from contest list; extend with real API if available)
const stats = ref([
  { icon: DocumentCopy, color: '#409eff', label: '题目',   value: '—' },
  { icon: Trophy,       color: '#e6a23c', label: '比赛',   value: '—' },
  { icon: UserFilled,   color: '#67c23a', label: '选手',   value: '—' },
  { icon: CircleCheck,  color: '#f56c6c', label: '提交',   value: '—' },
])

onMounted(async () => {
  try {
    const data = await contestApi.list(1, 6)
    contests.value = data.contests ?? []
    stats.value[1].value = String(data.total ?? contests.value.length)
  } finally {
    loadingContests.value = false
  }
})

const fmt = (t: string) => dayjs(t).format('MM-DD HH:mm')

function statusLabel(s: string) {
  return { running: '进行中', frozen: '封榜', ended: '已结束', ready: '即将开始', draft: '草稿' }[s] ?? s
}
function statusTagType(s: string): '' | 'success' | 'warning' | 'info' | 'danger' {
  return ({ running: 'success', frozen: 'warning', ended: 'info', ready: '', draft: 'info' } as any)[s] ?? ''
}
</script>

<style scoped>
.home { padding-bottom: 48px; }

/* ── Hero ── */
.hero {
  background: linear-gradient(135deg, #1d2129 0%, #2c3345 60%, #1a3a5c 100%);
  border-radius: var(--oj-radius-lg);
  margin-bottom: 32px;
  overflow: hidden;
  position: relative;
}
.hero::before {
  content: '';
  position: absolute;
  inset: 0;
  background: radial-gradient(circle at 70% 50%, rgba(64,158,255,.12) 0%, transparent 60%);
  pointer-events: none;
}
.hero-inner {
  position: relative;
  text-align: center;
  padding: 64px 24px 56px;
}
.hero-badge {
  display: inline-block;
  background: rgba(64,158,255,.18);
  color: #7ec8ff;
  border: 1px solid rgba(64,158,255,.3);
  border-radius: 20px;
  padding: 3px 14px;
  font-size: 12px;
  letter-spacing: .5px;
  margin-bottom: 18px;
}
.hero-title {
  font-size: 40px;
  font-weight: 800;
  color: #fff;
  margin: 0 0 12px;
  letter-spacing: .5px;
}
.hero-sub {
  color: #a8b5c8;
  font-size: 15px;
  margin: 0 0 32px;
}
.hero-btns { display: flex; gap: 14px; justify-content: center; flex-wrap: wrap; }

/* ── Stats ── */
.stats-row {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
  margin-bottom: 32px;
}
.stat-card {
  background: var(--oj-card-bg);
  border: 1px solid var(--oj-border);
  border-radius: var(--oj-radius-lg);
  padding: 20px;
  text-align: center;
  box-shadow: var(--oj-shadow-sm);
  transition: box-shadow .15s;
}
.stat-card:hover { box-shadow: var(--oj-shadow); }
.stat-icon  { font-size: 28px; margin-bottom: 8px; }
.stat-value { font-size: 26px; font-weight: 700; color: var(--oj-text); }
.stat-label { font-size: 12px; color: var(--oj-text-3); margin-top: 2px; }

/* ── Sections ── */
.section { margin-bottom: 32px; }
.sec-head  { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.sec-title { margin: 0; font-size: 18px; font-weight: 700; display: flex; align-items: center; gap: 6px; }
.sec-more  { font-size: 13px; }

/* ── Contest list ── */
.home-contest-list { display: flex; flex-direction: column; gap: 8px; }
.hc-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: var(--oj-card-bg);
  border: 1px solid var(--oj-border);
  border-radius: var(--oj-radius);
  text-decoration: none;
  color: inherit;
  transition: background .15s, box-shadow .15s;
}
.hc-item:hover { background: #f0f5ff; box-shadow: var(--oj-shadow-sm); }
.hc-left  { display: flex; align-items: center; gap: 10px; min-width: 0; }
.hc-title { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.hc-right { display: flex; align-items: center; gap: 6px; flex-shrink: 0; color: var(--oj-text-3); font-size: 13px; }
.hc-arrow { font-size: 12px; }

/* ── Languages ── */
.lang-grid  { display: flex; flex-wrap: wrap; gap: 10px; }
.lang-pill  {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  background: var(--oj-card-bg);
  border: 1px solid var(--oj-border);
  border-radius: 20px;
  font-size: 13px;
  font-weight: 500;
}
.lang-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }

/* ── Features ── */
.features    { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
.feature-card {
  background: var(--oj-card-bg);
  border: 1px solid var(--oj-border);
  border-radius: var(--oj-radius-lg);
  padding: 20px;
  box-shadow: var(--oj-shadow-sm);
  transition: box-shadow .15s;
}
.feature-card:hover { box-shadow: var(--oj-shadow); }
.feat-icon  { font-size: 26px; margin-bottom: 10px; }
.feat-title { font-size: 15px; font-weight: 600; margin-bottom: 6px; }
.feat-desc  { font-size: 13px; color: var(--oj-text-3); line-height: 1.6; }

/* ── Responsive ── */
@media (max-width: 900px) {
  .stats-row { grid-template-columns: repeat(2, 1fr); }
  .features  { grid-template-columns: repeat(2, 1fr); }
  .hero-title { font-size: 28px; }
}
@media (max-width: 600px) {
  .stats-row { grid-template-columns: repeat(2, 1fr); }
  .features  { grid-template-columns: 1fr; }
}
</style>
