/**
 * 创建桥接网桥对话框（仅管理员）
 * - 桥接承载默认路由的物理网卡可能短暂中断宿主机网络（顶部警告）
 * - 迁移宿主机 IP：网卡承载管理 IP/默认路由时需开启
 */
import { useState } from 'react'
import { Banner, Input, Modal, Select, Switch, Toast } from '@douyinfe/semi-ui'
import { createNetworkBridge, type HostInterface } from '@/api/network'
import { useMountModalLifecycle } from '@/hooks/useMountModalLifecycle'

interface BridgeDialogProps {
  hostInterfaces: HostInterface[]
  onClose: () => void
  onSaved: () => void
}

export default function BridgeDialog({ hostInterfaces, onClose, onSaved }: BridgeDialogProps) {
  const { modalVisible, requestClose, afterModalClose } = useMountModalLifecycle(onClose)
  const [submitting, setSubmitting] = useState(false)
  const [form, setForm] = useState(() => ({
    name: '',
    mode: 'bridge',
    uplink_if:
      hostInterfaces.find((i) => !i.ovs_port && !i.managed_bridge)?.name ||
      hostInterfaces[0]?.name ||
      '',
    migrate_host_ip: true,
    // 预设 IP 地址段（DHCP）配置
    enable_dhcp: false,
    dhcp_cidr: '',
    dhcp_start: '',
    dhcp_end: '',
    dhcp_gateway: '',
    dhcp_dns: '',
  }))

  const handleSubmit = async () => {
    if (!form.name.trim()) {
      Toast.warning('请输入网桥名称')
      return
    }
    if (!form.uplink_if) {
      Toast.warning('请选择物理网卡')
      return
    }
    // 启用 DHCP 时校验网段 CIDR 必填
    if (form.enable_dhcp && !form.dhcp_cidr.trim()) {
      Toast.warning('请填写网段 CIDR')
      return
    }
    setSubmitting(true)
    try {
      // 构建请求体，仅在启用 DHCP 时携带预设 IP 字段
      const payload: Parameters<typeof createNetworkBridge>[0] = {
        name: form.name.trim(),
        mode: form.mode,
        uplink_if: form.uplink_if,
        migrate_host_ip: form.migrate_host_ip,
      }
      if (form.enable_dhcp) {
        payload.dhcp_cidr = form.dhcp_cidr.trim()
        payload.dhcp_start = form.dhcp_start.trim()
        payload.dhcp_end = form.dhcp_end.trim()
        payload.dhcp_gateway = form.dhcp_gateway.trim()
        payload.dhcp_dns = form.dhcp_dns.trim()
      }
      await createNetworkBridge(payload)
      Toast.success('网桥已创建')
      onSaved()
      requestClose()
    } catch {
      // 请求层已提示
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      title="创建桥接网桥"
      visible={modalVisible}
      afterClose={afterModalClose}
      onCancel={requestClose}
      onOk={() => void handleSubmit()}
      okText="保存"
      cancelText="取消"
      confirmLoading={submitting}
      width={560}
      closeOnEsc
    >
      <Banner
        type="warning"
        closeIcon={null}
        description="桥接承载默认路由的物理网卡可能短暂中断宿主机网络，请确认已具备回滚方式。"
        style={{ marginBottom: 16 }}
      />
      <div className="qvm-form-item">
        <div className="qvm-form-label required">网桥名称</div>
        <Input
          value={form.name}
          onChange={(v) => setForm((f) => ({ ...f, name: v }))}
          maxLength={15}
          placeholder="例如 brpub0"
        />
      </div>
      <div className="qvm-form-item">
        <div className="qvm-form-label">类型</div>
        <Select
          style={{ width: '100%' }}
          value={form.mode}
          onChange={(v) => setForm((f) => ({ ...f, mode: String(v) }))}
          optionList={[{ value: 'bridge', label: '桥接直通' }]}
        />
      </div>
      <div className="qvm-form-item">
        <div className="qvm-form-label required">物理网卡</div>
        <Select
          style={{ width: '100%' }}
          filter
          placeholder="选择物理网卡"
          value={form.uplink_if}
          onChange={(v) => setForm((f) => ({ ...f, uplink_if: String(v) }))}
          optionList={hostInterfaces.map((i) => ({
            value: i.name,
            label: `${i.name}${i.default_route ? '（默认路由）' : ''}${
              i.addresses?.length ? ` - ${i.addresses.join(', ')}` : ''
            }`,
          }))}
        />
      </div>
      <div className="qvm-form-item">
        <div className="net-switch-row">
          <div>
            <div className="qvm-form-label">迁移宿主机 IP</div>
            <div className="qvm-form-tip">
              网卡当前承载管理 IP 或默认路由时通常需要开启，否则宿主机 IP 仍留在物理网卡上。
            </div>
          </div>
          <div className="net-switch-control">
            <Switch
              checked={form.migrate_host_ip}
              onChange={(v) => setForm((f) => ({ ...f, migrate_host_ip: v }))}
              size="small"
              checkedText="开"
              uncheckedText="关"
            />
          </div>
        </div>
      </div>
      {/* 预设 IP 地址段（DHCP）配置 */}
      <div className="qvm-form-item">
        <div className="net-switch-row">
          <div>
            <div className="qvm-form-label">预设 IP 地址段（DHCP）</div>
            <div className="qvm-form-tip">
              开启后由本机 dnsmasq 为接入虚拟机自动分配预设 IP，并写入 dhcp-hosts 静态绑定。
            </div>
          </div>
          <div className="net-switch-control">
            <Switch
              checked={form.enable_dhcp}
              onChange={(v) => setForm((f) => ({ ...f, enable_dhcp: v }))}
              size="small"
              checkedText="开"
              uncheckedText="关"
            />
          </div>
        </div>
      </div>
      {form.enable_dhcp && (
        <>
          <div className="qvm-form-item">
            <div className="qvm-form-label required">网段 CIDR</div>
            <Input
              value={form.dhcp_cidr}
              onChange={(v) => setForm((f) => ({ ...f, dhcp_cidr: v }))}
              placeholder="例如 10.100.4.0/22"
            />
            <div className="qvm-form-tip">虚拟机 IP 将从此网段分配</div>
          </div>
          <div style={{ display: 'flex', gap: 12 }}>
            <div className="qvm-form-item" style={{ flex: 1 }}>
              <div className="qvm-form-label">网关</div>
              <Input
                value={form.dhcp_gateway}
                onChange={(v) => setForm((f) => ({ ...f, dhcp_gateway: v }))}
                placeholder="留空自动推导"
              />
            </div>
            <div className="qvm-form-item" style={{ flex: 1 }}>
              <div className="qvm-form-label">DNS</div>
              <Input
                value={form.dhcp_dns}
                onChange={(v) => setForm((f) => ({ ...f, dhcp_dns: v }))}
                placeholder="例如 223.5.5.5"
              />
            </div>
          </div>
          <div style={{ display: 'flex', gap: 12 }}>
            <div className="qvm-form-item" style={{ flex: 1 }}>
              <div className="qvm-form-label">DHCP 起始 IP</div>
              <Input
                value={form.dhcp_start}
                onChange={(v) => setForm((f) => ({ ...f, dhcp_start: v }))}
                placeholder="留空自动推导"
              />
            </div>
            <div className="qvm-form-item" style={{ flex: 1 }}>
              <div className="qvm-form-label">DHCP 结束 IP</div>
              <Input
                value={form.dhcp_end}
                onChange={(v) => setForm((f) => ({ ...f, dhcp_end: v }))}
                placeholder="留空自动推导"
              />
            </div>
          </div>
        </>
      )}
    </Modal>
  )
}
