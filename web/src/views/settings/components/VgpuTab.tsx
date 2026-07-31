/**
 * vGPU 管理 Tab：vGPU 配置列表 + 实例管理（发现设备 / 创建实例 / 销毁实例）
 * 独立操作区，无需整体保存（与诊断导出 / 存储管理 Tab 一致）。
 */
import { useCallback, useEffect, useState } from 'react'
import {
  Banner,
  Button,
  Empty,
  Modal,
  Select,
  Spin,
  Table,
  Tag,
  Toast,
  Tooltip,
} from '@douyinfe/semi-ui'
import type { ColumnProps } from '@douyinfe/semi-ui/lib/es/table'
import {
  IconDelete,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconVideo,
} from '@douyinfe/semi-icons'
import {
  createVGPUInstance,
  destroyVGPUInstance,
  discoverVGPUProfiles,
  getVGPUInstances,
  getVGPUProfiles,
  type VGPUInstance,
  type VGPUProfileInfo,
} from '@/api/settings'
import { confirmModal } from '@/utils/confirm'
import { formatMB } from '@/utils/format'
import { SectionHead } from './SettingRow'

/** vGPU 实例状态标签颜色映射 */
function vgpuStatusTag(status?: string) {
  switch (status) {
    case 'available':
      return <Tag size="small" color="green">可用</Tag>
    case 'bound':
      return <Tag size="small" color="blue">已绑定</Tag>
    case 'error':
      return <Tag size="small" color="red">异常</Tag>
    default:
      return <Tag size="small" color="grey">{status || '未知'}</Tag>
  }
}

export default function VgpuTab() {
  const [profiles, setProfiles] = useState<VGPUProfileInfo[]>([])
  const [instances, setInstances] = useState<VGPUInstance[]>([])
  const [loading, setLoading] = useState(false)
  const [discovering, setDiscovering] = useState(false)
  const [createVisible, setCreateVisible] = useState(false)
  const [selectedProfile, setSelectedProfile] = useState<string>('')
  const [creating, setCreating] = useState(false)
  const [destroyingId, setDestroyingId] = useState<string>('')

  // ==================== 数据加载 ====================
  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const [profileRes, instanceRes] = await Promise.all([
        getVGPUProfiles(),
        getVGPUInstances(),
      ])
      setProfiles(profileRes.data || [])
      setInstances(instanceRes.data || [])
    } catch {
      // 请求层已统一提示
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchAll()
  }, [fetchAll])

  // ==================== 发现设备 ====================
  const handleDiscover = async () => {
    setDiscovering(true)
    try {
      const res = await discoverVGPUProfiles()
      setProfiles(res.data || [])
      Toast.success(res.message || 'vGPU 设备发现完成')
    } catch {
      // 请求层已统一提示
    } finally {
      setDiscovering(false)
    }
  }

  // ==================== 创建实例 ====================
  const openCreate = () => {
    setSelectedProfile(profiles[0]?.profile_name || '')
    setCreateVisible(true)
  }

  const handleCreate = async () => {
    if (!selectedProfile) {
      Toast.warning('请选择 vGPU 配置')
      return
    }
    setCreating(true)
    try {
      const res = await createVGPUInstance({ profile_id: selectedProfile })
      Toast.success(res.message || 'vGPU 实例已创建')
      setCreateVisible(false)
      await fetchAll()
    } catch {
      // 请求层已统一提示
    } finally {
      setCreating(false)
    }
  }

  // ==================== 销毁实例 ====================
  const handleDestroy = async (instance: VGPUInstance) => {
    const ok = await confirmModal({
      title: '销毁 vGPU 实例',
      content: `确定要销毁 vGPU 实例 ${instance.uuid} 吗？${
        instance.bound_vm ? '该实例已绑定到虚拟机「' + instance.bound_vm + '」，销毁前请先解绑。' : '此操作不可恢复。'
      }`,
      okText: '确定销毁',
      danger: true,
    })
    if (!ok) return
    setDestroyingId(instance.uuid)
    try {
      const res = await destroyVGPUInstance(instance.uuid)
      Toast.success(res.message || 'vGPU 实例已销毁')
      await fetchAll()
    } catch {
      // 请求层已统一提示
    } finally {
      setDestroyingId('')
    }
  }

  // ==================== 表格列定义 ====================
  /** 已用实例数（按 profile_name 聚合实例列表） */
  const usedCount = (profileName: string) =>
    instances.filter((i) => i.profile_id === profileName || i.profile_name === profileName).length

  const profileColumns: ColumnProps<VGPUProfileInfo>[] = [
    {
      title: 'PCI 设备',
      dataIndex: 'pci_device',
      width: 160,
      render: (text: string) => <span className="stg-mono-text">{text}</span>,
    },
    {
      title: '型号名',
      dataIndex: 'profile_name',
      width: 180,
    },
    {
      title: '描述',
      dataIndex: 'description',
      render: (text: string) => text || <span className="stg-vgpu-muted">—</span>,
    },
    {
      title: '显存',
      dataIndex: 'memory_mb',
      width: 100,
      align: 'center' as const,
      render: (mb?: number) => (mb ? formatMB(mb) : '—'),
    },
    {
      title: '最大实例',
      dataIndex: 'max_instances',
      width: 90,
      align: 'center' as const,
    },
    {
      title: '已用实例',
      dataIndex: 'profile_name',
      width: 90,
      align: 'center' as const,
      render: (_: unknown, row: VGPUProfileInfo) => {
        const used = row.used_instances ?? usedCount(row.profile_name)
        return (
          <Tag size="small" color={used >= row.max_instances ? 'red' : 'cyan'}>
            {used} / {row.max_instances}
          </Tag>
        )
      },
    },
  ]

  const instanceColumns: ColumnProps<VGPUInstance>[] = [
    {
      title: 'UUID',
      dataIndex: 'uuid',
      width: 320,
      render: (text: string) => <span className="stg-mono-text">{text}</span>,
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
      width: 160,
      render: (text?: string) => (text ? <span className="stg-mono-text">{text}</span> : '—'),
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      align: 'center' as const,
      render: (status: string) => vgpuStatusTag(status),
    },
    {
      title: '绑定虚拟机',
      dataIndex: 'bound_vm',
      width: 160,
      render: (text?: string) =>
        text ? <Tag size="small" color="blue">{text}</Tag> : <span className="stg-vgpu-muted">未绑定</span>,
    },
    {
      title: '操作',
      dataIndex: 'uuid',
      width: 80,
      align: 'center' as const,
      render: (_: string, row: VGPUInstance) => (
        <Tooltip content="销毁实例" position="top">
          <span
            className={`stg-act-ic destroy ${destroyingId === row.uuid ? 'disabled' : ''}`}
            onClick={() => destroyingId !== row.uuid && void handleDestroy(row)}
          >
            {destroyingId === row.uuid ? <IconRefresh spin /> : <IconDelete />}
          </span>
        </Tooltip>
      ),
    },
  ]

  return (
    <div className="stg-tab-pane stg-tab-pane-wide">
      <Banner
        type="info"
        closeIcon={null}
        className="stg-banner"
        description="vGPU（虚拟 GPU）可将物理 GPU 切分为多个虚拟实例分配给虚拟机。先「发现设备」扫描宿主机上的 vGPU 配置，再按需创建实例，最后在虚拟机表单中选择未绑定的实例挂载。"
      />

      {/* ==================== vGPU 配置列表 ==================== */}
      <SectionHead icon={<IconVideo />} title="vGPU 配置" />

      <div className="stg-vgpu-toolbar">
        <Button
          type="primary"
          theme="light"
          icon={discovering ? <IconRefresh spin /> : <IconSearch />}
          loading={discovering}
          onClick={() => void handleDiscover()}
        >
          发现设备
        </Button>
        <Button
          icon={<IconRefresh />}
          loading={loading}
          onClick={() => void fetchAll()}
        >
          刷新
        </Button>
      </div>

      <Spin spinning={loading}>
        {profiles.length === 0 && !loading ? (
          <Empty description="暂无 vGPU 配置，请点击「发现设备」扫描" style={{ padding: '24px 0' }} />
        ) : (
          <Table<VGPUProfileInfo>
            rowKey={(row) => (row ? `${row.pci_device}-${row.profile_name}` : '')}
            columns={profileColumns}
            dataSource={profiles}
            size="small"
            bordered
            pagination={false}
          />
        )}
      </Spin>

      {/* ==================== vGPU 实例列表 ==================== */}
      <SectionHead icon={<IconVideo />} title="vGPU 实例" />

      <div className="stg-vgpu-toolbar">
        <Button
          type="primary"
          theme="light"
          icon={<IconPlus />}
          disabled={profiles.length === 0}
          onClick={openCreate}
        >
          创建实例
        </Button>
      </div>

      <Spin spinning={loading}>
        {instances.length === 0 && !loading ? (
          <Empty description="暂无 vGPU 实例，请先创建" style={{ padding: '24px 0' }} />
        ) : (
          <Table<VGPUInstance>
            rowKey="uuid"
            columns={instanceColumns}
            dataSource={instances}
            size="small"
            bordered
            pagination={false}
          />
        )}
      </Spin>

      {/* ==================== 创建实例弹窗 ==================== */}
      <Modal
        title="创建 vGPU 实例"
        visible={createVisible}
        onCancel={() => setCreateVisible(false)}
        onOk={() => void handleCreate()}
        okText="创建"
        cancelText="取消"
        confirmLoading={creating}
        closeOnEsc
      >
        <div className="stg-vgpu-create-body">
          <div className="stg-vgpu-create-label">选择 vGPU 配置</div>
          <Select
            style={{ width: '100%' }}
            value={selectedProfile || undefined}
            placeholder="请选择 vGPU 配置"
            filter
            onChange={(v) => setSelectedProfile((v as string) || '')}
          >
            {profiles.map((p) => {
              const used = p.used_instances ?? usedCount(p.profile_name)
              const full = used >= p.max_instances
              return (
                <Select.Option key={p.profile_name} value={p.profile_name} disabled={full}>
                  <div className="stg-vgpu-create-option">
                    <span className="stg-vgpu-create-name">{p.profile_name}</span>
                    <span className="stg-vgpu-create-meta">
                      {p.description ? `${p.description} · ` : ''}
                      {p.memory_mb ? `${formatMB(p.memory_mb)} · ` : ''}
                      {used}/{p.max_instances}
                      {full ? '（已满）' : ''}
                    </span>
                  </div>
                </Select.Option>
              )
            })}
          </Select>
          <div className="stg-vgpu-create-tip">
            实例创建后处于「可用」状态，可在虚拟机创建 / 编辑表单的 vGPU 分区中挂载到虚拟机。
          </div>
        </div>
      </Modal>
    </div>
  )
}
