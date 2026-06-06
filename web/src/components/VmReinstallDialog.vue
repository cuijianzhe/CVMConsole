<template>
  <el-dialog
    v-model="visible"
    title="重装系统"
    width="640px"
    append-to-body
    :close-on-click-modal="false"
    destroy-on-close
  >
    <el-alert type="warning" :closable="false" style="margin-bottom: 16px;">
      <template #title>
        重装会替换当前系统盘并自动删除全部快照，CPU、内存、网络和额外数据盘会保留，虚拟机会在任务开始时自动关机。
      </template>
    </el-alert>

    <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
      <el-form-item label="虚拟机名称">
        <el-input :model-value="vmName" disabled />
      </el-form-item>
      <el-form-item label="模板" prop="template">
        <el-select
          v-model="form.template"
          filterable
          clearable
          placeholder="请选择要重装的模板"
          style="width: 100%;"
          :loading="templateLoading"
          @change="handleTemplateChange"
        >
          <el-option
            v-for="item in templates"
            :key="item.name"
            :label="item.display_name || item.admin_name || item.name"
            :value="item.name"
          >
            <div class="template-option">
              <span>{{ item.display_name || item.admin_name || item.name }}</span>
              <span class="template-option-meta">{{ item.type || 'linux' }} / 最低 {{ resolveTemplateMinDiskSize(item) || '-' }} GB</span>
            </div>
          </el-option>
        </el-select>
      </el-form-item>
      <el-form-item label="系统盘大小" prop="disk_size">
        <el-input-number v-model="form.disk_size" :min="templateMinDiskSize || 1" :max="8192" :step="10" style="width: 100%;" />
        <div class="form-inline-hint">
          默认值为当前系统盘 {{ currentDiskSize || '-' }} GB；最低不能小于模板原始磁盘 {{ templateMinDiskSize || '-' }} GB。
        </div>
      </el-form-item>
      <el-form-item v-if="showCredentialFields" label="主机名" prop="hostname">
        <el-input v-model="form.hostname" placeholder="请输入重装后的主机名" />
      </el-form-item>
      <el-form-item v-if="showCredentialFields" label="登录用户名" prop="user">
        <el-input
          v-model="form.user"
          :disabled="isWindowsTemplate"
          :placeholder="isWindowsTemplate ? 'administrator' : '请输入登录用户名'"
        />
        <div class="form-inline-hint">
          <template v-if="isWindowsTemplate">Windows 模板固定使用 administrator。</template>
          <template v-else-if="isFnOSTemplate">FnOS 会把该账号写入为首次管理员账号。</template>
          <template v-else>仅支持小写字母、数字、下划线和短横线，且需以字母或下划线开头。</template>
        </div>
      </el-form-item>
      <el-form-item v-if="showCredentialFields" label="登录密码" prop="password">
        <el-input v-model="form.password" type="password" show-password placeholder="请输入强密码">
          <template #append>
            <el-button @click="generatePassword">随机强密码</el-button>
          </template>
        </el-input>
        <div class="form-inline-hint">
          至少 12 位，需包含大写字母、小写字母、数字和符号（支持 !@#$%^&*_-+=?）。
        </div>
      </el-form-item>
      <el-form-item v-if="isFnOSTemplate" label="FnOS 设备 ID">
        <el-radio-group v-model="form.fnos_device_id_mode">
          <el-radio label="regenerate">重新生成</el-radio>
          <el-radio label="preserve">保留模板设备 ID</el-radio>
          <el-radio label="custom">自定义</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="isFnOSTemplate && form.fnos_device_id_mode === 'custom'" label="自定义设备 ID" prop="fnos_device_id">
        <el-input v-model="form.fnos_device_id" placeholder="请输入 32 位或 40 位十六进制设备 ID" />
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="danger" :loading="submitting" @click="submitReinstall">提交重装任务</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { getTemplateList, reinstallVm } from '@/api/vm'

const visible = ref(false)
const submitting = ref(false)
const templateLoading = ref(false)
const templates = ref([])
const currentVm = ref(null)
const formRef = ref(null)

const windowsTemplateUsername = 'administrator'
const usernamePattern = /^[a-z_][a-z0-9_-]{0,31}$/
const passwordAllowedPattern = /^[A-Za-z0-9!@#$%^&*_\-+=?]+$/
const hasUppercasePattern = /[A-Z]/
const hasLowercasePattern = /[a-z]/
const hasDigitPattern = /[0-9]/
const hasSymbolPattern = /[!@#$%^&*_\-+=?]/
const fnosDeviceIdPattern = /^[0-9a-fA-F]{32}([0-9a-fA-F]{8})?$/

const form = reactive({
  template: '',
  disk_size: 0,
  hostname: '',
  user: '',
  password: '',
  fnos_device_id_mode: 'regenerate',
  fnos_device_id: ''
})

const vmName = computed(() => currentVm.value?.name || '')
const currentDiskSize = computed(() => parseDiskSizeGB(currentVm.value?.disk_size))
const selectedTemplate = computed(() => templates.value.find(item => item.name === form.template) || null)
const selectedTemplateType = computed(() => `${selectedTemplate.value?.type || ''}`.trim().toLowerCase())
const templateMinDiskSize = computed(() => resolveTemplateMinDiskSize(selectedTemplate.value))
const showCredentialFields = computed(() => ['linux', 'windows', 'fnos'].includes(selectedTemplateType.value))
const isFnOSTemplate = computed(() => selectedTemplateType.value === 'fnos')
const isWindowsTemplate = computed(() => selectedTemplateType.value === 'windows')

const rules = {
  template: [{ required: true, message: '请选择模板', trigger: 'change' }],
  disk_size: [{
    validator: (_rule, value, callback) => {
      const size = Number(value || 0)
      if (size <= 0) {
        callback(new Error('请设置系统盘大小'))
        return
      }
      if (templateMinDiskSize.value > 0 && size < templateMinDiskSize.value) {
        callback(new Error(`系统盘大小不能小于 ${templateMinDiskSize.value} GB`))
        return
      }
      callback()
    },
    trigger: 'change'
  }],
  hostname: [{
    validator: (_rule, value, callback) => {
      if (!showCredentialFields.value) {
        callback()
        return
      }
      if (!`${value || ''}`.trim()) {
        callback(new Error('请输入主机名'))
        return
      }
      callback()
    },
    trigger: 'blur'
  }],
  user: [{
    validator: (_rule, value, callback) => {
      if (!showCredentialFields.value || isWindowsTemplate.value) {
        callback()
        return
      }
      const normalized = `${value || ''}`.trim()
      if (!normalized) {
        callback(new Error('请输入登录用户名'))
        return
      }
      if (!usernamePattern.test(normalized)) {
        callback(new Error('用户名格式不正确'))
        return
      }
      callback()
    },
    trigger: 'blur'
  }],
  password: [{
    validator: (_rule, value, callback) => {
      if (!showCredentialFields.value) {
        callback()
        return
      }
      const normalized = `${value || ''}`
      if (!normalized) {
        callback(new Error('请输入登录密码'))
        return
      }
      if (normalized.length < 12) {
        callback(new Error('密码长度不能少于 12 位'))
        return
      }
      if (!passwordAllowedPattern.test(normalized) || !hasUppercasePattern.test(normalized) || !hasLowercasePattern.test(normalized) || !hasDigitPattern.test(normalized) || !hasSymbolPattern.test(normalized)) {
        callback(new Error('密码必须包含大小写字母、数字和符号'))
        return
      }
      callback()
    },
    trigger: 'blur'
  }],
  fnos_device_id: [{
    validator: (_rule, value, callback) => {
      if (!isFnOSTemplate.value || form.fnos_device_id_mode !== 'custom') {
        callback()
        return
      }
      const normalized = `${value || ''}`.trim()
      if (!normalized) {
        callback(new Error('请输入自定义设备 ID'))
        return
      }
      if (!fnosDeviceIdPattern.test(normalized)) {
        callback(new Error('设备 ID 只能为 32 位或 40 位十六进制字符串'))
        return
      }
      callback()
    },
    trigger: 'blur'
  }]
}

function parseDiskSizeGB(value) {
  const text = `${value || ''}`.trim()
  const matched = text.match(/([\d.]+)\s*GB/i)
  if (!matched) {
    return 0
  }
  const parsed = Number.parseFloat(matched[1])
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0
  }
  return Math.ceil(parsed)
}

function resolveTemplateMinDiskSize(template) {
  if (!template) {
    return 0
  }
  const text = `${template.virtual_size || ''}`.trim()
  const gibMatch = text.match(/([\d.]+)\s*GiB/i)
  if (gibMatch) {
    const parsed = Number.parseFloat(gibMatch[1])
    return Number.isFinite(parsed) && parsed > 0 ? Math.ceil(parsed) : 0
  }
  const gbMatch = text.match(/([\d.]+)\s*GB/i)
  if (gbMatch) {
    const parsed = Number.parseFloat(gbMatch[1])
    return Number.isFinite(parsed) && parsed > 0 ? Math.ceil(parsed) : 0
  }
  return 0
}

function randomFromCharset(charset, length) {
  const chars = charset.split('')
  const randomValues = new Uint32Array(length)
  window.crypto.getRandomValues(randomValues)
  return randomValues.reduce((result, value) => result + chars[value % chars.length], '')
}

function buildStrongPassword() {
  const uppercase = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
  const lowercase = 'abcdefghijkmnopqrstuvwxyz'
  const digits = '23456789'
  const symbols = '!@#$%^&*_-+=?'
  const all = uppercase + lowercase + digits + symbols
  const fixed = [
    randomFromCharset(uppercase, 1),
    randomFromCharset(lowercase, 1),
    randomFromCharset(digits, 1),
    randomFromCharset(symbols, 1)
  ]
  const rest = randomFromCharset(all, 12)
  return [...fixed.join('').concat(rest)].sort(() => Math.random() - 0.5).join('')
}

async function fetchTemplates() {
  templateLoading.value = true
  try {
    const res = await getTemplateList()
    templates.value = Array.isArray(res.data) ? res.data : []
  } finally {
    templateLoading.value = false
  }
}

function resetForm(vm = {}) {
  form.template = ''
  form.disk_size = parseDiskSizeGB(vm.disk_size) || 0
  form.hostname = vm.name || ''
  form.user = ''
  form.password = ''
  form.fnos_device_id_mode = 'regenerate'
  form.fnos_device_id = ''
}

function applyTemplateDefaults() {
  if (isWindowsTemplate.value) {
    form.user = windowsTemplateUsername
  } else if (!form.user) {
    form.user = ''
  }
  if (templateMinDiskSize.value > 0 && form.disk_size < templateMinDiskSize.value) {
    form.disk_size = templateMinDiskSize.value
  }
  if (!isFnOSTemplate.value) {
    form.fnos_device_id_mode = 'regenerate'
    form.fnos_device_id = ''
  }
}

function handleTemplateChange() {
  applyTemplateDefaults()
}

function generatePassword() {
  form.password = buildStrongPassword()
  formRef.value?.validateField('password').catch(() => false)
}

async function submitReinstall() {
  if (!formRef.value) {
    return
  }
  const valid = await formRef.value.validate().catch(() => false)
  if (!valid) {
    return
  }
  submitting.value = true
  try {
    await reinstallVm(vmName.value, {
      template: form.template,
      disk_size: Number(form.disk_size || 0),
      hostname: `${form.hostname || ''}`.trim(),
      user: isWindowsTemplate.value ? windowsTemplateUsername : `${form.user || ''}`.trim(),
      password: form.password,
      preserve_fnos_device_id: isFnOSTemplate.value && (form.fnos_device_id_mode === 'preserve' || form.fnos_device_id_mode === 'custom'),
      fnos_device_id: isFnOSTemplate.value && form.fnos_device_id_mode === 'custom' ? `${form.fnos_device_id || ''}`.trim() : ''
    })
    ElMessage.success('重装任务已提交，请在任务中心查看进度')
    visible.value = false
    emit('success')
  } finally {
    submitting.value = false
  }
}

async function open(vm) {
  currentVm.value = vm || {}
  resetForm(currentVm.value)
  visible.value = true
  await fetchTemplates()
  const currentTemplate = `${currentVm.value?.template || ''}`.trim()
  if (currentTemplate && templates.value.some(item => item.name === currentTemplate)) {
    form.template = currentTemplate
  }
  applyTemplateDefaults()
}

const emit = defineEmits(['success'])

defineExpose({
  open
})
</script>

<style scoped>
.template-option {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.template-option-meta {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.form-inline-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}
</style>
