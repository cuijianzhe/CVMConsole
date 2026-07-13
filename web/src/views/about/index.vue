<template>
  <div class="about-container">
    <el-collapse v-model="activeSections" class="about-collapse">
      <!-- 面板信息 -->
      <el-collapse-item name="panel">
        <template #title>
          <div class="section-header">
            <el-icon class="section-icon"><Monitor /></el-icon>
            <span class="section-title">面板信息</span>
          </div>
        </template>
        <div class="info-grid">
          <div class="info-item">
            <span class="info-label">版本</span>
            <span class="info-value">{{ versionInfo.version || '开发版' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">构建时间</span>
            <span class="info-value">{{ versionInfo.build_time || '未设置' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">站点名称</span>
            <span class="info-value">{{ versionInfo.site_title || '未设置' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">运行模式</span>
            <span class="info-value">
              <el-tag size="small" :type="isDev ? 'warning' : 'success'">{{ isDev ? '开发环境' : '生产环境' }}</el-tag>
            </span>
          </div>
        </div>
      </el-collapse-item>

      <!-- 系统信息 -->
      <el-collapse-item name="system">
        <template #title>
          <div class="section-header">
            <el-icon class="section-icon"><Setting /></el-icon>
            <span class="section-title">系统信息</span>
          </div>
        </template>
        <div class="info-grid" v-loading="sysLoading">
          <div class="info-item">
            <span class="info-label">操作系统</span>
            <span class="info-value">{{ sysInfo.distro || sysInfo.os || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">内核版本</span>
            <span class="info-value">{{ sysInfo.kernel || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">系统架构</span>
            <span class="info-value">{{ sysInfo.arch || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">主机名</span>
            <span class="info-value">{{ sysInfo.hostname || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">CPU 核数</span>
            <span class="info-value">{{ sysInfo.num_cpu || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">Go 版本</span>
            <span class="info-value">{{ sysInfo.go_version || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">QEMU 版本</span>
            <span class="info-value">{{ sysInfo.qemu || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">libvirt 版本</span>
            <span class="info-value">{{ sysInfo.libvirt || '-' }}</span>
          </div>
          <div class="info-item">
            <span class="info-label">系统运行时间</span>
            <span class="info-value">{{ sysInfo.uptime || '-' }}</span>
          </div>
        </div>
      </el-collapse-item>
    </el-collapse>

    <!-- 页脚 -->
    <!-- <div class="about-footer">
      <p>© {{ currentYear }} CVMConsole. 基于 Vue 3 + Element Plus + Go 构建</p>
    </div> -->
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { getPublicVersion, getPublicSystemInfo } from '@/api/settings'
import { applyDocumentTitle } from '@/utils/site'
import { Monitor, Setting } from '@element-plus/icons-vue'

const activeSections = ref(['panel', 'system'])
const isDev = import.meta.env.DEV
const versionInfo = ref({ version: '', build_time: '', site_title: '' })
const sysInfo = ref({})
const sysLoading = ref(false)
const currentYear = new Date().getFullYear()

const fetchVersion = async () => {
  try {
    const res = await getPublicVersion()
    versionInfo.value = {
      version: res.data?.version || '',
      build_time: res.data?.build_time || '',
      site_title: res.data?.site_title || ''
    }
  } catch {
    versionInfo.value = { version: 'dev', build_time: '', site_title: '' }
  }
}

const fetchSystemInfo = async () => {
  sysLoading.value = true
  try {
    const res = await getPublicSystemInfo()
    sysInfo.value = res.data || {}
  } catch {
    sysInfo.value = {}
  } finally {
    sysLoading.value = false
  }
}

onMounted(() => {
  applyDocumentTitle('关于项目')
  fetchVersion()
  fetchSystemInfo()
})
</script>

<style scoped>
.about-container {
  max-width: 960px;
  margin: 0 auto;
  padding: 20px;
  min-height: calc(100vh - 120px);
  display: flex;
  flex-direction: column;
}

.about-collapse {
  border: none;
}

.about-collapse :deep(.el-collapse-item) {
  margin-bottom: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 10px;
  overflow: hidden;
  background: var(--el-bg-color);
}

.about-collapse :deep(.el-collapse-item__header) {
  background: var(--el-fill-color-lighter);
  border-bottom: 1px solid var(--el-border-color-extra-light);
  padding: 0 20px;
  height: 48px;
  line-height: 48px;
  font-size: 15px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.about-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}

.about-collapse :deep(.el-collapse-item__content) {
  padding: 20px;
}

.section-header {
  display: flex;
  align-items: center;
  gap: 10px;
}

.section-icon {
  font-size: 18px;
  color: var(--el-color-primary);
}

.section-title {
  font-weight: 600;
}

/* 信息网格 */
.info-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.info-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 16px;
  background: var(--el-fill-color-lighter);
  border-radius: 8px;
  gap: 12px;
}

.info-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  flex-shrink: 0;
}

.info-value {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
  text-align: right;
  word-break: break-all;
}

.info-link {
  font-size: 13px;
  color: var(--el-color-primary);
  text-decoration: none;
  word-break: break-all;
  text-align: right;
}

.info-link:hover {
  text-decoration: underline;
}

.info-link-inline {
  color: var(--el-color-primary);
  text-decoration: none;
  margin-left: 4px;
}

.info-link-inline:hover {
  text-decoration: underline;
}

.text-muted {
  color: var(--el-text-color-secondary);
}

/* 页脚 */
.about-footer {
  text-align: center;
  padding: 24px 0;
  border-top: 1px solid var(--el-border-color-lighter);
  margin-top: auto;
}

.about-footer p {
  margin: 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

/* 响应式 */
@media (max-width: 768px) {
  .tech-grid {
    grid-template-columns: 1fr;
  }

  .info-grid {
    grid-template-columns: 1fr;
  }

  .info-item {
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
  }

  .info-value,
  .info-link {
    text-align: left;
  }
}
</style>
