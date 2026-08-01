/**
 * 额外数据盘分区（创建向导 · ISO / 模板模式共用）
 */
import { useEffect, useState } from 'react'
import { Button, Input, InputNumber, Select, Switch, Tooltip } from '@douyinfe/semi-ui'
import { IconDelete, IconPlus } from '@douyinfe/semi-icons'
import { DiskIcon } from '../icons'
import SectionCard from './SectionCard'
import FormField from './FormField'
import { useVmFormScope } from '../scopeContext'
import { DISK_BUS_OPTIONS } from '../constants'
import { storageTargetLabel } from './storageTargetUtils'
import DiskIopsDialog from '../dialogs/DiskIopsDialog'

interface ExtraDiskSectionProps {
  /** 分区标题 */
  title?: string
  /** 底部提示 */
  tip?: string
}

export default function ExtraDiskSection({ title = '额外数据盘', tip }: ExtraDiskSectionProps) {
  const { form, options, ctx } = useVmFormScope()
  const { form: f, setField } = form
  const [iopsDialogIndex, setIopsDialogIndex] = useState(-1)
  // 旧版规则：ISO 模式额外磁盘 raw 格式对所有角色开放；模板模式仅管理员
  const rawAllowed = ctx.isAdmin || f.create_mode === 'iso'
	const guestType = f.template_type || f.os_type
  const canAutoMount =
    f.create_mode === 'template' && (guestType === 'linux' || guestType === 'windows')

  const addExtraDisk = () => {
    const defaultTarget = options.storageTargets.find((t) => t.is_default)
    setField('extra_disks', [
      ...f.extra_disks,
      {
        size: 20,
        format: 'qcow2',
        bus: f.disk_bus || 'virtio',
        storage_pool_id: f.storage_pool_id || defaultTarget?.id || '',
        iops_total: 0,
        iops_read: 0,
        iops_write: 0,
        cloud_disk_spec_id: '',
        guest_mount: { enabled: false, filesystem: 'ext4', mount_point: '/data' },
      },
    ])
  }

  /** 数据盘规格列表（仅 DATA 类型） */
  const dataDiskSpecs = options.cloudDiskSpecs.filter((s) => s.disk_type === 'DATA')

  /** 选择数据盘规格后联动填充磁盘配置并禁用手动编辑（一次性更新所有字段，避免快照覆盖） */
  const handleDataDiskSpecChange = (index: number, v: string | string[] | undefined) => {
    const raw = v === undefined || v === '' || Array.isArray(v) ? '' : Number(v)
    if (raw === '') {
      // 清空规格，仅更新 cloud_disk_spec_id，保留用户已填写的其他值
      updateExtraDisk(index, 'cloud_disk_spec_id', '')
      return
    }
    const spec = dataDiskSpecs.find((s) => s.id === (raw as number))
    if (!spec) return
    // 一次性更新整个磁盘项，避免连续 setField 导致快照覆盖
    setField(
      'extra_disks',
      f.extra_disks.map((disk, i) => {
        if (i !== index) return disk
        // 选中规格后填充磁盘配置
        const iopsTotal = spec.iops_mode === 'TOTAL' ? (spec.total_iops || 0) : 0
        const iopsRead = spec.iops_mode === 'READ_WRITE' ? (spec.read_iops || 0) : 0
        const iopsWrite = spec.iops_mode === 'READ_WRITE' ? (spec.write_iops || 0) : 0
        return {
          ...disk,
          cloud_disk_spec_id: raw as number | '',
          size: spec.capacity_gb,
          format: spec.disk_format ? spec.disk_format.toLowerCase() : disk.format,
          iops_total: iopsTotal,
          iops_read: iopsRead,
          iops_write: iopsWrite,
        }
      }),
    )
  }

  const updateExtraDisk = (index: number, key: string, value: unknown) => {
    setField(
      'extra_disks',
      f.extra_disks.map((disk, i) => (i === index ? { ...disk, [key]: value } : disk)),
    )
  }

  const removeExtraDisk = (index: number) => {
    setField('extra_disks', f.extra_disks.filter((_, i) => i !== index))
  }

  const iopsDialogDisk = iopsDialogIndex >= 0 ? f.extra_disks[iopsDialogIndex] : null
  const [lastIopsDialog, setLastIopsDialog] = useState<{
    index: number
    disk: NonNullable<typeof iopsDialogDisk>
  } | null>(null)
  useEffect(() => {
    if (iopsDialogDisk) setLastIopsDialog({ index: iopsDialogIndex, disk: iopsDialogDisk })
  }, [iopsDialogDisk, iopsDialogIndex])
  const activeIopsDialog = iopsDialogDisk
    ? { index: iopsDialogIndex, disk: iopsDialogDisk }
    : lastIopsDialog

  return (
    <SectionCard icon={<DiskIcon />} title={title}>
      <FormField label="额外磁盘" tip={tip}>
        {f.extra_disks.map((disk, index) => {
          // 当前磁盘是否已选择数据盘规格（选中后容量/格式/IOPS 字段置灰）
          const hasDataDiskSpec = disk.cloud_disk_spec_id !== undefined && disk.cloud_disk_spec_id !== ''
          return (
          <div key={index} className="qvm-vf-disk-row">
            {/* 数据盘规格选择（仅 DATA 类型，留空可手动填写） */}
            {dataDiskSpecs.length > 0 && (
              <Select
                style={{ width: 150 }}
                value={disk.cloud_disk_spec_id ? String(disk.cloud_disk_spec_id) : undefined}
                placeholder="规格选择"
                showClear
                filter
                onChange={(v) => handleDataDiskSpecChange(index, v)}
                optionList={dataDiskSpecs.map((spec) => ({
                  value: String(spec.id),
                  label: `${spec.name}（${spec.capacity_gb}G）`,
                }))}
              />
            )}
            <InputNumber
              style={{ width: 110 }}
              value={disk.size}
              min={1}
              max={2000}
              placeholder="大小(GB)"
              disabled={hasDataDiskSpec}
              onChange={(v) => updateExtraDisk(index, 'size', Number(v || 0))}
            />
            <Select
              style={{ width: 96 }}
              value={disk.format}
              disabled={hasDataDiskSpec}
              onChange={(v) => updateExtraDisk(index, 'format', v)}
              optionList={[
                { value: 'qcow2', label: 'qcow2' },
                ...(rawAllowed ? [{ value: 'raw', label: 'raw' }] : []),
              ]}
            />
            {canAutoMount && (
              <Tooltip content="克隆完成后自动挂载到系统" position="top">
                <Switch
                  checked={!!disk.guest_mount?.enabled}
                  checkedText="开"
                  uncheckedText="关"
                  onChange={(enabled) => {
                    updateExtraDisk(index, 'guest_mount', {
                      enabled,
                      filesystem: disk.guest_mount?.filesystem || 'ext4',
                      mount_point: disk.guest_mount?.mount_point || '/data',
                      drive_letter: disk.guest_mount?.drive_letter || '',
                    })
                    if (enabled) setField('guest_agent', { enabled: true })
                  }}
                />
              </Tooltip>
            )}
            {!!disk.guest_mount?.enabled && guestType === 'linux' && (
              <>
                <Select
                  style={{ width: 92 }}
                  value={disk.guest_mount.filesystem || 'ext4'}
                  onChange={(value) => updateExtraDisk(index, 'guest_mount', { ...disk.guest_mount, filesystem: value })}
                  optionList={[
                    { value: 'ext4', label: 'ext4' },
                    { value: 'xfs', label: 'XFS' },
                    { value: 'btrfs', label: 'Btrfs' },
                  ]}
                />
                <Input
                  style={{ width: 125 }}
                  value={disk.guest_mount.mount_point || '/data'}
                  onChange={(value) => updateExtraDisk(index, 'guest_mount', { ...disk.guest_mount, mount_point: value })}
                  placeholder="/data"
                />
              </>
            )}
            {!!disk.guest_mount?.enabled && guestType === 'windows' && (
              <Input
                style={{ width: 90 }}
                value={disk.guest_mount.drive_letter || ''}
                onChange={(value) => updateExtraDisk(index, 'guest_mount', { ...disk.guest_mount, drive_letter: value })}
                maxLength={1}
                placeholder="盘符"
              />
            )}
            <Select
              style={{ width: 104 }}
              value={disk.bus}
              onChange={(v) => updateExtraDisk(index, 'bus', v)}
              optionList={DISK_BUS_OPTIONS.map((item) => ({ value: item.value, label: item.label }))}
            />
            <Select
              style={{ width: 170 }}
              value={disk.storage_pool_id || undefined}
              placeholder="默认存储"
              showClear
              filter
              onChange={(v) => updateExtraDisk(index, 'storage_pool_id', (v as string) || '')}
              optionList={options.storageTargets.map((t) => ({ value: t.id, label: storageTargetLabel(t) }))}
            />
            <span className="qvm-vf-disk-unit">GB</span>
            {ctx.isAdmin && (
              <Button size="small" onClick={() => setIopsDialogIndex(index)} style={{ fontSize: 11 }}>
                IOPS
              </Button>
            )}
            <Button size="small" type="danger" theme="borderless" icon={<IconDelete />} onClick={() => removeExtraDisk(index)} />
            {ctx.isAdmin && (disk.iops_total > 0 || disk.iops_read > 0 || disk.iops_write > 0) && (
              <span className="qvm-vf-iops-badge">
                IOPS: 总{disk.iops_total || 0} / 读{disk.iops_read || 0} / 写{disk.iops_write || 0}
              </span>
            )}
          </div>
          )
        })}
        <Button type="primary" theme="light" size="small" icon={<IconPlus />} onClick={addExtraDisk}>
          添加额外磁盘
        </Button>
      </FormField>

      {activeIopsDialog && (
        <DiskIopsDialog
          visible={iopsDialogIndex >= 0}
          subtitle={`额外磁盘 #${activeIopsDialog.index + 1}（${activeIopsDialog.disk.size}GB ${activeIopsDialog.disk.format} ${activeIopsDialog.disk.bus}）`}
          initial={{
            total: activeIopsDialog.disk.iops_total,
            read: activeIopsDialog.disk.iops_read,
            write: activeIopsDialog.disk.iops_write,
          }}
          onApply={(values) => {
            updateExtraDisk(activeIopsDialog.index, 'iops_total', values.total)
            updateExtraDisk(activeIopsDialog.index, 'iops_read', values.read)
            updateExtraDisk(activeIopsDialog.index, 'iops_write', values.write)
          }}
          onClose={() => setIopsDialogIndex(-1)}
        />
      )}
    </SectionCard>
  )
}
