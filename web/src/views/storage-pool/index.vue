<template>
  <div class="storage-pool-page">
    <div class="page-header-bar">
      <div class="page-header-left">
        <div class="page-title-row">
          <el-icon class="page-icon"><Box /></el-icon>
          <h2>存储池</h2>
        </div>
        <p>管理宿主机硬盘分区，配置虚拟机落盘位置与格式化挂载</p>
      </div>
      <div class="page-header-right">
        <el-button type="primary" :icon="Refresh" @click="fetchData" :loading="loading">刷新</el-button>
      </div>
    </div>

    <el-row :gutter="12" class="overview-row">
      <el-col :span="6" :xs="12" :sm="6">
        <el-card shadow="hover" class="overview-card">
          <div class="overview-accent" style="background: #409EFF;"></div>
          <div class="overview-body">
            <div class="overview-header">
              <el-icon :size="18" color="#409EFF"><Box /></el-icon>
              <span class="overview-label">总容量</span>
            </div>
            <div class="overview-value">{{ formatBytes(overviewStats.totalSize) }}</div>
            <div class="overview-sub">{{ overviewStats.diskCount }} 块物理硬盘</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6" :xs="12" :sm="6">
        <el-card shadow="hover" class="overview-card">
          <div class="overview-accent" style="background: #E6A23C;"></div>
          <div class="overview-body">
            <div class="overview-header">
              <el-icon :size="18" color="#E6A23C"><FolderOpened /></el-icon>
              <span class="overview-label">已用空间</span>
            </div>
            <div class="overview-value">{{ formatBytes(overviewStats.totalUsed) }}</div>
            <div class="overview-sub">已挂载 {{ overviewStats.mountedCount }} 个分区</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6" :xs="12" :sm="6">
        <el-card shadow="hover" class="overview-card">
          <div class="overview-accent" style="background: #67C23A;"></div>
          <div class="overview-body">
            <div class="overview-header">
              <el-icon :size="18" color="#67C23A"><Coin /></el-icon>
              <span class="overview-label">可用空间</span>
            </div>
            <div class="overview-value">{{ formatBytes(overviewStats.totalAvail) }}</div>
            <div class="overview-sub">剩余可用</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6" :xs="12" :sm="6">
        <el-card shadow="hover" class="overview-card">
          <div class="overview-accent" style="background: #9C6ADE;"></div>
          <div class="overview-body">
            <div class="overview-header">
              <el-icon :size="18" color="#9C6ADE"><Files /></el-icon>
              <span class="overview-label">存储池数量</span>
            </div>
            <div class="overview-value">{{ overviewStats.diskCount }}</div>
            <div class="overview-sub">物理磁盘</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="12" class="chart-row">
      <el-col :span="12" :xs="24">
        <el-card shadow="never" class="chart-card">
          <template #header>
            <span class="chart-title">存储池容量分布</span>
          </template>
          <div ref="pieChartRef" class="chart-container"></div>
        </el-card>
      </el-col>
      <el-col :span="12" :xs="24">
        <el-card shadow="never" class="chart-card">
          <template #header>
            <span class="chart-title">存储池容量对比</span>
          </template>
          <div ref="barChartRef" class="chart-container"></div>
        </el-card>
      </el-col>
    </el-row>

    <div class="disk-group-list" v-loading="loading">
      <el-card
        v-for="disk in tableData"
        :key="disk.id"
        class="disk-group-card"
        shadow="never"
      >
        <template #header>
          <div class="disk-group-header">
            <div class="disk-group-info">
              <div class="disk-group-name-row">
                <el-icon class="disk-icon" :size="20"><Box /></el-icon>
                <span class="disk-group-name">{{ disk.display_name }}</span>
                <el-tag v-if="disk.is_default" size="small" type="success" effect="plain">默认</el-tag>
                <el-tag v-if="disk.enabled" size="small" type="primary" effect="plain">已启用</el-tag>
              </div>
              <div class="disk-group-meta">
                <span class="mono-text">{{ disk.device_path }}</span>
                <span class="meta-sep">·</span>
                <span>{{ typeLabel(disk.type) }}</span>
                <template v-if="disk.model">
                  <span class="meta-sep">·</span>
                  <span>{{ disk.model }}</span>
                </template>
                <template v-if="disk.size > 0">
                  <span class="meta-sep">·</span>
                  <span>{{ formatBytes(disk.size) }}</span>
                </template>
              </div>
            </div>
            <div class="disk-group-actions">
              <el-button size="small" plain @click="openConfig(disk)">配置</el-button>
              <el-button size="small" plain type="primary" :disabled="!disk.can_use_for_vm || disk.is_default" @click="handleSetDefault(disk)">设为默认</el-button>
              <el-button size="small" plain type="warning" :disabled="!disk.can_format" @click="openFormat(disk)">格式化挂载</el-button>
            </div>
          </div>
        </template>

        <div v-if="disk.children && disk.children.length > 0" class="partition-list">
          <div
            v-for="part in flattenChildren(disk.children)"
            :key="part.id"
            class="partition-item"
            :style="{ paddingLeft: (20 + part.depth * 24) + 'px' }"
          >
            <div class="partition-main">
              <div class="partition-name-row">
                <el-icon v-if="part.depth > 0" class="sub-device-icon" :size="14"><Connection /></el-icon>
                <span class="partition-name">{{ part.display_name }}</span>
                <el-tag v-if="part.type === 'lvm'" size="small" type="warning" effect="plain">LVM</el-tag>
                <el-tag v-if="part.is_default" size="small" type="success" effect="plain">默认</el-tag>
                <el-tag v-if="part.enabled" size="small" type="primary" effect="plain">已启用</el-tag>
                <el-tag v-if="part.can_use_for_vm" size="small" type="success" effect="plain">可用于虚拟机</el-tag>
                <el-tooltip v-else-if="part.status_reason" :content="part.status_reason" placement="top">
                  <el-tag size="small" type="danger" effect="plain">{{ part.status_reason }}</el-tag>
                </el-tooltip>
              </div>
              <div class="partition-meta">
                <span class="mono-text">{{ part.device_path }}</span>
                <span class="meta-sep">·</span>
                <span>{{ part.fstype || '未知文件系统' }}</span>
                <template v-if="part.mountpoints?.length">
                  <span class="meta-sep">·</span>
                  <span class="mono-text">{{ part.mountpoints.join(', ') }}</span>
                </template>
              </div>
            </div>
            <div class="partition-capacity" v-if="part.size > 0">
              <el-progress
                :percentage="part.use_percent || 0"
                :stroke-width="8"
                :color="progressColor(part.use_percent)"
                :show-text="false"
              />
              <div class="partition-capacity-text">
                <span :class="{ 'text-success': part.available > 0 }">{{ formatBytes(part.available) }} 可用</span>
                <span class="capacity-sep">/</span>
                <span>{{ formatBytes(part.size) }} 总计</span>
              </div>
            </div>
            <div class="partition-actions">
              <el-button size="small" plain @click="openConfig(part)">配置</el-button>
              <el-button size="small" plain type="primary" :disabled="!part.can_use_for_vm || part.is_default" @click="handleSetDefault(part)">设为默认</el-button>
              <el-button size="small" plain type="warning" :disabled="!part.can_format" @click="openFormat(part)">格式化挂载</el-button>
            </div>
          </div>
        </div>
        <el-empty v-else description="无分区信息" :image-size="60" />
      </el-card>
      <el-empty v-if="!loading && tableData.length === 0" description="未发现存储设备" />
    </div>

    <el-dialog title="配置存储池" v-model="configVisible" width="520px" :close-on-click-modal="false" append-to-body>
      <el-form :model="configForm" label-width="100px">
        <el-form-item label="设备">
          <el-input :model-value="currentRow?.device_path" disabled />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="configForm.display_name" placeholder="请输入用户侧显示名称" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="configForm.enabled" :disabled="!currentRow?.can_use_for_vm" active-text="允许用户创建到此硬盘" />
          <div v-if="!currentRow?.can_use_for_vm" class="form-tip">
            <el-icon><InfoFilled /></el-icon>
            {{ currentRow?.status_reason || '该硬盘当前不可用于虚拟机存储' }}
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="configVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingConfig" @click="saveConfig">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog title="格式化并挂载硬盘" v-model="formatVisible" width="560px" :close-on-click-modal="false" append-to-body>
      <el-alert type="error" :closable="false" show-icon class="danger-alert">
        <template #title>
          此操作会清空目标硬盘或分区上的全部数据，并写入开机自动挂载配置。
        </template>
      </el-alert>
      <el-descriptions :column="1" border size="small">
        <el-descriptions-item label="设备">{{ currentRow?.device_path }}</el-descriptions-item>
        <el-descriptions-item label="容量">{{ formatBytes(currentRow?.size) }}</el-descriptions-item>
        <el-descriptions-item label="文件系统">{{ currentRow?.fstype || '无' }}</el-descriptions-item>
        <el-descriptions-item label="挂载目录">/var/lib/kvm-storage/{{ currentRow?.id }}</el-descriptions-item>
      </el-descriptions>
      <div class="confirm-line">
        <el-checkbox v-model="formatConfirmed">我确认要格式化该设备并挂载为虚拟机存储池</el-checkbox>
      </div>
      <template #footer>
        <el-button @click="formatVisible = false">取消</el-button>
        <el-button type="danger" :disabled="!formatConfirmed" :loading="formatting" @click="submitFormat">提交任务</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { InfoFilled, Box, Refresh, FolderOpened, Coin, Files, Connection } from '@element-plus/icons-vue'
import { getStoragePoolList, updateStoragePoolConfig, setDefaultStoragePool, formatMountStoragePool } from '@/api/infra'
import * as echarts from 'echarts'

const tableData = ref([])
const loading = ref(false)
const configVisible = ref(false)
const formatVisible = ref(false)
const savingConfig = ref(false)
const formatting = ref(false)
const formatConfirmed = ref(false)
const currentRow = ref(null)

const pieChartRef = ref(null)
const barChartRef = ref(null)
let pieChart = null
let barChart = null

const configForm = reactive({
  display_name: '',
  enabled: false,
})

const fetchData = async () => {
  loading.value = true
  try {
    const res = await getStoragePoolList()
    tableData.value = res.data || []
  } finally {
    loading.value = false
  }
}

onMounted(fetchData)

const openConfig = (row) => {
  currentRow.value = row
  configForm.display_name = row.display_name || ''
  configForm.enabled = !!row.enabled
  configVisible.value = true
}

const saveConfig = async () => {
  if (!currentRow.value) return
  savingConfig.value = true
  try {
    await updateStoragePoolConfig(currentRow.value.id, {
      display_name: configForm.display_name,
      enabled: configForm.enabled,
    })
    ElMessage.success('存储池配置已保存')
    configVisible.value = false
    fetchData()
  } finally {
    savingConfig.value = false
  }
}

const handleSetDefault = async (row) => {
  try {
    await ElMessageBox.confirm(`确定将 ${row.display_name} 设为默认虚拟机存储位置吗？`, '提示', { type: 'warning' })
    await setDefaultStoragePool(row.id)
    ElMessage.success('已设为默认存储位置')
    fetchData()
  } catch {}
}

const openFormat = (row) => {
  currentRow.value = row
  formatConfirmed.value = false
  formatVisible.value = true
}

const submitFormat = async () => {
  if (!currentRow.value) return
  formatting.value = true
  try {
    await formatMountStoragePool(currentRow.value.id)
    ElMessage.success('格式化并挂载任务已提交，请在任务中心查看进度')
    formatVisible.value = false
  } finally {
    formatting.value = false
  }
}

const formatBytes = (bytes) => {
  if (!bytes || bytes <= 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let val = Number(bytes)
  let idx = 0
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024
    idx += 1
  }
  return `${val.toFixed(2)} ${units[idx]}`
}

const progressColor = (pct = 0) => {
  if (pct >= 90) return '#f56c6c'
  if (pct >= 70) return '#e6a23c'
  return '#409eff'
}

const typeLabel = (type) => {
  const map = { disk: '硬盘', part: '分区', lvm: 'LVM', loop: 'Loop', rom: '光驱' }
  return map[type] || type || '-'
}

const collectLeafNodes = (nodes) => {
  const leaves = []
  for (const node of nodes) {
    if (!node.children || node.children.length === 0) {
      leaves.push(node)
    } else {
      leaves.push(...collectLeafNodes(node.children))
    }
  }
  return leaves
}

const isDarkMode = () => {
  return document.documentElement.classList.contains('dark')
}

const getThemeColor = (varName, fallback) => {
  const val = getComputedStyle(document.documentElement).getPropertyValue(varName).trim()
  return val || fallback
}

const leafPools = computed(() => collectLeafNodes(tableData.value))

const mountedLeafPools = computed(() => leafPools.value.filter(p => p.mountpoints && p.mountpoints.length > 0))

const overviewStats = computed(() => {
  const mounted = mountedLeafPools.value
  const totalSize = leafPools.value.reduce((sum, p) => sum + (p.size || 0), 0)
  const totalUsed = mounted.reduce((sum, p) => sum + (p.used || 0), 0)
  const totalAvail = mounted.reduce((sum, p) => sum + (p.available || 0), 0)
  return {
    totalSize,
    totalUsed,
    totalAvail,
    diskCount: tableData.value.length,
    mountedCount: mounted.length,
  }
})

const pieChartData = computed(() => {
  return leafPools.value
    .filter(p => p.size > 0)
    .map(p => ({
      name: p.display_name,
      value: p.size,
      used: p.used || 0,
      available: p.available || 0,
      usePercent: p.use_percent || 0,
    }))
})

const barChartData = computed(() => {
  return mountedLeafPools.value
    .filter(p => p.size > 0)
    .map(p => ({
      name: p.display_name,
      used: p.used || 0,
      available: p.available || 0,
      total: p.size,
      usePercent: p.use_percent || 0,
    }))
})

const flattenChildren = (nodes, depth = 0) => {
  const result = []
  for (const node of nodes) {
    result.push({ ...node, depth })
    if (node.children && node.children.length > 0) {
      result.push(...flattenChildren(node.children, depth + 1))
    }
  }
  return result
}

const PALETTE = ['#409EFF', '#67C23A', '#E6A23C', '#9C6ADE', '#F56C6C', '#00B8D4']

const initCharts = () => {
  if (pieChartRef.value) {
    pieChart = echarts.init(pieChartRef.value)
  }
  if (barChartRef.value) {
    barChart = echarts.init(barChartRef.value)
  }
  updateCharts()
}

const updateCharts = () => {
  if (!pieChart && !barChart) return

  const dark = isDarkMode()
  const textColor = dark ? '#A3A6AD' : '#606266'
  const primaryTextColor = dark ? '#E5EAF3' : '#303133'
  const gridLineColor = dark ? 'rgba(255,255,255,0.08)' : 'rgba(150,150,150,0.15)'
  const availableColor = dark ? '#3A3A3C' : '#E5E7EB'
  const isMobile = window.innerWidth < 768

  if (pieChart) {
    const pieData = pieChartData.value
    const totalSize = overviewStats.value.totalSize
    pieChart.setOption({
      tooltip: {
        trigger: 'item',
        formatter: (params) => {
          const d = params.data
          const usedGB = (d.used / 1024 / 1024 / 1024).toFixed(2)
          const availGB = (d.available / 1024 / 1024 / 1024).toFixed(2)
          return `<b>${d.name}</b><br/>容量占比: ${params.percent}%<br/>已用: ${usedGB} GB<br/>可用: ${availGB} GB<br/>使用率: ${d.usePercent}%`
        },
        backgroundColor: dark ? '#2A2A2C' : '#fff',
        borderColor: dark ? '#4A4A4C' : '#E5E7EB',
        textStyle: { color: primaryTextColor },
      },
      legend: {
        show: !isMobile,
        orient: 'vertical',
        right: '5%',
        top: 'center',
        textStyle: { color: textColor, fontSize: 12 },
        itemWidth: 12,
        itemHeight: 12,
        itemGap: 8,
      },
      series: [{
        type: 'pie',
        radius: isMobile ? ['40%', '65%'] : ['45%', '70%'],
        center: isMobile ? ['50%', '50%'] : ['40%', '50%'],
        avoidLabelOverlap: true,
        padAngle: 2,
        itemStyle: { borderRadius: 6 },
        label: {
          show: !isMobile,
          formatter: '{b}\n{d}%',
          fontSize: 11,
          color: textColor,
          lineHeight: 16,
        },
        labelLine: { show: !isMobile },
        emphasis: {
          itemStyle: { shadowBlur: 10, shadowOffsetX: 0, shadowColor: 'rgba(0, 0, 0, 0.2)' },
        },
        data: pieData.map((d, i) => ({
          ...d,
          itemStyle: { color: PALETTE[i % PALETTE.length] },
        })),
        graphic: [{
          type: 'text',
          left: isMobile ? 'center' : '35%',
          top: 'center',
          style: {
            text: formatBytes(totalSize),
            textAlign: 'center',
            fill: primaryTextColor,
            fontSize: 18,
            fontWeight: 'bold',
          },
        }, {
          type: 'text',
          left: isMobile ? 'center' : '35%',
          top: isMobile ? '58%' : '58%',
          style: {
            text: '总容量',
            textAlign: 'center',
            fill: textColor,
            fontSize: 12,
          },
        }],
      }],
    })
  }

  if (barChart) {
    const barData = barChartData.value
    if (isMobile) {
      barChart.setOption({
        tooltip: {
          trigger: 'axis',
          axisPointer: { type: 'shadow' },
          formatter: (params) => {
            const d = barData[params[0]?.dataIndex]
            if (!d) return ''
            return `<b>${d.name}</b><br/>已用: ${formatBytes(d.used)} (${d.usePercent}%)<br/>可用: ${formatBytes(d.available)}<br/>总计: ${formatBytes(d.total)}`
          },
          backgroundColor: dark ? '#2A2A2C' : '#fff',
          borderColor: dark ? '#4A4A4C' : '#E5E7EB',
          textStyle: { color: primaryTextColor },
        },
        grid: { left: '4%', right: '12%', top: '10%', bottom: '10%', containLabel: true },
        xAxis: {
          type: 'value',
          axisLabel: { show: false },
          splitLine: { show: false },
          axisLine: { show: false },
        },
        yAxis: {
          type: 'category',
          data: barData.map(d => ''),
          axisLine: { show: false },
          axisTick: { show: false },
        },
        series: [
          {
            name: '已用',
            type: 'bar',
            stack: 'total',
            barWidth: 20,
            data: barData.map(d => ({
              value: d.used,
              itemStyle: { color: progressColor(d.usePercent), borderRadius: [4, 0, 0, 4] },
            })),
            label: {
              show: true,
              position: 'inside',
              formatter: (p) => {
                const d = barData[p.dataIndex]
                return d.usePercent >= 15 ? `${d.usePercent}%` : ''
              },
              fontSize: 10,
              color: '#fff',
            },
          },
          {
            name: '可用',
            type: 'bar',
            stack: 'total',
            data: barData.map(d => ({
              value: d.available,
              itemStyle: { color: availableColor, borderRadius: [0, 4, 4, 0] },
            })),
            label: {
              show: true,
              position: 'right',
              formatter: (p) => {
                const d = barData[p.dataIndex]
                return d.name
              },
              fontSize: 10,
              color: textColor,
              distance: 8,
            },
          },
        ],
      })
    } else {
      barChart.setOption({
        tooltip: {
          trigger: 'axis',
          axisPointer: { type: 'shadow' },
          formatter: (params) => {
            const d = barData[params[0]?.dataIndex]
            if (!d) return ''
            return `<b>${d.name}</b><br/>已用: ${formatBytes(d.used)} (${d.usePercent}%)<br/>可用: ${formatBytes(d.available)}<br/>总计: ${formatBytes(d.total)}`
          },
          backgroundColor: dark ? '#2A2A2C' : '#fff',
          borderColor: dark ? '#4A4A4C' : '#E5E7EB',
          textStyle: { color: primaryTextColor },
        },
        legend: {
          data: ['已用', '可用'],
          top: 0,
          textStyle: { color: textColor },
          itemWidth: 12,
          itemHeight: 12,
        },
        grid: { left: '3%', right: '6%', top: '15%', bottom: '10%', containLabel: true },
        xAxis: {
          type: 'category',
          data: barData.map(d => d.name),
          axisLabel: { color: textColor, fontSize: 11, rotate: barData.length > 5 ? 15 : 0 },
          axisLine: { lineStyle: { color: gridLineColor } },
        },
        yAxis: {
          type: 'value',
          axisLabel: {
            color: textColor,
            fontSize: 11,
            formatter: (v) => {
              if (v >= 1024 * 1024 * 1024 * 1024) return (v / 1024 / 1024 / 1024 / 1024).toFixed(1) + ' TB'
              if (v >= 1024 * 1024 * 1024) return (v / 1024 / 1024 / 1024).toFixed(1) + ' GB'
              if (v >= 1024 * 1024) return (v / 1024 / 1024).toFixed(0) + ' MB'
              return v
            },
          },
          splitLine: { lineStyle: { color: gridLineColor } },
        },
        series: [
          {
            name: '已用',
            type: 'bar',
            stack: 'total',
            barWidth: 32,
            data: barData.map(d => ({
              value: d.used,
              itemStyle: { color: progressColor(d.usePercent), borderRadius: [4, 0, 0, 4] },
            })),
            label: {
              show: true,
              position: 'inside',
              formatter: (p) => {
                const d = barData[p.dataIndex]
                return d.usePercent >= 10 ? `${d.usePercent}%` : ''
              },
              fontSize: 11,
              color: '#fff',
            },
          },
          {
            name: '可用',
            type: 'bar',
            stack: 'total',
            data: barData.map(d => ({
              value: d.available,
              itemStyle: { color: availableColor, borderRadius: [0, 4, 4, 0] },
            })),
          },
        ],
      })
    }
  }
}

const handleChartResize = () => {
  pieChart?.resize()
  barChart?.resize()
}

let themeObserver = null

watch(tableData, () => {
  nextTick(() => {
    updateCharts()
  })
})

onMounted(() => {
  nextTick(() => {
    initCharts()
  })
  window.addEventListener('resize', handleChartResize)
  themeObserver = new MutationObserver(() => {
    updateCharts()
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
})

onUnmounted(() => {
  window.removeEventListener('resize', handleChartResize)
  if (themeObserver) {
    themeObserver.disconnect()
  }
  pieChart?.dispose()
  barChart?.dispose()
})
</script>

<style scoped>
.storage-pool-page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.storage-pool-page :deep(.el-table__expand-icon) {
  vertical-align: middle;
}

.page-header-bar {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 20px 10px 16px;
}

.page-header-left .page-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}

.page-header-left .page-icon {
  font-size: 22px;
  color: var(--el-color-primary);
}

.page-header-left h2 {
  margin: 0;
  font-size: 19px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.page-header-left p {
  margin: 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.page-header-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.overview-row {
  margin: 0 10px 12px;
}

.overview-card {
  border-radius: 12px;
  border: none;
  overflow: hidden;
  transition: transform .2s, box-shadow .2s;
}

.overview-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
}

.overview-card :deep(.el-card__body) {
  padding: 0;
}

.overview-accent {
  height: 3px;
}

.overview-body {
  padding: 14px 16px;
}

.overview-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.overview-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  font-weight: 500;
}

.overview-value {
  font-size: 20px;
  font-weight: 800;
  color: var(--el-text-color-primary);
  line-height: 1.2;
}

.overview-sub {
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  margin-top: 2px;
}

.chart-row {
  margin: 0 10px 12px;
}

.chart-card {
  border-radius: 12px;
  border: none;
}

.chart-card :deep(.el-card__header) {
  padding: 12px 16px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.chart-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.chart-container {
  width: 100%;
  height: 300px;
}

/* ===== 磁盘分组卡片 ===== */
.disk-group-list {
  margin: 0 10px 10px;
}

.disk-group-card {
  border-radius: 12px;
  border: 1px solid var(--el-border-color-lighter);
  margin-bottom: 16px;
  overflow: hidden;
}

.disk-group-card :deep(.el-card__header) {
  padding: 16px 20px;
  background: var(--el-fill-color-lighter);
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.disk-group-card :deep(.el-card__body) {
  padding: 0;
}

.disk-group-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
}

.disk-group-info {
  flex: 1;
  min-width: 0;
}

.disk-group-name-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.disk-icon {
  color: var(--el-color-primary);
  flex-shrink: 0;
}

.disk-group-name {
  font-size: 16px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}

.disk-group-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.meta-sep {
  color: var(--el-text-color-placeholder);
  margin: 0 4px;
}

.disk-group-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

/* ===== 分区列表 ===== */
.partition-list {
  display: flex;
  flex-direction: column;
}

.partition-item {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--el-border-color-extra-light);
  transition: background-color .15s;
}

.partition-item:last-child {
  border-bottom: none;
}

.partition-item:hover {
  background: var(--el-fill-color-lighter);
}

.partition-main {
  flex: 1;
  min-width: 0;
}

.partition-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  flex-wrap: wrap;
}

.partition-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.sub-device-icon {
  color: var(--el-text-color-placeholder);
  flex-shrink: 0;
}

.partition-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.partition-capacity {
  flex-shrink: 0;
  width: 180px;
}

.partition-capacity-text {
  display: flex;
  align-items: baseline;
  gap: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 4px;
}

.capacity-sep {
  color: var(--el-text-color-placeholder);
  margin: 0 2px;
}

.text-success {
  color: #67C23A;
}

.partition-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.mono-text {
  font-family: 'SF Mono', 'Monaco', 'Menlo', 'Consolas', monospace;
  font-size: 12px;
}

.form-tip {
  margin-top: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  display: flex;
  align-items: center;
  gap: 4px;
}

.confirm-line {
  margin-top: 16px;
}

.danger-alert {
  margin-bottom: 16px;
}

@media (max-width: 900px) {
  .disk-group-header {
    flex-direction: column;
    gap: 12px;
  }

  .disk-group-actions {
    width: 100%;
  }

  .partition-item {
    flex-wrap: wrap;
    gap: 12px;
  }

  .partition-capacity {
    width: 100%;
    order: 3;
  }

  .partition-actions {
    width: 100%;
    order: 4;
  }
}

@media (max-width: 768px) {
  .overview-row {
    margin: 0 4px 8px;
  }

  .chart-row {
    margin: 0 4px 8px;
  }

  .chart-container {
    height: 220px;
  }

  .overview-value {
    font-size: 18px;
  }

  .disk-group-list {
    margin: 0 4px 4px;
  }

  .disk-group-card :deep(.el-card__header) {
    padding: 12px 16px;
  }

  .partition-item {
    padding: 12px 16px;
  }

  .partition-actions {
    flex-wrap: wrap;
  }
}

@media (max-width: 480px) {
  .overview-body {
    padding: 10px 12px;
  }

  .overview-value {
    font-size: 16px;
  }

  .overview-label {
    font-size: 12px;
  }

  .chart-container {
    height: 200px;
  }

  .disk-group-name {
    font-size: 14px;
  }

  .disk-group-meta {
    font-size: 12px;
  }
}
</style>
