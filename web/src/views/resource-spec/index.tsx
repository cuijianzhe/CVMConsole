/**
 * 资源规格管理页（仅管理员可写，所有用户可读）
 * - 管理虚拟机资源规格模板：定义规格名称、CPU 核心数、内存大小
 * - 开通虚拟机时按规格快速选择 CPU/内存配比
 * - 表格列：名称、CPU、内存、创建时间、操作
 * - 行内操作：编辑/删除（纯图标 + Tooltip，遵循 qvm-act-ic 模式）
 */
import { useCallback, useEffect, useState } from 'react'
import { Button, Empty, Modal, Spin, Table, Toast, Tooltip } from '@douyinfe/semi-ui'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import { IconDelete, IconEditStroked, IconPlus, IconRefresh } from '@douyinfe/semi-icons'
import { useUserStore } from '@/stores/user'
import { ROLES } from '@/config/constants'
import {
  listResourceSpecs,
  deleteResourceSpec,
  type ResourceSpecItem,
} from '@/api/resourceSpec'
import ResourceSpecModal from './components/ResourceSpecModal'
import './resource-spec.css'

/** 格式化时间戳为可读日期 */
function formatDate(iso: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '-'
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

export default function ResourceSpecPage() {
  const role = useUserStore((s) => s.role)
  const isAdmin = role === ROLES.admin

  const [list, setList] = useState<ResourceSpecItem[]>([])
  const [loading, setLoading] = useState(false)
  // 弹窗状态：null=关闭，{ mode: 'create' }=新建，{ mode: 'edit', item }=编辑
  const [modal, setModal] = useState<
    { mode: 'create' } | { mode: 'edit'; item: ResourceSpecItem } | null
  >(null)

  // ==================== 数据加载 ====================
  const loadData = useCallback(async () => {
    if (!isAdmin) return
    setLoading(true)
    try {
      const res = await listResourceSpecs({ page: 1, page_size: 100 })
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
  const handleEdit = (row: ResourceSpecItem) => setModal({ mode: 'edit', item: row })

  const handleDelete = (row: ResourceSpecItem) => {
    Modal.confirm({
      title: '删除资源规格',
      content: `确定要删除规格「${row.name}」吗？此操作不可撤销。`,
      okText: '删除',
      cancelText: '取消',
      okButtonProps: { type: 'danger', theme: 'solid' },
      onOk: async () => {
        try {
          await deleteResourceSpec(row.id)
          Toast.success('删除成功')
          loadData()
        } catch {
          // 错误提示由请求层统一处理
        }
      },
    })
  }

  // ==================== 表格列定义 ====================
  const columns: ColumnProps<ResourceSpecItem>[] = [
    { title: '名称', dataIndex: 'name', width: 220 },
    {
      title: 'CPU',
      dataIndex: 'cpu_cores',
      width: 120,
      render: (v: number) => <span className="rs-num">{v} 核</span>,
    },
    {
      title: '内存',
      dataIndex: 'memory_gb',
      width: 120,
      render: (v: number) => <span className="rs-num">{v} GB</span>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 140,
      render: (v: string) => <span className="rs-date">{formatDate(v)}</span>,
    },
    ...(isAdmin
      ? [
          {
            title: '操作',
            width: 100,
            fixed: 'right' as const,
            render: (_v: unknown, row: ResourceSpecItem) => (
              <div className="rs-act-cell" onClick={(e) => e.stopPropagation()}>
                <Tooltip content="编辑" position="top">
                  <span className="rs-act-ic edit" onClick={() => handleEdit(row)}>
                    <IconEditStroked />
                  </span>
                </Tooltip>
                <Tooltip content="删除" position="top">
                  <span className="rs-act-ic del" onClick={() => handleDelete(row)}>
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
      <div className="rs-page">
        <div className="rs-empty">
          <div className="rs-empty-icon">
            <IconRefresh />
          </div>
          <div>资源规格管理仅对管理员开放</div>
        </div>
      </div>
    )
  }

  return (
    <div className="rs-page">
      {/* ==================== 页头 ==================== */}
      <div className="rs-page-header">
        <div>
          <h2>资源规格</h2>
          <p className="rs-page-sub">管理虚拟机资源规格模板，定义 CPU 核心数与内存大小</p>
        </div>
        <div className="rs-header-actions">
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
        <div className="rs-table-card">
          <Table<ResourceSpecItem>
            columns={columns}
            dataSource={list}
            rowKey="id"
            pagination={false}
            empty={
              <Empty
                title="暂无资源规格"
                description="点击右上角「新建」创建资源规格模板"
              />
            }
          />
        </div>
      </Spin>

      {/* ==================== 新建/编辑弹窗（按需挂载，保留离场动画） ==================== */}
      {modal && (
        <ResourceSpecModal
          item={modal.mode === 'edit' ? modal.item : null}
          onExited={() => setModal(null)}
          onSuccess={() => void loadData()}
        />
      )}
    </div>
  )
}
