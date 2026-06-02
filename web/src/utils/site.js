import { ref } from 'vue'
import { getPublicSettings } from '@/api/settings'

export const DEFAULT_SITE_TITLE = 'QVMConsole'
const SITE_TITLE_STORAGE_KEY = 'site_title'

function normalizeSiteTitle(value) {
  const normalized = String(value || '').trim()
  return normalized || DEFAULT_SITE_TITLE
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

export const siteTitle = ref(readCachedSiteTitle())

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
  const currentSiteTitle = getSiteTitle()
  return normalizedPageTitle ? `${normalizedPageTitle} - ${currentSiteTitle}` : currentSiteTitle
}

export function applyDocumentTitle(pageTitle = '') {
  if (typeof document !== 'undefined') {
    document.title = buildDocumentTitle(pageTitle)
  }
}

export async function syncPublicSiteTitle() {
  try {
    const res = await getPublicSettings()
    return setSiteTitle(res.data?.site_title)
  } catch {
    return getSiteTitle()
  }
}
