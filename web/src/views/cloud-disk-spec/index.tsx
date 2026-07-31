/**
 * 云盘规格管理页（仅管理员可写，所有用户可读）
 * - 管理云盘规格模板：定义规格名称、容量、磁盘类型、格式、IOPS 限速等
 * - 创建虚拟机/挂载云盘时按规格快速选择磁盘配置
 * - 表格列：名称、容量、磁盘类型、格式、IOPS、描述、创建时间、操作
 * - 行内操作：编辑/删除（纯图标 + Tooltip，遵循 cds-act-ic 模式）
 */
import { useCallback, useEffect, useState } from 'react'
import { Button, Empty, Modal, Spin, Table, Tag, Toast, Tooltip } from '@douyinfe/semi-ui'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import { IconDelete, IconEditStroked, IconPlus, IconRefresh } from '@douyinfe/semi-icons'
import { useUserStore } from '@/stores/user'
import { ROLES } from '@/config/constants'
import {
  listCloudDiskSpecs,
  deleteCloudDiskSpec,
  type CloudDiskSpecItem,
  type DiskType,
} from '@/api/cloudDiskSpec'
import CloudDiskSpecModal from './components/CloudDiskSpecModal'
import './cloud-disk-spec.css'

/** 格式化时间戳为可读日期 */
function formatDate(iso: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '-'
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** 磁盘类型中文文案 */
function diskTypeText(t: DiskType): string {
  return t === 'SYSTEM' ? '系统盘' : '数据盘'
}

/** 磁盘类型标签配色 */
function diskTypeTagColor(t: DiskType): 'orange' | 'blue' {
  return t === 'SYSTEM' ? 'orange' : 'blue'
}

/** 容量展示：能整除 1024 则显示 TB */
function formatCapacity(gb: number): string {
  if (gb >= 1024 && gb % 1024 === 0) return `${gb / 1024} TB`
  return `${gb} GB`
}

/** IOPS 摘要展示 */
function formatIOPS(row: CloudDiskSpecItem): string {
  if (row.iops_mode === 'TOTAL') {
    return row.total_iops > 0 ? `${row.total_iops}` : '不限'
  }
  // READ_WRITE 模式
  const r = row.read_iops > 0 ? `${row.read_iops}` : '不限'
  const w = row.write_iops > 0 ? `${row.write_iops}` : '不限'
  return `R:${r} / W:${w}`
}

export default function CloudDiskSpecPage() {
  const role = useUserStore((s) => s.role)
  const isAdmin = role === ROLES.admin

  const [list, setList] = useState<CloudDiskSpecItem[]>([])
  const [loading, setLoading] = useState(false)
  // 弹窗状态：null=关闭，{ mode: 'create' }=新建，{ mode: 'edit', item }=编辑
  const [modal, setModal] = useState<
    { mode: 'create' } | { mode: 'edit'; item: CloudDiskSpecItem } | null
  >(null)

  // ==================== 数据加载 ====================
  const loadData = useCallback(async () => {
    if (!isAdmin) return
    setLoading(true)
    try {
      const res = await listCloudDiskSpecs({ page: 1, page_size: 100 })
      setList(res.data?.list || [])
    } catch {
      // 错误提示由请求层统一处理
    } finally {
      setLoading(false)
    }
  }, [isAdmin])

  useEffect(() => {
    void loadData()
  }, [loadData])

  // ==================== 行内操作 ====================
  const handleCreate = () => setModal({ mode: 'create' })
  const handleEdit = (row: CloudDiskSpecItem) => setModal({ mode: 'edit', item: row })

  const handleDelete = (row: CloudDiskSpecItem) => {
    Modal.confirm({
      title: '删除云盘规格',
      content: `确定要删除规格「${row.name}」吗？此操作不可撤销。`,
      okText: '删除',
      cancelText: '取消',
      okButtonProps: { type: 'danger', theme: 'solid' },
      onOk: async () => {
        try {
          await deleteCloudDiskSpec(row.id)
          Toast.success('删除成功')
          loadData()
        } catch {
          // 错误提示由请求层统一处理
        }
      },
    })
  }

  // ==================== 表格列定义 ====================
  const columns: ColumnProps<CloudDiskSpecItem>[] = [
    { title: '名称', dataIndex: 'name', width: 200 },
    {
      title: '容量',
      dataIndex: 'capacity_gb',
      width: 110,
      render: (v: number) => <span className="cds-num">{formatCapacity(v)}</span>,
    },
    {
      title: '磁盘类型',
      dataIndex: 'disk_type',
      width: 100,
      render: (_v: unknown, row: CloudDiskSpecItem) => (
        <Tag color={diskTypeTagColor(row.disk_type)} size="small">
          {diskTypeText(row.disk_type)}
        </Tag>
      ),
    },
    {
      title: '格式',
      dataIndex: 'disk_format',
      width: 90,
      render: (v: string) => <span className="cds-mono">{v}</span>,
    },
    {
      title: 'IOPS',
      width: 160,
      render: (_v: unknown, row: CloudDiskSpecItem) => (
        <span className="cds-iops">{formatIOPS(row)}</span>
      ),
    },
    {
      title: '描述',
      dataIndex: 'description',
      width: 180,
      render: (v: string) => (
        <span className="cds-desc" title={v || ''}>
          {v || '-'}
        </span>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 120,
      render: (v: string) => <span className="cds-date">{formatDate(v)}</span>,
    },
    ...(isAdmin
      ? [
          {
            title: '操作',
            width: 100,
            fixed: 'right' as const,
            render: (_v: unknown, row: CloudDiskSpecItem) => (
              <div className="cds-act-cell" onClick={(e) => e.stopPropagation()}>
                <Tooltip content="编辑" position="top">
                  <span className="cds-act-ic edit" onClick={() => handleEdit(row)}>
                    <IconEditStroked />
                  </span>
                </Tooltip>
                <Tooltip content="删除" position="top">
                  <span className="cds-act-ic del" onClick={() => handleDelete(row)}>
                    <IconDelete />
                  </span>
                </Tooltip>
              </div>
            ),
          },
        ]
      : []),
  ]

  // ==================== 非管理员提示 ====================
  if (!isAdmin) {
    return (
      <div className="cds-page">
        <div className="cds-empty">
          <div className="cds-empty-icon">
            <IconRefresh />
          </div>
          <div>云盘规格管理仅对管理员开放</div>
        </div>
      </div>
    )
  }

  return (
    <div className="cds-page">
      {/* ==================== 页头 ==================== */}
      <div className="cds-page-header">
        <div>
          <h2>云盘规格</h2>
          <p className="cds-page-sub">
            管理云盘规格模板，定义磁盘容量、类型、格式与 IOPS 限速
          </p>
        </div>
        <div className="cds-header-actions">
          <Button type="primary" theme="light" icon={<IconPlus />} onClick={handleCreate}>
            新建
          </Button>
          <Button icon={<IconRefresh />} loading={loading} onClick={() => void loadData()}>
            刷新
          </Button>
        </div>
      </div>

      {/* ==================== 表格 ==================== */}
      <Spin spinning={loading} size="large" style={{ display: 'block' }}>
        <div className="cds-table-card">
          <Table<CloudDiskSpecItem>
            columns={columns}
            dataSource={list}
            rowKey="id"
            pagination={false}
            scroll={{ x: 1100 }}
            empty={
              <Empty
                title="暂无云盘规格"
                description="点击右上角「新建」创建云盘规格模板"
              />
            }
          />
        </div>
      </Spin>

      {/* ==================== 新建/编辑弹窗（按需挂载，保留离场动画） ==================== */}
      {modal && (
        <CloudDiskSpecModal
          item={modal.mode === 'edit' ? modal.item : null}
          onExited={() => setModal(null)}
          onSuccess={() => void loadData()}
        />
      )}
    </div>
  )
}
