/**
 * 站点信息管理：站点标题、公开设置同步、UI 自定义（favicon / 浏览器标题 / 图标）
 * 对应旧版 utils/site.js
 */
import { useAppStore, buildDocumentTitle } from '@/stores/app'
import { getPublicSettings } from '@/api/settings'

/** 更新浏览器标签页标题（优先使用 UI 自定义的 browser_title，回退站点标题） */
export function applyDocumentTitle(pageTitle = '') {
  const { siteTitle, uiCustomization } = useAppStore.getState()
  const effectiveTitle = uiCustomization.browserTitle?.trim() || siteTitle
  document.title = buildDocumentTitle(pageTitle, effectiveTitle)
}

/**
 * 动态应用浏览器 Favicon
 * - 传入 base64 字符串时设置为图标
 * - 传入空字符串时回退到 /favicon.svg
 */
export function applyFavicon(faviconBase64: string) {
  const href = faviconBase64?.trim() || '/favicon.svg'
  // 查找或创建 <link rel="icon"> 元素
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = href
}

/** 从后端同步公开设置（站点标题、泄露密码检测开关、SPICE 默认值、UI 自定义） */
export async function syncPublicSiteInfo(): Promise<string> {
  try {
    const res = await getPublicSettings()
    const data = res.data || {}
    const appStore = useAppStore.getState()
    if (data.site_title !== undefined) {
      appStore.setSiteTitle(data.site_title || '')
    }
    appStore.setPublicFlags({
      password_breach_check_enabled: data.password_breach_check_enabled,
      spice_enabled_by_default: data.spice_enabled_by_default,
    })
    // 同步 UI 自定义配置（图标 base64 + 标题文本）
    appStore.setUiCustomization({
      systemHomeIcon: String(data.system_home_icon || ''),
      homeTitle: String(data.home_title || ''),
      homeSubtitle: String(data.home_subtitle || ''),
      loginPageIcon: String(data.login_page_icon || ''),
      productName: String(data.product_name || ''),
      browserFavicon: String(data.browser_favicon || ''),
      browserTitle: String(data.browser_title || ''),
      footerText: String(data.footer_text || ''),
      footerLink: String(data.footer_link || ''),
    })
    // 立即应用 favicon 与浏览器标题
    applyFavicon(String(data.browser_favicon || ''))
    applyDocumentTitle()
    return useAppStore.getState().siteTitle
  } catch {
    return useAppStore.getState().siteTitle
  }
}
