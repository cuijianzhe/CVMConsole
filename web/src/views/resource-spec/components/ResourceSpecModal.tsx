/**
 * 资源规格新建/编辑弹窗
 * - 使用 useMountModalLifecycle 保留离场动画
 * - 表单字段：名称、CPU 核心数、内存大小（GB）
 */
import { useEffect, useState } from 'react'
import { Button, Input, InputNumber, Modal, Toast } from '@douyinfe/semi-ui'
import { IconSave } from '@douyinfe/semi-icons'
import { createResourceSpec, updateResourceSpec, type ResourceSpecItem } from '@/api/resourceSpec'
import { useMountModalLifecycle } from '@/hooks/useMountModalLifecycle'

interface ResourceSpecModalProps {
  /** 编辑时传入已有规格，新建时传入 null */
  item: ResourceSpecItem | null
  /** 弹窗完全关闭后回调（父组件清理状态） */
  onExited: () => void
  /** 保存成功后回调（父组件刷新列表） */
  onSuccess: () => void
}

/** 表单状态 */
interface SpecForm {
  name: string
  cpu_cores: number
  memory_gb: number
}

export default function ResourceSpecModal({ item, onExited, onSuccess }: ResourceSpecModalProps) {
  const { modalVisible, requestClose, afterModalClose } = useMountModalLifecycle(onExited)
  const [saving, setSaving] = useState(false)
  const isEdit = !!item

  const [form, setForm] = useState<SpecForm>({
    name: item?.name || '',
    cpu_cores: item?.cpu_cores || 2,
    memory_gb: item?.memory_gb || 4,
  })

  // 编辑模式下回填表单（item 变化时）
  useEffect(() => {
    if (item) {
      setForm({
        name: item.name,
        cpu_cores: item.cpu_cores,
        memory_gb: item.memory_gb,
      })
    }
  }, [item])

  const patchForm = (patch: Partial<SpecForm>) => setForm((prev) => ({ ...prev, ...patch }))

  const handleSubmit = async () => {
    const name = form.name.trim()
    if (!name) {
      Toast.error('请输入规格名称')
      return
    }
    if (form.cpu_cores <= 0) {
      Toast.error('CPU 核心数必须大于 0')
      return
    }
    if (form.memory_gb <= 0) {
      Toast.error('内存大小必须大于 0')
      return
    }
    setSaving(true)
    try {
      const payload = { name, cpu_cores: form.cpu_cores, memory_gb: form.memory_gb }
      if (isEdit && item) {
        await updateResourceSpec(item.id, payload)
        Toast.success('规格已更新')
      } else {
        await createResourceSpec(payload)
        Toast.success('规格已创建')
      }
      onSuccess()
      requestClose()
    } catch {
      // 错误提示由请求层统一处理
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title={isEdit ? '编辑资源规格' : '新建资源规格'}
      visible={modalVisible}
      onCancel={requestClose}
      afterClose={afterModalClose}
      width={460}
      maskClosable={false}
      footer={
        <>
          <Button onClick={requestClose}>取消</Button>
          <Button
            type="primary"
            theme="solid"
            icon={<IconSave />}
            loading={saving}
            onClick={() => void handleSubmit()}
          >
            {isEdit ? '保存' : '创建'}
          </Button>
        </>
      }
    >
      <div className="qvm-form-item">
        <div className="qvm-form-label required">规格名称</div>
        <Input
          value={form.name}
          onChange={(v) => patchForm({ name: v })}
          placeholder="如：2核4G-标准型"
          maxLength={100}
        />
      </div>
      <div style={{ display: 'flex', gap: 16, marginTop: 12 }}>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label required">CPU 核心数</div>
          <InputNumber
            value={form.cpu_cores}
            onNumberChange={(v) => patchForm({ cpu_cores: v })}
            min={1}
            max={256}
            style={{ width: '100%' }}
            suffix="核"
          />
        </div>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label required">内存大小</div>
          <InputNumber
            value={form.memory_gb}
            onNumberChange={(v) => patchForm({ memory_gb: v })}
            min={1}
            max={1024}
            style={{ width: '100%' }}
            suffix="GB"
          />
        </div>
      </div>
    </Modal>
  )
}
