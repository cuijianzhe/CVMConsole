/**
 * 云盘规格新建/编辑弹窗
 * - 使用 useMountModalLifecycle 保留离场动画
 * - 表单字段：名称、磁盘类型、容量、存储位置、磁盘格式、IOPS 模式与限速、描述
 */
import { useEffect, useState } from 'react'
import { Button, Input, InputNumber, Modal, Select, Tag, TextArea, Toast } from '@douyinfe/semi-ui'
import { IconSave } from '@douyinfe/semi-icons'
import {
  createCloudDiskSpec,
  updateCloudDiskSpec,
  type CloudDiskSpecItem,
  type DiskType,
  type DiskFormat,
  type IOPSMode,
} from '@/api/cloudDiskSpec'
import { getVMStorageTargets, type VmStorageTarget } from '@/api/infra'
import { formatBytes } from '@/utils/format'
import { useMountModalLifecycle } from '@/hooks/useMountModalLifecycle'

interface CloudDiskSpecModalProps {
  /** 编辑时传入已有规格，新建时传入 null */
  item: CloudDiskSpecItem | null
  /** 弹窗完全关闭后回调（父组件清理状态） */
  onExited: () => void
  /** 保存成功后回调（父组件刷新列表） */
  onSuccess: () => void
}

/** 表单状态 */
interface SpecForm {
  name: string
  disk_type: DiskType
  capacity_gb: number
  storage_location: string
  disk_format: DiskFormat
  iops_mode: IOPSMode
  total_iops: number
  read_iops: number
  write_iops: number
  description: string
}

/** 默认表单值（新建时使用） */
const DEFAULT_FORM: SpecForm = {
  name: '',
  disk_type: 'DATA',
  capacity_gb: 50,
  storage_location: '',
  disk_format: 'QCOW2',
  iops_mode: 'READ_WRITE',
  total_iops: 0,
  read_iops: 0,
  write_iops: 0,
  description: '',
}

export default function CloudDiskSpecModal({
  item,
  onExited,
  onSuccess,
}: CloudDiskSpecModalProps) {
  const { modalVisible, requestClose, afterModalClose } = useMountModalLifecycle(onExited)
  const [saving, setSaving] = useState(false)
  const isEdit = !!item
  const [storageTargets, setStorageTargets] = useState<VmStorageTarget[]>([])
  const [storageLoading, setStorageLoading] = useState(false)

  // 加载存储位置列表
  useEffect(() => {
    setStorageLoading(true)
    getVMStorageTargets()
      .then((res) => setStorageTargets(res.data || []))
      .catch(() => setStorageTargets([]))
      .finally(() => setStorageLoading(false))
  }, [])

  const [form, setForm] = useState<SpecForm>(() =>
    item
      ? {
          name: item.name,
          disk_type: item.disk_type,
          capacity_gb: item.capacity_gb,
          storage_location: item.storage_location || '',
          disk_format: item.disk_format,
          iops_mode: item.iops_mode,
          total_iops: item.total_iops || 0,
          read_iops: item.read_iops || 0,
          write_iops: item.write_iops || 0,
          description: item.description || '',
        }
      : { ...DEFAULT_FORM },
  )

  // 编辑模式下回填表单（item 变化时）
  useEffect(() => {
    if (item) {
      setForm({
        name: item.name,
        disk_type: item.disk_type,
        capacity_gb: item.capacity_gb,
        storage_location: item.storage_location || '',
        disk_format: item.disk_format,
        iops_mode: item.iops_mode,
        total_iops: item.total_iops || 0,
        read_iops: item.read_iops || 0,
        write_iops: item.write_iops || 0,
        description: item.description || '',
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
    if (form.capacity_gb <= 0) {
      Toast.error('容量必须大于 0')
      return
    }
    setSaving(true)
    try {
      const payload = {
        name,
        disk_type: form.disk_type,
        capacity_gb: form.capacity_gb,
        storage_location: form.storage_location.trim(),
        disk_format: form.disk_format,
        iops_mode: form.iops_mode,
        total_iops: form.total_iops,
        read_iops: form.read_iops,
        write_iops: form.write_iops,
        description: form.description.trim(),
      }
      if (isEdit && item) {
        await updateCloudDiskSpec(item.id, payload)
        Toast.success('规格已更新')
      } else {
        await createCloudDiskSpec(payload)
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
      title={isEdit ? '编辑云盘规格' : '新建云盘规格'}
      visible={modalVisible}
      onCancel={requestClose}
      afterClose={afterModalClose}
      width={560}
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
      {/* 基本信息 */}
      <div style={{ display: 'flex', gap: 16 }}>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label required">规格名称</div>
          <Input
            value={form.name}
            onChange={(v) => patchForm({ name: v })}
            placeholder="如：50GB-数据盘-标准型"
            maxLength={100}
          />
        </div>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label required">磁盘类型</div>
          <Select
            value={form.disk_type}
            onChange={(v) => patchForm({ disk_type: v as DiskType })}
            style={{ width: '100%' }}
            optionList={[
              { label: '系统盘', value: 'SYSTEM' },
              { label: '数据盘', value: 'DATA' },
            ]}
          />
        </div>
      </div>

      {/* 容量与格式 */}
      <div style={{ display: 'flex', gap: 16, marginTop: 12 }}>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label required">容量</div>
          <InputNumber
            value={form.capacity_gb}
            onNumberChange={(v) => patchForm({ capacity_gb: v })}
            min={1}
            max={65536}
            style={{ width: '100%' }}
            suffix="GB"
          />
        </div>
        <div className="qvm-form-item" style={{ flex: 1 }}>
          <div className="qvm-form-label">磁盘格式</div>
          <Select
            value={form.disk_format}
            onChange={(v) => patchForm({ disk_format: v as DiskFormat })}
            style={{ width: '100%' }}
            optionList={[
              { label: 'QCOW2（推荐，支持快照）', value: 'QCOW2' },
              { label: 'RAW（性能优先）', value: 'RAW' },
            ]}
          />
        </div>
      </div>

      {/* 存储位置 */}
      <div className="qvm-form-item" style={{ marginTop: 12 }}>
        <div className="qvm-form-label">存储位置</div>
        <Select
          value={form.storage_location || undefined}
          onChange={(v) => patchForm({ storage_location: String(v) })}
          placeholder="留空则使用默认存储池"
          style={{ width: '100%' }}
          loading={storageLoading}
          allowClear
        >
          {storageTargets.map((target) => (
            <Select.Option key={target.id} value={target.id}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span>{target.display_name}</span>
                {target.is_default && <Tag size="small" color="blue">默认</Tag>}
                {target.available !== undefined && (
                  <Tag size="small" color="grey">{formatBytes(target.available)} 可用</Tag>
                )}
              </div>
            </Select.Option>
          ))}
        </Select>
      </div>

      {/* IOPS 限速 */}
      <div className="qvm-form-item" style={{ marginTop: 12 }}>
        <div className="qvm-form-label">IOPS 限制模式</div>
        <Select
          value={form.iops_mode}
          onChange={(v) => patchForm({ iops_mode: v as IOPSMode })}
          style={{ width: '100%' }}
          optionList={[
            { label: '读写分别限制（READ_WRITE）', value: 'READ_WRITE' },
            { label: '总 IOPS 限制（TOTAL）', value: 'TOTAL' },
          ]}
        />
      </div>

      {form.iops_mode === 'TOTAL' ? (
        <div className="qvm-form-item" style={{ marginTop: 12 }}>
          <div className="qvm-form-label">总 IOPS 上限</div>
          <InputNumber
            value={form.total_iops}
            onNumberChange={(v) => patchForm({ total_iops: v })}
            min={0}
            max={1000000}
            style={{ width: '100%' }}
            suffix="IOPS"
          />
          <div className="qvm-form-tip">设为 0 表示不限制</div>
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 16, marginTop: 12 }}>
          <div className="qvm-form-item" style={{ flex: 1 }}>
            <div className="qvm-form-label">读 IOPS 上限</div>
            <InputNumber
              value={form.read_iops}
              onNumberChange={(v) => patchForm({ read_iops: v })}
              min={0}
              max={1000000}
              style={{ width: '100%' }}
              suffix="IOPS"
            />
          </div>
          <div className="qvm-form-item" style={{ flex: 1 }}>
            <div className="qvm-form-label">写 IOPS 上限</div>
            <InputNumber
              value={form.write_iops}
              onNumberChange={(v) => patchForm({ write_iops: v })}
              min={0}
              max={1000000}
              style={{ width: '100%' }}
              suffix="IOPS"
            />
          </div>
        </div>
      )}

      {/* 描述 */}
      <div className="qvm-form-item" style={{ marginTop: 12 }}>
        <div className="qvm-form-label">描述</div>
        <TextArea
          value={form.description}
          onChange={(v) => patchForm({ description: v })}
          placeholder="可选，规格用途说明"
          maxLength={200}
          autosize={{ minRows: 2, maxRows: 4 }}
        />
      </div>
    </Modal>
  )
}
