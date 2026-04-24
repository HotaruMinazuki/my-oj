<template>
  <div class="page oj-fade-in">
    <div class="pg-head">
      <div class="pg-left">
        <router-link :to="`/contests/${contestId}`" class="back-btn">
          <el-icon><ArrowLeft /></el-icon> 返回比赛
        </router-link>
        <h2 class="pg-title">排行榜</h2>
      </div>
    </div>
    <RankingBoard :contest-id="contestId" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft } from '@element-plus/icons-vue'
import RankingBoard from '@/components/RankingBoard.vue'

const route     = useRoute()
const contestId = computed(() => Number(route.params.id))
</script>

<style scoped>
.pg-head { display: flex; align-items: flex-end; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 8px; }
.pg-left { display: flex; flex-direction: column; gap: 4px; }
.pg-title { margin: 0; font-size: 22px; font-weight: 700; }
.back-btn { display: inline-flex; align-items: center; gap: 4px; color: var(--oj-primary); text-decoration: none; font-size: 14px; }
.back-btn:hover { text-decoration: underline; }
.pg-contest-name { color: var(--oj-text-2); font-size: 14px; }
</style>
