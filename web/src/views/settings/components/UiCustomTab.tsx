/**
 * UI 自定义 Tab：首页图标 / 首页标题 / 登录页图标 / 产品名称 / Favicon / 浏览器标题
 * - 图标字段以 base64 字符串存储，使用 FileReader.readAsDataURL 转换
 * - 支持图片预览、替换、清除
 * - 保存后通过公开设置同步到全局（favicon、浏览器标题、侧边栏/登录页图标）
 */
import { useRef, useState } from 'react'
import { Button, Input, Toast } from '@douyinfe/semi-ui'
import { IconImage, IconDelete, IconUpload } from '@douyinfe/semi-icons'
import { SectionHead, SettingRow } from './SettingRow'
import type { SettingsTabProps } from '../types'

/** 图片上传最大体积（512KB），避免 base64 过大撑爆数据库行 */
const MAX_ICON_SIZE = 512 * 1024

/** 支持的图片 MIME 类型 */
const ACCEPTED_TYPES = ['image/png', 'image/jpeg', 'image/svg+xml', 'image/x-icon', 'image/webp']

interface IconUploaderProps {
  /** 当前 base64 值（空字符串表示未设置） */
  value: string
  /** 值变更回调，传入空字符串表示清除 */
  onChange: (base64: string) => void
  /** 上传按钮文案 */
  label?: string
}

/**
 * 图标上传器：预览 + 上传 + 清除
 * 将选中的图片文件转为 base64 字符串后回传父组件
 */
function IconUploader({ value, onChange, label = '上传图标' }: IconUploaderProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [loading, setLoading] = useState(false)

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    // 校验文件类型
    if (!ACCEPTED_TYPES.includes(file.type)) {
      Toast.error('仅支持 PNG / JPEG / SVG / ICO / WebP 格式')
      e.target.value = ''
      return
    }
    // 校验文件大小
    if (file.size > MAX_ICON_SIZE) {
      Toast.error(`图标大小不能超过 ${MAX_ICON_SIZE / 1024}KB`)
      e.target.value = ''
      return
    }
    setLoading(true)
    const reader = new FileReader()
    reader.onload = () => {
      onChange(String(reader.result || ''))
      setLoading(false)
    }
    reader.onerror = () => {
      Toast.error('图片读取失败，请重试')
      setLoading(false)
    }
    reader.readAsDataURL(file)
    // 重置 input value 以便重复选择同一文件
    e.target.value = ''
  }

  return (
    <div className="stg-icon-uploader">
      {/* 预览区 */}
      <div className="stg-icon-preview">
        {value ? (
          <img src={value} alt="图标预览" className="stg-icon-img" />
        ) : (
          <div className="stg-icon-placeholder">
            <IconImage size="extra-large" />
          </div>
        )}
      </div>
      {/* 操作按钮 */}
      <div className="stg-icon-actions">
        <input
          ref={inputRef}
          type="file"
          accept={ACCEPTED_TYPES.join(',')}
          style={{ display: 'none' }}
          onChange={handleFileChange}
        />
        <Button
          size="small"
          theme="light"
          icon={<IconUpload />}
          loading={loading}
          onClick={() => inputRef.current?.click()}
        >
          {value ? '替换' : label}
        </Button>
        {value && (
          <Button
            size="small"
            type="danger"
            theme="borderless"
            icon={<IconDelete />}
            onClick={() => onChange('')}
          >
            清除
          </Button>
        )}
      </div>
    </div>
  )
}

export default function UiCustomTab({ form, patch }: SettingsTabProps) {
  return (
    <div className="stg-tab-pane">
      <SectionHead icon={<IconImage />} title="侧边栏与首页" />

      <SettingRow
        label="系统首页图标"
        tip="显示在侧边栏左上角的品牌图标，建议 64×64 PNG/SVG | 环境变量: KVM_SYSTEM_HOME_ICON"
      >
        <IconUploader
          value={form.system_home_icon}
          onChange={(v) => patch({ system_home_icon: v })}
        />
      </SettingRow>

      <SettingRow
        label="首页标题"
        tip="侧边栏产品名称（留空则使用网站标题） | 环境变量: KVM_HOME_TITLE"
      >
        <Input
          value={form.home_title}
          onChange={(v) => patch({ home_title: v })}
          placeholder="留空则使用网站标题"
          maxLength={60}
        />
      </SettingRow>

      <SectionHead icon={<IconImage />} title="登录页" />

      <SettingRow
        label="登录页图标"
        tip="登录页品牌区展示的图标，建议 128×128 PNG/SVG | 环境变量: KVM_LOGIN_PAGE_ICON"
      >
        <IconUploader
          value={form.login_page_icon}
          onChange={(v) => patch({ login_page_icon: v })}
        />
      </SettingRow>

      <SettingRow
        label="产品名称"
        tip="登录页展示的产品名称（留空则使用网站标题） | 环境变量: KVM_PRODUCT_NAME"
      >
        <Input
          value={form.product_name}
          onChange={(v) => patch({ product_name: v })}
          placeholder="留空则使用网站标题"
          maxLength={60}
        />
      </SettingRow>

      <SectionHead icon={<IconImage />} title="浏览器" />

      <SettingRow
        label="浏览器 Favicon"
        tip="浏览器标签页图标，建议 32×32 ICO/PNG/SVG | 环境变量: KVM_BROWSER_FAVICON"
      >
        <IconUploader
          value={form.browser_favicon}
          onChange={(v) => patch({ browser_favicon: v })}
        />
      </SettingRow>

      <SettingRow
        label="浏览器标题"
        tip="浏览器标签页显示的标题（留空则使用网站标题） | 环境变量: KVM_BROWSER_TITLE"
      >
        <Input
          value={form.browser_title}
          onChange={(v) => patch({ browser_title: v })}
          placeholder="留空则使用网站标题"
          maxLength={60}
        />
      </SettingRow>

      <SectionHead icon={<IconImage />} title="页脚" />

      <SettingRow
        label="页脚版权信息"
        tip="关于页与登录页底部显示的版权信息（留空则使用默认格式：© 年份 网站标题. 保留所有权利） | 环境变量: KVM_FOOTER_TEXT"
      >
        <Input
          value={form.footer_text}
          onChange={(v) => patch({ footer_text: v })}
          placeholder="如：© 2026 MyCompany. All rights reserved."
          maxLength={200}
        />
      </SettingRow>

      <SettingRow
        label="页脚超链接"
        tip="点击页脚版权信息跳转的链接（留空则纯文本展示，不可点击） | 环境变量: KVM_FOOTER_LINK"
      >
        <Input
          value={form.footer_link}
          onChange={(v) => patch({ footer_link: v })}
          placeholder="如：https://example.com"
          maxLength={500}
        />
      </SettingRow>
    </div>
  )
}
