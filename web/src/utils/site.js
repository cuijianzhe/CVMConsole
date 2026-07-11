import { ref } from 'vue'
import { getPublicSettings } from '@/api/settings'

export const DEFAULT_SITE_TITLE = 'CVMConsole'
const SITE_TITLE_STORAGE_KEY = 'site_title'
const HOME_TITLE_STORAGE_KEY = 'home_title'
const SYSTEM_HOME_ICON_STORAGE_KEY = 'system_home_icon'
const LOGIN_PAGE_ICON_STORAGE_KEY = 'login_page_icon'
const PRODUCT_NAME_STORAGE_KEY = 'product_name'
const BROWSER_FAVICON_STORAGE_KEY = 'browser_favicon'
const BROWSER_TITLE_STORAGE_KEY = 'browser_title'

// 泄露密码检测开关（默认开启）
export const passwordBreachCheckEnabled = ref(true)

// 创建虚拟机时 SPICE 显示协议开关的默认初始值（默认关闭）
export const spiceEnabledByDefault = ref(false)

// UI 自定义配置
export const systemHomeIcon = ref('')
export const homeTitle = ref('QVMConsole')
export const loginPageIcon = ref('')
export const productName = ref('')
export const browserFavicon = ref('')
export const browserTitle = ref('QVMConsole')

function normalizeSiteTitle(value) {
  const normalized = String(value || '').trim()
  return normalized || DEFAULT_SITE_TITLE
}

function normalizeHomeTitle(value) {
  const normalized = String(value || '').trim()
  return normalized || 'QVMConsole'
}

function normalizeBrowserTitle(value) {
  const normalized = String(value || '').trim()
  return normalized || 'QVMConsole'
}

function readCachedSiteTitle() {
  if (typeof window === 'undefined') {
    return DEFAULT_SITE_TITLE
  }
  try {
    return normalizeSiteTitle(localStorage.getItem(SITE_TITLE_STORAGE_KEY) || '')
  } catch {
    return DEFAULT_SITE_TITLE
  }
}

function safeSetItem(key, value) {
  if (typeof window === 'undefined') return
  try {
    localStorage.setItem(key, value)
  } catch (e) {
    console.warn('localStorage quota exceeded for key:', key)
  }
}

function readCachedUIConfig() {
  if (typeof window === 'undefined') return
  try {
    systemHomeIcon.value = localStorage.getItem(SYSTEM_HOME_ICON_STORAGE_KEY) || ''
    homeTitle.value = normalizeHomeTitle(localStorage.getItem(HOME_TITLE_STORAGE_KEY))
    loginPageIcon.value = localStorage.getItem(LOGIN_PAGE_ICON_STORAGE_KEY) || ''
    productName.value = localStorage.getItem(PRODUCT_NAME_STORAGE_KEY) || ''
    browserFavicon.value = localStorage.getItem(BROWSER_FAVICON_STORAGE_KEY) || ''
    browserTitle.value = normalizeBrowserTitle(localStorage.getItem(BROWSER_TITLE_STORAGE_KEY))
  } catch {
    // ignore
  }
}

export const siteTitle = ref(readCachedSiteTitle())

readCachedUIConfig()

export function getSiteTitle() {
  return normalizeSiteTitle(siteTitle.value)
}

export function setSiteTitle(value) {
  const normalized = normalizeSiteTitle(value)
  siteTitle.value = normalized
  if (typeof window !== 'undefined') {
    localStorage.setItem(SITE_TITLE_STORAGE_KEY, normalized)
  }
  return normalized
}

export function buildDocumentTitle(pageTitle = '') {
  const normalizedPageTitle = String(pageTitle || '').trim()
  const currentBrowserTitle = browserTitle.value || getSiteTitle()
  return normalizedPageTitle ? `${normalizedPageTitle} - ${currentBrowserTitle}` : currentBrowserTitle
}

export function applyDocumentTitle(pageTitle = '') {
  if (typeof document !== 'undefined') {
    document.title = buildDocumentTitle(pageTitle)
  }
}

export async function syncPublicSiteTitle() {
  try {
    const res = await getPublicSettings()
    setSiteTitle(res.data?.site_title)
    // 同步泄露密码检测开关
    if (res.data?.password_breach_check_enabled !== undefined) {
      passwordBreachCheckEnabled.value = res.data.password_breach_check_enabled !== false
    }
    // 同步 SPICE 开关默认初始值
    if (res.data?.spice_enabled_by_default !== undefined) {
      spiceEnabledByDefault.value = res.data.spice_enabled_by_default === true
    }
    // 同步 UI 自定义配置
    if (res.data?.system_home_icon !== undefined) {
      systemHomeIcon.value = res.data.system_home_icon
      safeSetItem(SYSTEM_HOME_ICON_STORAGE_KEY, res.data.system_home_icon)
    }
    if (res.data?.home_title !== undefined) {
      homeTitle.value = normalizeHomeTitle(res.data.home_title)
      safeSetItem(HOME_TITLE_STORAGE_KEY, homeTitle.value)
    }
    if (res.data?.login_page_icon !== undefined) {
      loginPageIcon.value = res.data.login_page_icon
      safeSetItem(LOGIN_PAGE_ICON_STORAGE_KEY, res.data.login_page_icon)
    }
    if (res.data?.product_name !== undefined) {
      productName.value = res.data.product_name
      safeSetItem(PRODUCT_NAME_STORAGE_KEY, res.data.product_name)
    }
    if (res.data?.browser_favicon !== undefined) {
      browserFavicon.value = res.data.browser_favicon
      safeSetItem(BROWSER_FAVICON_STORAGE_KEY, res.data.browser_favicon)
      // 更新 favicon
      if (typeof document !== 'undefined' && browserFavicon.value) {
        const link = document.querySelector('link[rel="icon"]') || document.createElement('link')
        link.rel = 'icon'
        link.href = browserFavicon.value
        document.head.appendChild(link)
      }
    }
    if (res.data?.browser_title !== undefined) {
      browserTitle.value = normalizeBrowserTitle(res.data.browser_title)
      safeSetItem(BROWSER_TITLE_STORAGE_KEY, browserTitle.value)
    }
    return getSiteTitle()
  } catch {
    return getSiteTitle()
  }
}
