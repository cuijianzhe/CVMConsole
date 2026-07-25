<template>
  <div class="cloud-disk-spec-page">
    <el-card class="cloud-disk-spec-card">
      <template #header>
        <div class="section-header">
          <div class="header-title">
            <h2>云盘规格</h2>
            <el-text type="info">管理云存储磁盘规格模板，定义容量与 IOPS 指标</el-text>
          </div>
          <div class="header-actions">
            <el-input
              v-model="searchKeyword"
              placeholder="规格名称"
              clearable
              style="width: 240px; margin-right: 12px;"
              @keyup.enter="handleSearch"
              @clear="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
            <el-button type="primary" @click="handleSearch">搜索</el-button>
            <el-button @click="handleReset">重置</el-button>
          </div>
        </div>
      </template>

      <div class="toolbar">
        <el-button type="primary" @click="openCreateDialog">
          <el-icon><Plus /></el-icon>新建
        </el-button>
        <el-button
          type="danger"
          :disabled="!selectedRows.length"
          @click="handleBatchDelete"
        >
          <el-icon><Delete /></el-icon>删除
        </el-button>
        <span class="selected-info" v-if="selectedRows.length">
          已选 {{ selectedRows.length }} 项
        </span>
      </div>

      <el-table
        v-loading="tableLoading"
        :data="tableData"
        @selection-change="handleSelectionChange"
        border
        stripe
      >
        <el-table-column type="selection" width="50" align="center" />
        <el-table-column prop="name" label="规格名称" min-width="180" />
        <el-table-column label="容量" min-width="100" align="center">
          <template #default="{ row }">
            <span>{{ formatCapacity(row.capacity_gb) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="IOPS模式" min-width="120" align="center">
          <template #default="{ row }">
            <el-tag :type="row.iops_mode === 'TOTAL' ? 'info' : 'success'" size="small">
              {{ row.iops_mode === 'TOTAL' ? '总IOPS' : '读写IOPS' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="IOPS" min-width="180" align="center">
          <template #default="{ row }">
            <span v-if="row.iops_mode === 'TOTAL'">{{ row.total_iops }}</span>
            <span v-else>读 {{ row.read_iops }} / 写 {{ row.write_iops }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="180" align="center">
          <template #default="{ row }">
            {{ formatTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" align="center" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link @click="openEditDialog(row)">编辑</el-button>
            <el-button type="danger" link @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @size-change="fetchData"
          @current-change="fetchData"
        />
      </div>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑云盘规格' : '创建云盘规格'"
      width="600px"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <el-form
        ref="formRef"
        :model="form"
        :rules="formRules"
        label-width="140px"
      >
        <el-form-item label="规格名称" prop="name">
          <el-input v-model="form.name" placeholder="请输入规格名称，如 high-perf-ssd-1TB" maxlength="50" show-word-limit />
        </el-form-item>

        <el-form-item label="容量" prop="capacity_gb">
          <div class="capacity-input">
            <el-input-number v-model="form.capacity_gb" :min="1" :max="16384" :step="10" style="flex: 1;" />
            <el-select v-model="form.capacity_unit" style="width: 100px; margin-left: 12px;">
              <el-option label="GB" value="GB" />
              <el-option label="TB" value="TB" />
            </el-select>
          </div>
        </el-form-item>

        <el-divider content-position="left">磁盘 IOPS</el-divider>

        <el-form-item label="IOPS模式" prop="iops_mode">
          <el-radio-group v-model="form.iops_mode">
            <el-radio value="TOTAL">总IOPS</el-radio>
            <el-radio value="READ_WRITE">读写IOPS</el-radio>
          </el-radio-group>
        </el-form-item>

        <el-form-item v-if="form.iops_mode === 'TOTAL'" label="总IOPS" prop="total_iops">
          <el-input-number v-model="form.total_iops" :min="0" :max="10000000" :step="100" style="width: 200px;" />
          <span class="unit-label">IOPS</span>
        </el-form-item>

        <template v-if="form.iops_mode === 'READ_WRITE'">
          <el-form-item label="读IOPS" prop="read_iops">
            <el-input-number v-model="form.read_iops" :min="0" :max="10000000" :step="100" style="width: 200px;" />
            <span class="unit-label">IOPS</span>
          </el-form-item>
          <el-form-item label="写IOPS" prop="write_iops">
            <el-input-number v-model="form.write_iops" :min="0" :max="10000000" :step="100" style="width: 200px;" />
            <span class="unit-label">IOPS</span>
          </el-form-item>
        </template>

        <el-divider content-position="left">其他</el-divider>

        <el-form-item label="简介" prop="description">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="3"
            placeholder="请输入规格简介（0~200字）"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>

        <el-divider content-position="left" v-if="showPreview">预览规格</el-divider>

        <div class="preview-box" v-if="showPreview">
          <div class="preview-row"><span class="preview-label">名称：</span>{{ form.name || '-' }}</div>
          <div class="preview-row"><span class="preview-label">容量：</span>{{ form.capacity_gb }} {{ form.capacity_unit }}</div>
          <div class="preview-row">
            <span class="preview-label">IOPS：</span>
            <span v-if="form.iops_mode === 'TOTAL'">总 {{ form.total_iops }}</span>
            <span v-else>读 {{ form.read_iops }} / 写 {{ form.write_iops }}</span>
          </div>
          <div class="preview-row" v-if="form.description">
            <span class="preview-label">简介：</span>{{ form.description }}
          </div>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitLoading" @click="handleSubmit">
          {{ isEdit ? '保存' : '确定' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Plus, Delete } from '@element-plus/icons-vue'
import {
  listCloudDiskSpecs,
  createCloudDiskSpec,
  updateCloudDiskSpec,
  deleteCloudDiskSpec,
  batchDeleteCloudDiskSpecs
} from '@/api/cloudDiskSpec'

const tableLoading = ref(false)
const submitLoading = ref(false)
const searchKeyword = ref('')
const tableData = ref([])
const selectedRows = ref([])
const dialogVisible = ref(false)
const isEdit = ref(false)
const formRef = ref(null)

const pagination = reactive({
  page: 1,
  pageSize: 20,
  total: 0
})

const form = reactive({
  id: null,
  name: '',
  capacity_gb: 100,
  capacity_unit: 'GB',
  iops_mode: 'READ_WRITE',
  total_iops: 0,
  read_iops: 0,
  write_iops: 0,
  description: ''
})

const showPreview = computed(() => {
  return form.name || form.capacity_gb || form.total_iops || form.read_iops || form.write_iops
})

const validateName = (_rule, value, callback) => {
  if (!value) {
    callback(new Error('请输入规格名称'))
  } else if (value.length < 3) {
    callback(new Error('规格名称至少3个字符'))
  } else if (value.length > 50) {
    callback(new Error('规格名称不超过50个字符'))
  } else if (!/^[a-zA-Z0-9_\-\u4e00-\u9fa5]+$/.test(value)) {
    callback(new Error('规格名称只允许字母、数字、下划线、连字符和中文'))
  } else {
    callback()
  }
}

const formRules = {
  name: [{ required: true, validator: validateName, trigger: 'blur' }],
  capacity_gb: [
    { required: true, message: '请输入容量', trigger: 'blur' },
    { type: 'number', min: 1, message: '容量必须大于0', trigger: 'blur' }
  ],
  iops_mode: [{ required: true, message: '请选择IOPS模式', trigger: 'change' }]
}

const formatTime = (value) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

const formatCapacity = (gb) => {
  if (gb >= 1024 && gb % 1024 === 0) {
    return `${gb / 1024} TB`
  }
  return `${gb} GB`
}

const fetchData = async () => {
  tableLoading.value = true
  try {
    const res = await listCloudDiskSpecs({
      page: pagination.page,
      page_size: pagination.pageSize,
      keyword: searchKeyword.value || ''
    })
    tableData.value = res.data.list || []
    pagination.total = res.data.total || 0
  } catch (err) {
  } finally {
    tableLoading.value = false
  }
}

const handleSearch = () => {
  pagination.page = 1
  fetchData()
}

const handleReset = () => {
  searchKeyword.value = ''
  pagination.page = 1
  fetchData()
}

const handleSelectionChange = (rows) => {
  selectedRows.value = rows
}

const resetForm = () => {
  form.id = null
  form.name = ''
  form.capacity_gb = 100
  form.capacity_unit = 'GB'
  form.iops_mode = 'READ_WRITE'
  form.total_iops = 0
  form.read_iops = 0
  form.write_iops = 0
  form.description = ''
}

const openCreateDialog = () => {
  isEdit.value = false
  resetForm()
  dialogVisible.value = true
}

const openEditDialog = (row) => {
  isEdit.value = true
  form.id = row.id
  form.name = row.name
  form.capacity_gb = row.capacity_gb
  form.capacity_unit = 'GB'
  form.iops_mode = row.iops_mode || 'READ_WRITE'
  form.total_iops = row.total_iops || 0
  form.read_iops = row.read_iops || 0
  form.write_iops = row.write_iops || 0
  form.description = row.description || ''
  dialogVisible.value = true
}

const getSubmitData = () => {
  const capacityGB = form.capacity_unit === 'TB' ? form.capacity_gb * 1024 : form.capacity_gb
  const data = {
    name: form.name,
    capacity_gb: capacityGB,
    iops_mode: form.iops_mode,
    description: form.description
  }
  if (form.iops_mode === 'TOTAL') {
    data.total_iops = form.total_iops
    data.read_iops = 0
    data.write_iops = 0
  } else {
    data.total_iops = 0
    data.read_iops = form.read_iops
    data.write_iops = form.write_iops
  }
  return data
}

const handleSubmit = async () => {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  submitLoading.value = true
  try {
    const data = getSubmitData()
    if (isEdit.value) {
      await updateCloudDiskSpec(form.id, data)
      ElMessage.success('更新成功')
    } else {
      await createCloudDiskSpec(data)
      ElMessage.success('创建成功')
    }
    dialogVisible.value = false
    fetchData()
  } catch (err) {
  } finally {
    submitLoading.value = false
  }
}

const handleDelete = (row) => {
  ElMessageBox.confirm(
    `确定要删除云盘规格「${row.name}」吗？此操作不可恢复。`,
    '删除确认',
    {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      await deleteCloudDiskSpec(row.id)
      ElMessage.success('删除成功')
      fetchData()
    } catch (err) {
    }
  }).catch(() => {})
}

const handleBatchDelete = () => {
  const ids = selectedRows.value.map((row) => row.id)
  if (!ids.length) return

  ElMessageBox.confirm(
    `确定要删除选中的 ${ids.length} 条云盘规格吗？此操作不可恢复。`,
    '批量删除确认',
    {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      await batchDeleteCloudDiskSpecs(ids)
      ElMessage.success('批量删除成功')
      fetchData()
    } catch (err) {
    }
  }).catch(() => {})
}

onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.cloud-disk-spec-page {
  padding: 0;
}

.cloud-disk-spec-card {
  border: none;
}

.cloud-disk-spec-card :deep(.el-card__header) {
  padding: 16px 20px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
}

.header-title h2 {
  margin: 0 0 4px 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.header-actions {
  display: flex;
  align-items: center;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.selected-info {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.pagination-wrapper {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.capacity-input {
  display: flex;
  align-items: center;
  width: 100%;
}

.unit-label {
  margin-left: 12px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.preview-box {
  background: var(--el-fill-color-light);
  border-radius: 6px;
  padding: 12px 16px;
  margin-bottom: 16px;
}

.preview-row {
  font-size: 13px;
  color: var(--el-text-color-regular);
  line-height: 1.8;
}

.preview-label {
  color: var(--el-text-color-secondary);
  margin-right: 4px;
}
</style>
