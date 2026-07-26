<template>
  <div class="resource-spec-page">
    <el-card class="resource-spec-card">
      <template #header>
        <div class="section-header">
          <div class="header-title">
            <h2>资源规格</h2>
            <el-text type="info">管理虚拟机资源规格模板，定义 CPU 核心数与内存大小</el-text>
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
        <el-table-column prop="name" label="资源规格名称" min-width="160" />
        <el-table-column label="规格" min-width="140" align="center">
          <template #default="{ row }">
            <span>{{ row.cpu_cores }}核 / {{ row.memory_gb }}G</span>
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
      :title="isEdit ? '编辑资源规格' : '新建资源规格'"
      width="500px"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <el-form
        ref="formRef"
        :model="form"
        :rules="formRules"
        label-width="120px"
      >
        <el-form-item label="规格名称" prop="name">
          <el-input v-model="form.name" placeholder="请输入规格名称，如 4c4g" maxlength="100" />
        </el-form-item>
        <el-form-item label="CPU 核心数" prop="cpu_cores">
          <el-input-number v-model="form.cpu_cores" :min="1" :max="128" style="width: 100%" />
        </el-form-item>
        <el-form-item label="内存 (GB)" prop="memory_gb">
          <el-input-number v-model="form.memory_gb" :min="1" :max="1024" style="width: 100%" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitLoading" @click="handleSubmit">
          {{ isEdit ? '保存' : '创建' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Plus, Delete } from '@element-plus/icons-vue'
import {
  listResourceSpecs,
  createResourceSpec,
  updateResourceSpec,
  deleteResourceSpec,
  batchDeleteResourceSpecs
} from '@/api/resourceSpec'

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
  cpu_cores: 2,
  memory_gb: 2
})

const formRules = {
  name: [
    { required: true, message: '请输入规格名称', trigger: 'blur' },
    { min: 1, max: 100, message: '长度在 1 到 100 个字符', trigger: 'blur' }
  ],
  cpu_cores: [
    { required: true, message: '请输入CPU核心数', trigger: 'blur' },
    { type: 'number', min: 1, message: 'CPU核心数必须大于0', trigger: 'blur' }
  ],
  memory_gb: [
    { required: true, message: '请输入内存大小', trigger: 'blur' },
    { type: 'number', min: 1, message: '内存必须大于0', trigger: 'blur' }
  ]
}

const formatTime = (value) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

const fetchData = async () => {
  tableLoading.value = true
  try {
    const res = await listResourceSpecs({
      page: pagination.page,
      page_size: pagination.pageSize,
      keyword: searchKeyword.value || ''
    })
    tableData.value = res.data.list || []
    pagination.total = res.data.total || 0
  } catch (err) {
    // 拦截器处理错误
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

const openCreateDialog = () => {
  isEdit.value = false
  form.id = null
  form.name = ''
  form.cpu_cores = 2
  form.memory_gb = 2
  dialogVisible.value = true
}

const openEditDialog = (row) => {
  isEdit.value = true
  form.id = row.id
  form.name = row.name
  form.cpu_cores = row.cpu_cores
  form.memory_gb = row.memory_gb
  dialogVisible.value = true
}

const handleSubmit = async () => {
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) return

  submitLoading.value = true
  try {
    if (isEdit.value) {
      await updateResourceSpec(form.id, {
        name: form.name,
        cpu_cores: form.cpu_cores,
        memory_gb: form.memory_gb
      })
      ElMessage.success('更新成功')
    } else {
      await createResourceSpec({
        name: form.name,
        cpu_cores: form.cpu_cores,
        memory_gb: form.memory_gb
      })
      ElMessage.success('创建成功')
    }
    dialogVisible.value = false
    fetchData()
  } catch (err) {
    // 拦截器处理错误
  } finally {
    submitLoading.value = false
  }
}

const handleDelete = (row) => {
  ElMessageBox.confirm(
    `确定要删除资源规格「${row.name}」吗？此操作不可恢复。`,
    '删除确认',
    {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      await deleteResourceSpec(row.id)
      ElMessage.success('删除成功')
      fetchData()
    } catch (err) {
      // 拦截器处理错误
    }
  }).catch(() => {})
}

const handleBatchDelete = () => {
  const ids = selectedRows.value.map((row) => row.id)
  if (!ids.length) return

  ElMessageBox.confirm(
    `确定要删除选中的 ${ids.length} 条资源规格吗？此操作不可恢复。`,
    '批量删除确认',
    {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      await batchDeleteResourceSpecs(ids)
      ElMessage.success('批量删除成功')
      fetchData()
    } catch (err) {
      // 拦截器处理错误
    }
  }).catch(() => {})
}

onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.resource-spec-page {
  padding: 0;
}

.resource-spec-card {
  border: none;
}

.resource-spec-card :deep(.el-card__header) {
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
</style>
