/**
 * 关于项目页
 * 面板信息（/public/version）、系统运行环境信息（/system-info）。
 */
import { useEffect, useState } from 'react'
import { Card, Collapse, Spin, Tag } from '@douyinfe/semi-ui'
import { IconMonitorStroked, IconSetting } from '@douyinfe/semi-icons'
import {
  getPublicSystemInfo,
  getPublicVersion,
  type PublicSystemInfo,
  type PublicVersion,
} from '@/api/settings'
import { useAppStore } from '@/stores/app'
import './about.css'

/** 系统信息展示字段映射 */
const sysInfoFields: { label: string; keys: string[] }[] = [
  { label: '操作系统', keys: ['distro', 'os'] },
  { label: '内核版本', keys: ['kernel'] },
  { label: '系统架构', keys: ['arch'] },
  { label: '主机名', keys: ['hostname'] },
  { label: 'CPU 核数', keys: ['num_cpu'] },
  { label: 'QEMU 版本', keys: ['qemu'] },
  { label: 'libvirt 版本', keys: ['libvirt'] },
  { label: '系统运行时间', keys: ['uptime'] },
]

export default function AboutPage() {
  const isDev = import.meta.env.DEV
  const currentYear = new Date().getFullYear()
  const siteTitle = useAppStore((s) => s.siteTitle)
  const footerText = useAppStore((s) => s.uiCustomization.footerText)
  const footerLink = useAppStore((s) => s.uiCustomization.footerLink)
  const [versionInfo, setVersionInfo] = useState<PublicVersion>({})
  const [sysInfo, setSysInfo] = useState<PublicSystemInfo>({})
  const [sysLoading, setSysLoading] = useState(false)

  useEffect(() => {
    let mounted = true
    const load = async () => {
      setSysLoading(true)
      try {
        const [verRes, sysRes] = await Promise.allSettled([getPublicVersion(), getPublicSystemInfo()])
        if (!mounted) return
        if (verRes.status === 'fulfilled') setVersionInfo(verRes.value.data || {})
        else setVersionInfo({ version: 'dev' })
        if (sysRes.status === 'fulfilled') setSysInfo(sysRes.value.data || {})
      } finally {
        if (mounted) setSysLoading(false)
      }
    }
    void load()
    return () => {
      mounted = false
    }
  }, [])

  const sysValue = (keys: string[]): string => {
    for (const k of keys) {
      const v = sysInfo[k]
      if (v !== undefined && v !== null && v !== '') return String(v)
    }
    return '-'
  }

  return (
    <div className="about-page">
      <Collapse defaultActiveKey={['panel', 'system']} keepDOM>
        <Collapse.Panel
          itemKey="panel"
          header={
            <span className="about-section-header">
              <IconMonitorStroked className="about-section-icon" />
              面板信息
            </span>
          }
        >
          <div className="about-info-grid">
            <div className="about-info-item">
              <span className="about-info-label">版本</span>
              <span className="about-info-value">{versionInfo.version || '开发版'}</span>
            </div>
            <div className="about-info-item">
              <span className="about-info-label">构建时间</span>
              <span className="about-info-value">{versionInfo.build_time || '未设置'}</span>
            </div>
            <div className="about-info-item">
              <span className="about-info-label">站点名称</span>
              <span className="about-info-value">{versionInfo.site_title || '未设置'}</span>
            </div>
            <div className="about-info-item">
              <span className="about-info-label">运行模式</span>
              <Tag size="small" color={isDev ? 'orange' : 'green'}>
                {isDev ? '开发环境' : '生产环境'}
              </Tag>
            </div>
          </div>
        </Collapse.Panel>

        <Collapse.Panel
          itemKey="system"
          header={
            <span className="about-section-header">
              <IconSetting className="about-section-icon" />
              系统信息
            </span>
          }
        >
          <Spin spinning={sysLoading}>
            <div className="about-info-grid">
              {sysInfoFields.map((f) => (
                <div key={f.label} className="about-info-item">
                  <span className="about-info-label">{f.label}</span>
                  <span className="about-info-value">{sysValue(f.keys)}</span>
                </div>
              ))}
            </div>
          </Spin>
        </Collapse.Panel>
      </Collapse>

      <Card className="about-footer-card">
        <p className="about-footer">
          {footerLink?.trim() ? (
            <a href={footerLink.trim()} target="_blank" rel="noopener noreferrer">
              {footerText?.trim() || `© ${currentYear} ${siteTitle}. 保留所有权利`}
            </a>
          ) : (
            footerText?.trim() || `© ${currentYear} ${siteTitle}. 保留所有权利`
          )}
        </p>
      </Card>
    </div>
  )
}
