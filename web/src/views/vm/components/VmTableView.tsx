/**
 * 虚拟机表格视图（Semi Table）
 * - 名称 / 配置 / IP 三列点击表头排序（受控）
 * - 状态与操作列纯图标展示，悬停 Tooltip
 * - 小屏隐藏次要列（模板 / IP / 运行时长 / 勾选列）
 */
import { useMemo } from 'react'
import { Empty, Table, Tooltip } from '@douyinfe/semi-ui'
import { IllustrationNoContent, IllustrationNoContentDark } from '@douyinfe/semi-illustrations'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import { IconDesktop, IconLock, IconWrench } from '@douyinfe/semi-icons'
import type { VmListItem, VmPowerAction } from '@/api/vm'
import { formatRuntime } from '@/utils/format'
import VmStatusIcon from './VmStatusIcon'
import VmResourceBars from './VmResourceBars'
import VmIpCell from './VmIpCell'
import VmMacCell from './VmMacCell'
import VmActionsCell, { type VmMenuCommand } from './VmActionsCell'
import VmTagsEditor from './VmTagsEditor'
import { shouldOpenVmDetail, autoColWidth, formatMemoryMB } from '../utils'

export type VmSortField = 'name' | 'resource' | 'ip'
export type VmSortOrder = 'ascend' | 'descend'

interface VmTableViewProps {
  vms: VmListItem[]
  loading: boolean
  selectedKeys: string[]
  onSelectionChange: (keys: string[]) => void
  sortField: VmSortField
  sortOrder: VmSortOrder
  onSortChange: (field: VmSortField, order: VmSortOrder) => void
  operatingMap: Record<string, boolean>
  isAdmin: boolean
  isLightweight: boolean
  onPower: (vm: VmListItem, action: VmPowerAction) => void
  onMenu: (cmd: VmMenuCommand, vm: VmListItem) => void
  onConsole: (vm: VmListItem) => void
  onTagsSave: (vm: VmListItem, tags: string[]) => Promise<void>
  /** 点击虚拟机列表项跳转详情页 */
  onOpenDetail: (vm: VmListItem) => void
  /** 小屏模式：隐藏次要列与勾选列 */
  compact: boolean
}

/** 列排序字段映射（dataIndex ↔ 排序状态字段） */
const SORT_FIELD_BY_INDEX: Record<string, VmSortField> = {
  name: 'name',
  cpu_percent: 'resource',
  ip: 'ip',
}

export default function VmTableView({
  vms,
  loading,
  selectedKeys,
  onSelectionChange,
  sortField,
  sortOrder,
  onSortChange,
  operatingMap,
  isAdmin,
  isLightweight,
  onPower,
  onMenu,
  onConsole,
  onTagsSave,
  onOpenDetail,
  compact,
}: VmTableViewProps) {
  // 各内容型列按列中最长字段自适应宽度（组件列如标签/配置/操作保持固定宽度）
  const colWidths = useMemo(() => {
    return {
      name: autoColWidth(vms.map((v) => v.name), { charWidth: 9, padding: 60, min: 120, max: 240 }),
      template: autoColWidth(vms.map((v) => v.template || '-'), { charWidth: 8, padding: 20, min: 90, max: 200 }),
      cpu: autoColWidth(vms.map((v) => `${v.vcpu} 核`), { charWidth: 9, padding: 20, min: 72, max: 120 }),
      memory: autoColWidth(vms.map((v) => formatMemoryMB(v.memory)), { charWidth: 8, padding: 20, min: 80, max: 120 }),
      ip: autoColWidth(vms.map((v) => v.ip || '-'), { charWidth: 8, padding: 20, min: 120, max: 160 }),
      mac: autoColWidth(vms.map((v) => v.mac_address || '未分配'), { charWidth: 8, padding: 20, min: 140, max: 180 }),
      runtime: autoColWidth(vms.map((v) => formatRuntime(v.continuous_runtime_seconds)), { charWidth: 8, padding: 20, min: 90, max: 140 }),
    }
  }, [vms])
  const columns = useMemo<ColumnProps<VmListItem>[]>(() => {
    const sortState = (field: VmSortField) => (sortField === field ? sortOrder : false)
    return [
      {
        title: '名称',
        dataIndex: 'name',
        width: colWidths.name,
        sorter: true,
        sortOrder: sortState('name'),
        render: (_text, vm) => (
          <div className="qvm-vm-cell">
            <div className={`qvm-vm-ic ${vm.status === 'running' ? '' : 'off'}`}>
              <IconDesktop size="small" />
            </div>
            <span className="qvm-vm-name-text" title={vm.remark || undefined}>
              {vm.name}
            </span>
            {vm.locked && (
              <Tooltip content="已锁定" position="top">
                <IconLock size="small" className="qvm-vm-badge lock" />
              </Tooltip>
            )}
            {vm.in_rescue && (
              <Tooltip content="救援系统中" position="top">
                <IconWrench size="small" className="qvm-vm-badge rescue" />
              </Tooltip>
            )}
            {vm.group && <span className="qvm-vm-group-tag">{vm.group}</span>}
          </div>
        ),
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 56,
        align: 'center',
        render: (_text, vm) => <VmStatusIcon status={vm.status} />,
      },
      {
        title: '模板',
        dataIndex: 'template',
        width: colWidths.template,
        className: 'col-hide-md',
        onHeaderCell: () => ({ className: 'col-hide-md' }),
        ellipsis: true,
        render: (text) => <span className="qvm-tpl-name">{text || '-'}</span>,
      },
      {
        title: '标签',
        dataIndex: 'tags',
        width: 130,
        render: (_text, vm) => <VmTagsEditor vm={vm} onSave={onTagsSave} />,
      },
      {
        title: 'CPU',
        dataIndex: 'vcpu',
        width: colWidths.cpu,
        align: 'center',
        render: (vcpu) => <span className="qvm-vm-spec">{vcpu} 核</span>,
      },
      {
        title: '内存',
        dataIndex: 'memory',
        width: colWidths.memory,
        align: 'center',
        render: (mem) => <span className="qvm-vm-spec">{formatMemoryMB(Number(mem))}</span>,
      },
      {
        title: '配置 (资源使用)',
        dataIndex: 'cpu_percent',
        sorter: true,
        sortOrder: sortState('resource'),
        width: 230,
        render: (_text, vm) => <VmResourceBars vm={vm} />,
      },
      {
        title: 'IP 地址',
        dataIndex: 'ip',
        sorter: true,
        sortOrder: sortState('ip'),
        width: colWidths.ip,
        className: 'col-hide-sm',
        onHeaderCell: () => ({ className: 'col-hide-sm' }),
        render: (_text, vm) => <VmIpCell vm={vm} />,
      },
      {
        title: 'MAC 地址',
        dataIndex: 'mac_address',
        width: colWidths.mac,
        className: 'col-hide-sm',
        onHeaderCell: () => ({ className: 'col-hide-sm' }),
        render: (_text, vm) => <VmMacCell vm={vm} />,
      },
      {
        title: '运行时长',
        dataIndex: 'continuous_runtime_seconds',
        width: colWidths.runtime,
        className: 'col-hide-sm',
        onHeaderCell: () => ({ className: 'col-hide-sm' }),
        render: (_text, vm) => {
          const text =
            vm.status === 'running' || vm.status === 'paused'
              ? formatRuntime(vm.continuous_runtime_seconds)
              : '—'
          if (vm.continuous_running_since && text !== '—') {
            return (
              <Tooltip content={`开始时间：${vm.continuous_running_since}`} position="top">
                <span className="qvm-runtime">{text}</span>
              </Tooltip>
            )
          }
          return <span className="qvm-runtime">{text}</span>
        },
      },
      {
        title: '操作',
        dataIndex: 'actions',
        width: 120,
        render: (_text, vm) => (
          <VmActionsCell
            vm={vm}
            isAdmin={isAdmin}
            isLightweight={isLightweight}
            operating={!!operatingMap[vm.name]}
            onPower={onPower}
            onMenu={onMenu}
            onConsole={onConsole}
          />
        ),
      },
    ]
  }, [
    sortField,
    sortOrder,
    operatingMap,
    isAdmin,
    isLightweight,
    onPower,
    onMenu,
    onConsole,
    onTagsSave,
    colWidths,
  ])

  return (
    <div className="qvm-vm-table-wrap">
      <Table<VmListItem>
        rowKey="name"
        className="qvm-vm-table"
        columns={columns}
        dataSource={vms}
        loading={loading}
        pagination={false}
        size="middle"
        onRow={(vm) => ({
          className: 'qvm-vm-table-row-clickable',
          onClick: (event) => {
            if (vm && shouldOpenVmDetail(event.target)) onOpenDetail(vm)
          },
        })}
        rowSelection={
          compact
            ? undefined
            : {
                selectedRowKeys: selectedKeys,
                onChange: (keys) => onSelectionChange((keys || []) as string[]),
              }
        }
        onChange={({ sorter }) => {
          const field = SORT_FIELD_BY_INDEX[(sorter?.dataIndex as string) || '']
          if (field) {
            // Semi 排序循环含第三态 false（取消排序），本页始终保留排序，映射回升序
            const order = sorter?.sortOrder === 'descend' ? 'descend' : 'ascend'
            onSortChange(field, order)
          }
        }}
        empty={
          <Empty
            image={<IllustrationNoContent />}
            darkModeImage={<IllustrationNoContentDark />}
            title="暂无虚拟机"
            description="点击右上角「新建虚拟机」开始创建"
          />
        }
      />
    </div>
  )
}
