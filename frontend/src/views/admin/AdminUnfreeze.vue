<template>
  <div>
    <div class="page-header">
      <router-link :to="`/contests/${contestId}/ranking`" class="back-link">← 查看排行榜</router-link>
      <h2>🎉 滚榜仪式</h2>
    </div>

    <el-card shadow="never">
      <el-alert type="warning" :closable="false" show-icon style="margin-bottom:20px">
        每次点击「解封下一名」，将揭示排名最后一名选手被冻结的提交结果，直到无更多被冻结的提交为止。
      </el-alert>

      <div style="text-align:center; padding: 40px 0;">
        <el-button
          type="danger"
          size="large"
          :loading="unfreezing"
          :disabled="done"
          @click="handleUnfreeze"
          style="font-size:18px; padding: 20px 40px;"
        >
          {{ done ? '🎊 滚榜完成' : '▶ 解封下一名' }}
        </el-button>

        <div v-if="lastResult" class="last-result">
          <el-divider>上次解封结果</el-divider>
          <pre>{{ JSON.stringify(lastResult, null, 2) }}</pre>
        </div>
      </div>
    </el-card>

    <!-- 实时排行榜预览 -->
    <el-card shadow="never" style="margin-top:20px">
      <template #header><span>实时排行榜</span></template>
      <RankingBoard :contest-id="contestId" />
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { contestApi } from '@/api/http'
import RankingBoard from '@/components/RankingBoard.vue'

const route     = useRoute()
const contestId = computed(() => Number(route.params.id))

const unfreezing = ref(false)
const done       = ref(false)
const lastResult = ref<any>(null)

async function handleUnfreeze() {
  unfreezing.value = true
  try {
    const res = await contestApi.unfreezeNext(contestId.value)
    lastResult.value = res
    if (res.done) {
      done.value = true
      ElMessage.success('🎊 所有冻结提交已全部解封！')
    } else {
      ElMessage.success('解封成功，继续点击解封下一名')
    }
  } finally {
    unfreezing.value = false
  }
}
</script>

<style scoped>
.page-header { margin-bottom: 16px; }
.page-header h2 { margin: 4px 0 0; font-size: 22px; }
.back-link { color: #409eff; text-decoration: none; font-size: 14px; }
.last-result { margin-top: 24px; text-align: left; }
.last-result pre { background: #f5f7fa; padding: 12px; border-radius: 4px; font-size: 13px; overflow: auto; }
</style>
