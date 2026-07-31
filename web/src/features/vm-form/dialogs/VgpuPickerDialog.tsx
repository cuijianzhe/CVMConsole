/**
 * vGPU 实例选择弹窗（仅管理员，创建 / 编辑共用）
 * 展示宿主机上所有可用的（未绑定的）vGPU 实例，勾选后挂载到虚拟机。
 */
import { useEffect, useState } from 'react'
import { Banner, Modal, Table, Tag } from '@douyinfe/semi-ui'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import type { VGPUInstance } from '@/api/settings'
import { useVmFormScope } from '../scopeContext'

interface VgpuPickerDialogProps {
  visible: boolean
  onClose: () => void
}

export default function VgpuPickerDialog({ visible, onClose }: VgpuPickerDialogProps) {
  const { form, options } = useVmFormScope()
  const { form: f, setField } = form
  const [loading, setLoading] = useState(false)
  const [selectedKeys, setSelectedKeys] = useState<string[]>([])

  // 弹窗打开时刷新实例列表
  useEffect(() => {
    if (!visible) return
    setSelectedKeys([])
    setLoading(true)
    void options.loadVGPUInstances().finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible])

  /** 可选条件：状态为 available 且未被当前表单选中 */
  const isSelectable = (row: VGPUInstance) => {
    if (row.status && row.status !== 'available') return false
    if (row.bound_vm) return false
    return !f.vgpu_instances.some((i) => i.uuid === row.uuid)
  }

  const handleOk = () => {
    const selected = options.vgpuInstances.filter((i) => selectedKeys.includes(i.uuid))
    const next = [...f.vgpu_instances]
    for (const inst of selected) {
      if (!next.some((i) => i.uuid === inst.uuid)) {
        next.push({ uuid: inst.uuid })
      }
    }
    setField('vgpu_instances', next)
    setField('vgpu_instances_touched', true)
    onClose()
  }

  const columns: ColumnProps<VGPUInstance>[] = [
    {
      title: 'UUID',
      dataIndex: 'uuid',
      width: 300,
      render: (text: string) => <span className="qvm-vf-mono">{text}</span>,
    },
    {
      title: '所属配置',
      dataIndex: 'profile_name',
      width: 180,
      render: (text: string, row: VGPUInstance) => text || row.profile_id || '—',
    },
    {
      title: 'PCI 设备',
      dataIndex: 'pci_device',
      width: 150,
      render: (text?: string) => (text ? <span className="qvm-vf-mono">{text}</span> : '—'),
    },
    {
      title: '显存',
      dataIndex: 'memory_mb',
      width: 90,
      align: 'center' as const,
      render: (mb?: number) => (mb ? `${mb} MB` : '—'),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      align: 'center' as const,
      render: (status: string, row: VGPUInstance) => {
        if (row.bound_vm) return <Tag size="small" color="blue">已绑定 {row.bound_vm}</Tag>
        if (!status || status === 'available')
          return <Tag size="small" color="green">可用</Tag>
        return <Tag size="small" color="red">{status}</Tag>
      },
    },
  ]

  return (
    <Modal
      title="选择 vGPU 实例"
      visible={visible}
      onCancel={onClose}
      onOk={handleOk}
      okText="添加选中实例"
      cancelText="取消"
      width={760}
      closeOnEsc
    >
      <Banner
        type="info"
        closeIcon={null}
        style={{ marginBottom: 12 }}
        description="仅展示未绑定到虚拟机的可用 vGPU 实例。选择后将在虚拟机启动时挂载，修改 vGPU 实例需要先关机。"
      />
      <Table<VGPUInstance>
        rowKey="uuid"
        size="small"
        bordered
        loading={loading}
        columns={columns}
        dataSource={options.vgpuInstances}
        pagination={false}
        scroll={{ y: 400 }}
        rowSelection={{
          selectedRowKeys: selectedKeys,
          onChange: (keys) => setSelectedKeys((keys || []) as string[]),
          getCheckboxProps: (row?: VGPUInstance) => ({ disabled: row ? !isSelectable(row) : true }),
        }}
      />
    </Modal>
  )
}
