/**
 * vGPU 实例分区（仅管理员，创建 / 编辑共用）
 * 展示已选中的 vGPU 实例，支持添加（弹窗选择未绑定实例）与移除。
 * 仅管理员可见，批量克隆时该分区不显示（vGPU 实例无法在批量场景复用）。
 */
import { useState } from 'react'
import { Banner, Button, Table, Tag, Tooltip } from '@douyinfe/semi-ui'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import { IconPlus, IconVideo, IconDelete } from '@douyinfe/semi-icons'
import SectionCard from './SectionCard'
import { useVmFormScope } from '../scopeContext'
import VgpuPickerDialog from '../dialogs/VgpuPickerDialog'

export default function VgpuSection() {
  const { form, options, ctx } = useVmFormScope()
  const { form: f, setField } = form
  const [pickerVisible, setPickerVisible] = useState(false)
  const runningOrPaused = ctx.vmStatus === 'running' || ctx.vmStatus === 'paused'

  // 仅管理员可见
  if (!ctx.isAdmin) return null

  /** 根据 UUID 查找实例详情（显示配置名 / PCI 设备等） */
  const instanceInfo = (uuid: string) =>
    options.vgpuInstances.find((i) => i.uuid === uuid)

  /** 移除已选 vGPU 实例 */
  const removeInstance = (index: number) => {
    setField('vgpu_instances', f.vgpu_instances.filter((_, i) => i !== index))
    setField('vgpu_instances_touched', true)
  }

  const columns: ColumnProps<{ uuid: string }>[] = [
    {
      title: 'UUID',
      dataIndex: 'uuid',
      width: 300,
      render: (text: string) => <span className="qvm-vf-mono">{text}</span>,
    },
    {
      title: '所属配置',
      dataIndex: 'uuid',
      render: (uuid: string) => {
        const info = instanceInfo(uuid)
        return info?.profile_name || info?.profile_id || '—'
      },
    },
    {
      title: 'PCI 设备',
      dataIndex: 'uuid',
      width: 160,
      render: (uuid: string) => {
        const info = instanceInfo(uuid)
        return info?.pci_device ? <span className="qvm-vf-mono">{info.pci_device}</span> : '—'
      },
    },
    {
      title: '状态',
      dataIndex: 'uuid',
      width: 100,
      align: 'center' as const,
      render: (uuid: string) => {
        const info = instanceInfo(uuid)
        const boundVm = info?.bound_vm
        if (boundVm) return <Tag size="small" color="blue">已绑定</Tag>
        return <Tag size="small" color="green">待挂载</Tag>
      },
    },
    {
      title: '操作',
      dataIndex: 'uuid',
      width: 70,
      align: 'center' as const,
      render: (_: string, __: { uuid: string }, index: number) => (
        <Tooltip content="移除" position="top">
          <span
            className="qvm-act-ic vgpu-remove"
            onClick={() => removeInstance(index)}
          >
            <IconDelete />
          </span>
        </Tooltip>
      ),
    },
  ]

  return (
    <SectionCard icon={<IconVideo />} title="vGPU 实例">
      <Banner
        type="info"
        closeIcon={null}
        style={{ marginBottom: 14 }}
        description="vGPU（虚拟 GPU）可将物理 GPU 切分为多个虚拟实例分配给虚拟机。请先在「系统设置 → vGPU 管理」中创建实例，再在此处选择未绑定的实例挂载。"
      />
      {f.vgpu_instances.length > 0 ? (
        <Table<{ uuid: string }>
          rowKey="uuid"
          size="small"
          bordered
          columns={columns}
          dataSource={f.vgpu_instances}
          pagination={false}
          style={{ marginBottom: 10 }}
        />
      ) : (
        <div className="qvm-vf-empty-text">未选择 vGPU 实例</div>
      )}
      <Button
        type="primary"
        theme="light"
        size="small"
        icon={<IconPlus />}
        onClick={() => setPickerVisible(true)}
      >
        添加 vGPU 实例
      </Button>
      {ctx.mode === 'edit' && runningOrPaused && (
        <div className="qvm-vf-tip warn" style={{ marginTop: 8 }}>
          修改 vGPU 实例需要先关机
        </div>
      )}
      <VgpuPickerDialog visible={pickerVisible} onClose={() => setPickerVisible(false)} />
    </SectionCard>
  )
}
