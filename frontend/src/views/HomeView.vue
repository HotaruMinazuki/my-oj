<template>
  <div class="home oj-fade-in">
    <!-- ── Hero ── -->
    <section class="hero">
      <div class="hero-inner">
        <div class="hero-badge">ICPC / OI / IOI 多赛制</div>
        <h1 class="hero-title">在线算法评测系统</h1>
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

  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { EditPen, Trophy, ArrowRight } from '@element-plus/icons-vue'
import dayjs from 'dayjs'
import { contestApi } from '@/api/http'
import type { Contest } from '@/types'

const contests        = ref<Contest[]>([])
const loadingContests = ref(true)

onMounted(async () => {
  try {
    const data = await contestApi.list(1, 6)
    contests.value = data.contests ?? []
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

/* ── Responsive ── */
@media (max-width: 900px) {
  .hero-title { font-size: 28px; }
}
</style>
