/**
 * Otoha portal rules (TASK-61). When the server runs with `otoha.portal` on, visitors and regular users only get
 * buying, My Otoha, usage, orders and account settings; every other user page goes back to My Otoha (or to the
 * buy page when signed out). Admins and sites without the portal are not affected.
 */
import type { CustomMenuItem, PublicSettings } from '@/types'

export const OTOHA_PORTAL_HOME = '/otoha/account'
export const OTOHA_PORTAL_BUY = '/otoha/buy'
export const OTOHA_PORTAL_USAGE = '/otoha/usage'

export interface OtohaPortalViewer {
  portalEnabled: boolean
  isAuthenticated: boolean
  isAdmin: boolean
}

export interface OtohaPortalNavItem {
  path: string
  /** i18n key for the built-in entries */
  labelKey?: string
  /** admin-written label for custom menu items */
  label?: string
  iconSvg?: string
}

export function isOtohaPortalEnabled(settings: Partial<PublicSettings> | null | undefined): boolean {
  return settings?.otoha_portal_enabled === true
}

/** The portal shapes what visitors and regular users see; admins keep the full site. */
export function otohaPortalAppliesTo(viewer: OtohaPortalViewer): boolean {
  return viewer.portalEnabled && !viewer.isAdmin
}

function portalCustomItems(items: CustomMenuItem[] | null | undefined): CustomMenuItem[] {
  return (items ?? [])
    .filter((item) => item.visibility === 'user' && item.show_in_portal === true)
    .sort((a, b) => a.sort_order - b.sort_order)
}

/** The portal's menu: the five built-in entries, then the user menu items the admin marked for the portal. */
export function otohaPortalNav(customMenuItems: CustomMenuItem[] | null | undefined): OtohaPortalNavItem[] {
  return [
    { path: OTOHA_PORTAL_HOME, labelKey: 'otoha.nav.myOtoha' },
    { path: OTOHA_PORTAL_BUY, labelKey: 'otoha.nav.buy' },
    { path: OTOHA_PORTAL_USAGE, labelKey: 'otoha.nav.usage' },
    { path: '/orders', labelKey: 'otoha.nav.orders' },
    { path: '/profile', labelKey: 'otoha.nav.settings' },
    ...portalCustomItems(customMenuItems).map((item) => ({
      path: `/custom/${item.id}`,
      label: item.label,
      iconSvg: item.icon_svg,
    })),
  ]
}

// Pages the portal keeps: its own pages, payment steps, signing in and out, legal documents and first-time setup.
// Admin pages are left to the admin check. A prefix matches the page itself and anything below it.
const KEPT_PAGES = ['/otoha', '/orders', '/profile', '/payment', '/legal', '/auth', '/setup', '/admin']
const SIGN_IN_PAGES = ['/login', '/register']
const SIGNED_OUT_PAGES = ['/email-verify', '/forgot-password', '/reset-password']

// General pages that have a portal version.
const PORTAL_VERSIONS: Record<string, string> = {
  '/usage': OTOHA_PORTAL_USAGE,
  '/purchase': OTOHA_PORTAL_BUY,
}

function underPage(path: string, page: string): boolean {
  return path === page || path.startsWith(`${page}/`)
}

/**
 * Where the portal sends a visit to `path`, or null to let it through (the usual checks still apply).
 * `customMenuItems` are the public custom menu items; only those marked for the portal keep their page.
 */
export function resolveOtohaPortalRedirect(
  path: string,
  viewer: OtohaPortalViewer,
  customMenuItems?: CustomMenuItem[] | null,
): string | null {
  if (!otohaPortalAppliesTo(viewer)) return null
  const home = viewer.isAuthenticated ? OTOHA_PORTAL_HOME : OTOHA_PORTAL_BUY

  if (KEPT_PAGES.some((page) => underPage(path, page))) return null
  if (SIGNED_OUT_PAGES.includes(path)) return null
  if (SIGN_IN_PAGES.includes(path)) return viewer.isAuthenticated ? OTOHA_PORTAL_HOME : null

  const version = PORTAL_VERSIONS[path]
  if (version) return version

  const custom = /^\/custom\/([^/]+)$/.exec(path)
  if (custom && portalCustomItems(customMenuItems).some((item) => item.id === custom[1])) return null

  return home
}

/** The subtitle the server falls back to when the admin has not set one; it describes the gateway. */
export const DEFAULT_SITE_SUBTITLE = 'Subscription to API Conversion Platform'

/**
 * The subtitle on the sign-in pages. Without the portal: the configured one or the usual default. In the portal
 * the gateway's default does not describe the service, so null asks for the portal's own text.
 */
export function siteSubtitleForAuth(configured: string | null | undefined, portal: boolean): string | null {
  const text = (configured ?? '').trim()
  if (!portal) return configured || DEFAULT_SITE_SUBTITLE
  return text && text !== DEFAULT_SITE_SUBTITLE ? text : null
}
