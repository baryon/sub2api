import { describe, expect, it } from 'vitest'
import type { CustomMenuItem } from '@/types'
import {
  DEFAULT_SITE_SUBTITLE,
  OTOHA_PORTAL_BUY,
  OTOHA_PORTAL_HOME,
  OTOHA_PORTAL_USAGE,
  isOtohaPortalEnabled,
  otohaPortalAppliesTo,
  otohaPortalNav,
  resolveOtohaPortalRedirect,
  siteSubtitleForAuth,
  type OtohaPortalViewer,
} from '../otohaPortal'

const user: OtohaPortalViewer = { portalEnabled: true, isAuthenticated: true, isAdmin: false }
const visitor: OtohaPortalViewer = { portalEnabled: true, isAuthenticated: false, isAdmin: false }
const admin: OtohaPortalViewer = { portalEnabled: true, isAuthenticated: true, isAdmin: true }

function menuItem(partial: Partial<CustomMenuItem> & { id: string }): CustomMenuItem {
  return {
    label: partial.id,
    icon_svg: '',
    url: `https://example.com/${partial.id}`,
    visibility: 'user',
    sort_order: 0,
    ...partial,
  }
}

describe('isOtohaPortalEnabled', () => {
  it('is on only when the server says so', () => {
    expect(isOtohaPortalEnabled({ otoha_portal_enabled: true })).toBe(true)
    expect(isOtohaPortalEnabled({ otoha_portal_enabled: false })).toBe(false)
    expect(isOtohaPortalEnabled({})).toBe(false)
    expect(isOtohaPortalEnabled(null)).toBe(false)
    expect(isOtohaPortalEnabled(undefined)).toBe(false)
  })
})

describe('otohaPortalAppliesTo', () => {
  it('applies to visitors and regular users, never to admins or sites without the portal', () => {
    expect(otohaPortalAppliesTo(user)).toBe(true)
    expect(otohaPortalAppliesTo(visitor)).toBe(true)
    expect(otohaPortalAppliesTo(admin)).toBe(false)
    expect(otohaPortalAppliesTo({ ...user, portalEnabled: false })).toBe(false)
  })
})

describe('otohaPortalNav', () => {
  it('lists my Otoha, buy, usage, orders and account settings in that order', () => {
    expect(otohaPortalNav([]).map((item) => item.path)).toEqual([
      OTOHA_PORTAL_HOME,
      OTOHA_PORTAL_BUY,
      OTOHA_PORTAL_USAGE,
      '/orders',
      '/profile',
    ])
    expect(otohaPortalNav(undefined).every((item) => item.labelKey)).toBe(true)
  })

  it('adds only the user menu items the admin marked for the portal, in their order', () => {
    const items = [
      menuItem({ id: 'docs', sort_order: 1 }),
      menuItem({ id: 'help', label: 'Help', sort_order: 3, show_in_portal: true, icon_svg: '<svg/>' }),
      menuItem({ id: 'status', label: 'Status', sort_order: 2, show_in_portal: true }),
      menuItem({ id: 'ops', visibility: 'admin', show_in_portal: true }),
    ]
    const custom = otohaPortalNav(items).slice(5)
    expect(custom).toEqual([
      { path: '/custom/status', label: 'Status', iconSvg: '' },
      { path: '/custom/help', label: 'Help', iconSvg: '<svg/>' },
    ])
  })
})

describe('resolveOtohaPortalRedirect', () => {
  it('changes nothing without the portal or for admins', () => {
    for (const path of ['/', '/home', '/keys', '/dashboard', '/usage', '/custom/docs']) {
      expect(resolveOtohaPortalRedirect(path, { ...user, portalEnabled: false })).toBeNull()
      expect(resolveOtohaPortalRedirect(path, { ...visitor, portalEnabled: false })).toBeNull()
      expect(resolveOtohaPortalRedirect(path, admin)).toBeNull()
    }
  })

  it('sends the front page to my Otoha when signed in and to buying otherwise', () => {
    expect(resolveOtohaPortalRedirect('/', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/home', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/', visitor)).toBe(OTOHA_PORTAL_BUY)
    expect(resolveOtohaPortalRedirect('/home', visitor)).toBe(OTOHA_PORTAL_BUY)
  })

  it('lands signed-in users on my Otoha instead of the dashboard or the sign-in pages', () => {
    expect(resolveOtohaPortalRedirect('/dashboard', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/login', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/register', user)).toBe(OTOHA_PORTAL_HOME)
  })

  it('sends developer pages back to my Otoha', () => {
    for (const path of [
      '/keys',
      '/batch-image',
      '/docs/batch-image',
      '/available-channels',
      '/model-plaza',
      '/monitor',
      '/subscriptions',
      '/redeem',
      '/affiliate',
      '/key-usage',
      '/custom/docs',
      '/some/unknown/page',
    ]) {
      expect(resolveOtohaPortalRedirect(path, user), path).toBe(OTOHA_PORTAL_HOME)
      expect(resolveOtohaPortalRedirect(path, visitor), path).toBe(OTOHA_PORTAL_BUY)
    }
  })

  it('moves the general usage and purchase pages to their portal versions', () => {
    expect(resolveOtohaPortalRedirect('/usage', user)).toBe(OTOHA_PORTAL_USAGE)
    expect(resolveOtohaPortalRedirect('/purchase', user)).toBe(OTOHA_PORTAL_BUY)
  })

  it('keeps the portal pages, payment steps, sign-in and legal pages', () => {
    for (const path of [
      '/otoha',
      '/otoha/buy',
      '/otoha/result',
      '/otoha/account',
      '/otoha/usage',
      '/orders',
      '/profile',
      '/payment/result',
      '/payment/stripe',
      '/payment/qrcode',
      '/legal/terms',
      '/auth/callback',
      '/auth/oidc/callback',
      '/email-verify',
      '/forgot-password',
      '/reset-password',
      '/setup',
    ]) {
      expect(resolveOtohaPortalRedirect(path, user), path).toBeNull()
      expect(resolveOtohaPortalRedirect(path, visitor), path).toBeNull()
    }
    expect(resolveOtohaPortalRedirect('/login', visitor)).toBeNull()
    expect(resolveOtohaPortalRedirect('/register', visitor)).toBeNull()
  })

  it('leaves admin pages to the admin check', () => {
    expect(resolveOtohaPortalRedirect('/admin/dashboard', user)).toBeNull()
    expect(resolveOtohaPortalRedirect('/admin', visitor)).toBeNull()
  })

  it('does not treat look-alike paths as portal pages', () => {
    expect(resolveOtohaPortalRedirect('/otohax', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/orders-old', user)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/profiles', user)).toBe(OTOHA_PORTAL_HOME)
  })

  it('keeps custom pages the admin marked for the portal', () => {
    const items = [menuItem({ id: 'help', show_in_portal: true }), menuItem({ id: 'docs' })]
    expect(resolveOtohaPortalRedirect('/custom/help', user, items)).toBeNull()
    expect(resolveOtohaPortalRedirect('/custom/docs', user, items)).toBe(OTOHA_PORTAL_HOME)
    expect(resolveOtohaPortalRedirect('/custom/missing', user, items)).toBe(OTOHA_PORTAL_HOME)
  })
})

describe('siteSubtitleForAuth', () => {
  it('keeps the configured subtitle and the old default without the portal', () => {
    expect(siteSubtitleForAuth('My gateway', false)).toBe('My gateway')
    expect(siteSubtitleForAuth('', false)).toBe(DEFAULT_SITE_SUBTITLE)
    expect(siteSubtitleForAuth(undefined, false)).toBe(DEFAULT_SITE_SUBTITLE)
    expect(siteSubtitleForAuth(DEFAULT_SITE_SUBTITLE, false)).toBe(DEFAULT_SITE_SUBTITLE)
  })

  it('replaces the gateway default with the portal text in the portal', () => {
    expect(siteSubtitleForAuth('', true)).toBeNull()
    expect(siteSubtitleForAuth(undefined, true)).toBeNull()
    expect(siteSubtitleForAuth(` ${DEFAULT_SITE_SUBTITLE} `, true)).toBeNull()
    expect(siteSubtitleForAuth('Models for everyone', true)).toBe('Models for everyone')
  })
})
