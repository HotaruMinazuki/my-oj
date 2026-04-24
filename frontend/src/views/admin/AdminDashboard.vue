<template>
  <div>
    <h2>管理概览</h2>
    <el-row :gutter="20" style="margin-top:24px">
      <el-col :span="8">
        <el-card shadow="never" class="stat-card">
          <el-statistic title="快速导航" />
          <div class="quick-links">
            <router-link to="/admin/problems"><el-button type="primary" plain style="width:100%">📝 创建题目</el-button></router-link>
            <router-link to="/admin/contests"><el-button type="success" plain style="width:100%">🏆 创建比赛</el-button></router-link>
          </div>
        </el-card>
      </el-col>
      <el-col :span="16">
        <el-card shadow="never">
          <template #header>使用提示</template>
          <el-timeline>
            <el-timeline-item timestamp="Step 1" placement="top">
              在「题目管理」中创建题目，填写题面和限制
            </el-timeline-item>
            <el-timeline-item timestamp="Step 2" placement="top">
              在「题目管理」中上传测试数据（.zip，包含 1.in/1.out 等文件）
            </el-timeline-item>
            <el-timeline-item timestamp="Step 3" placement="top">
              在「比赛管理」中创建比赛，关联题目，设置封榜时间
            </el-timeline-item>
            <el-timeline-item timestamp="Step 4" placement="top">
              比赛结束后，在比赛列表点击「滚榜」逐步解封排行
            </el-timeline-item>
          </el-timeline>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped>
h2 { margin: 0; }
.quick-links { display: flex; flex-direction: column; gap: 12px; margin-top: 16px; }
</style>
