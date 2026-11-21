/**
 * HomeView landing page contract.
 *
 * Three render modes are mutually exclusive, in priority order:
 *   1. custom home content (`home_content` HTML / URL → iframe)
 *   2. compact home (`compact_home_enabled`)
 *   3. the default product landing page (this suite's main subject)
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'

import HomeView from '../HomeView.vue'

const { appStore, authStore } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'Fallback site',
    siteLogo: '',
    siteVersion: '',
    contactInfo: '',
    apiBaseUrl: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
    showSuccess: vi.fn(),
    showError: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as { email?: string } | null,
    checkAuth: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    // Keys are echoed so assertions stay independent of the shipped translations.
    useI18n: () => ({ t: (key: string) => key }),
  }
})

function mountHome(settings: Record<string, unknown> = {}) {
  appStore.cachedPublicSettings = {
    site_name: 'Test site',
    site_subtitle: '',
    version: '',
    ...settings,
  }

  return mount(HomeView, {
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<span data-testid="icon" />' },
        ModelIcon: { template: '<span data-testid="model-icon" />' },
        VersionBadge: {
          props: ['version'],
          template: '<span data-testid="version-badge">{{ version }}</span>',
        },
      },
    },
  })
}

type HomeWrapper = ReturnType<typeof mountHome>

function linkTarget(wrapper: HomeWrapper, testId: string): unknown {
  const link = wrapper
    .findAllComponents(RouterLinkStub)
    .find((candidate) => candidate.attributes('data-testid') === testId)
  return link?.props('to')
}

function linkTargets(wrapper: HomeWrapper): unknown[] {
  return wrapper.findAllComponents(RouterLinkStub).map((link) => link.props('to'))
}

beforeEach(() => {
  authStore.isAuthenticated = false
  authStore.isAdmin = false
  authStore.user = null
  authStore.checkAuth.mockClear()
  appStore.fetchPublicSettings.mockClear()
  appStore.showSuccess.mockClear()
  appStore.showError.mockClear()
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
})

describe('HomeView render mode priority', () => {
  it('renders admin-provided HTML ahead of the landing page', () => {
    const wrapper = mountHome({ home_content: '<section id="custom-home">Custom home</section>' })

    expect(wrapper.get('#custom-home').text()).toBe('Custom home')
    expect(wrapper.find('[data-testid="landing-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('renders URL home content in an iframe', () => {
    const wrapper = mountHome({ home_content: ' https://example.com/home ' })

    expect(wrapper.get('iframe').attributes('src')).toBe('https://example.com/home')
    expect(wrapper.find('[data-testid="landing-home"]').exists()).toBe(false)
  })

  it('treats whitespace-only custom content as empty and shows the landing page', () => {
    const wrapper = mountHome({ home_content: ' \n\t ', compact_home_enabled: true })

    // Custom content wins over compact mode only when it has real content.
    expect(wrapper.find('[data-testid="landing-home"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
  })

  it('renders the landing page when neither override is configured', () => {
    const wrapper = mountHome({ compact_home_enabled: false })

    expect(wrapper.get('[data-testid="landing-home"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('keeps the compact page when compact mode is enabled', () => {
    const wrapper = mountHome({ compact_home_enabled: true })

    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
    expect(wrapper.find('[data-testid="landing-home"]').exists()).toBe(false)
    expect(authStore.checkAuth).toHaveBeenCalledOnce()
  })
})

describe('HomeView landing page structure', () => {
  it('renders navigation, hero, six capability cards, model matrix, billing CTA and footer', () => {
    const wrapper = mountHome()

    expect(wrapper.find('[data-testid="landing-home"] header nav').exists()).toBe(true)
    expect(wrapper.get('[data-testid="hero-primary-cta"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="capability-card"]')).toHaveLength(6)
    expect(wrapper.findAll('[data-testid="model-group"]').length).toBeGreaterThanOrEqual(6)
    expect(wrapper.findAll('[data-testid="model-chip"]').length).toBeGreaterThan(0)
    expect(wrapper.findAll('[data-testid="model-icon"]').length).toBeGreaterThan(0)
    expect(wrapper.get('[data-testid="billing-cta"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="landing-footer"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="footer-version"]').exists()).toBe(true)
  })

  it('sizes the layout with responsive breakpoints rather than fixed pixel widths', () => {
    const wrapper = mountHome()

    const fixedWidths = wrapper
      .findAll('*')
      .filter((element) => element.classes().some((name) => /^w-\[\d+px\]$/.test(name)))
    expect(fixedWidths).toHaveLength(0)

    expect(wrapper.get('[data-testid="capability-grid"]').classes()).toEqual(
      expect.arrayContaining(['sm:grid-cols-2', 'lg:grid-cols-3']),
    )
    // The code block scrolls on its own so the page never scrolls sideways.
    expect(wrapper.get('[data-testid="code-sample"]').element.closest('pre')?.className).toContain(
      'overflow-x-auto',
    )
  })

  it('does not publish unverifiable statistics', () => {
    const wrapper = mountHome()

    expect(wrapper.text()).not.toMatch(/\d+(\.\d+)?\s*%/)
    expect(wrapper.text()).not.toMatch(/99\.9|uptime/i)
  })

  it('marks decorative layers hidden and labels interactive controls', () => {
    const wrapper = mountHome()

    expect(wrapper.get('[data-testid="landing-home"] [aria-hidden="true"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="theme-toggle"]').attributes('aria-label')).toBe(
      'home.switchToDark',
    )
    expect(wrapper.get('[data-testid="copy-sample"]').attributes('aria-label')).toBe(
      'home.code.copyLabel',
    )
    expect(wrapper.get('[data-testid="copy-status"]').attributes('aria-live')).toBe('polite')
    expect(wrapper.get('[data-testid="copy-status"]').attributes('role')).toBe('status')
    // Every section that carries an anchor is a labelled region.
    expect(wrapper.get('#capabilities').attributes('aria-labelledby')).toBe('capabilities-heading')
    expect(wrapper.get('#models').attributes('aria-labelledby')).toBe('models-heading')
    expect(wrapper.get('#pricing').attributes('aria-labelledby')).toBe('pricing-heading')
  })

  it('respects prefers-reduced-motion for decorative animation', () => {
    const wrapper = mountHome()

    const guarded = wrapper
      .findAll('div')
      .filter((element) => element.classes().includes('motion-reduce:animate-none'))
    expect(guarded.length).toBeGreaterThan(0)
    const animated = wrapper
      .findAll('div')
      .filter((element) => element.classes().includes('motion-safe:animate-pulse-slow'))
    expect(animated.length).toBeGreaterThan(0)
  })

  it('toggles the dark class and stores the preference', async () => {
    const wrapper = mountHome()

    await wrapper.get('[data-testid="theme-toggle"]').trigger('click')

    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(wrapper.get('[data-testid="theme-toggle"]').attributes('aria-label')).toBe(
      'home.switchToLight',
    )
  })
})

describe('HomeView code sample', () => {
  it('builds the curl sample from the published API base URL', () => {
    const wrapper = mountHome({ api_base_url: 'https://api.example.com/v1/' })

    expect(wrapper.get('[data-testid="api-endpoint"]').text()).toBe('https://api.example.com')
    const sample = wrapper.get('[data-testid="code-sample"]').text()
    expect(sample).toContain('curl https://api.example.com/v1/chat/completions')
    expect(sample).toContain('Authorization: Bearer $SUB2API_API_KEY')
    expect(sample).not.toContain('/v1/v1/')
  })

  it('falls back to the current origin when no API base URL is configured', () => {
    const wrapper = mountHome({ api_base_url: '' })

    expect(wrapper.get('[data-testid="api-endpoint"]').text()).toBe(window.location.origin)
    expect(wrapper.get('[data-testid="code-sample"]').text()).toContain(
      `${window.location.origin}/v1/chat/completions`,
    )
  })

  it('copies the sample and announces the result through a live region', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })

    const wrapper = mountHome({ api_base_url: 'https://api.example.com' })
    const sample = wrapper.get('[data-testid="code-sample"]').text()

    expect(wrapper.get('[data-testid="copy-status"]').text()).toBe('')

    await wrapper.get('[data-testid="copy-sample"]').trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith(sample)
    expect(wrapper.get('[data-testid="copy-status"]').text()).toBe('home.code.copied')
    expect(appStore.showSuccess).toHaveBeenCalledWith('home.code.copied')
  })
})

describe('HomeView call-to-action targets', () => {
  it('sends anonymous visitors to login', () => {
    const wrapper = mountHome()

    expect(linkTarget(wrapper, 'hero-primary-cta')).toBe('/login')
    expect(linkTarget(wrapper, 'header-cta')).toBe('/login')
    expect(linkTarget(wrapper, 'billing-cta')).toBe('/login')
    expect(wrapper.find('[data-testid="billing-dashboard-cta"]').exists()).toBe(false)
  })

  it('sends authenticated users to their dashboard', () => {
    authStore.isAuthenticated = true
    authStore.user = { email: 'user@example.com' }
    const wrapper = mountHome()

    expect(linkTarget(wrapper, 'hero-primary-cta')).toBe('/dashboard')
    expect(linkTarget(wrapper, 'header-cta')).toBe('/dashboard')
    expect(linkTarget(wrapper, 'billing-dashboard-cta')).toBe('/dashboard')
  })

  it('sends administrators to the admin dashboard', () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true
    const wrapper = mountHome()

    expect(linkTarget(wrapper, 'hero-primary-cta')).toBe('/admin/dashboard')
    expect(linkTarget(wrapper, 'header-cta')).toBe('/admin/dashboard')
  })

  it('routes billing to checkout and offers both payment methods when both are enabled', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({ payment_enabled: true, subscription_enabled: true })

    expect(linkTarget(wrapper, 'billing-cta')).toBe('/purchase')
    expect(wrapper.get('[data-testid="billing-cta"]').text()).toBe('home.pricing.topUpOrSubscribe')
  })

  it('offers top-up only when subscriptions are disabled', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({ payment_enabled: true, subscription_enabled: false })

    expect(linkTarget(wrapper, 'billing-cta')).toBe('/purchase')
    expect(wrapper.get('[data-testid="billing-cta"]').text()).toBe('home.pricing.topUp')
  })

  it('offers subscriptions only when balance top-up is closed', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({
      payment_enabled: true,
      subscription_enabled: true,
      payment_balance_disabled: true,
    })

    expect(linkTarget(wrapper, 'billing-cta')).toBe('/purchase')
    expect(wrapper.get('[data-testid="billing-cta"]').text()).toBe('home.pricing.subscribe')
  })

  it('routes billing to subscriptions when payments are disabled', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({ payment_enabled: false, subscription_enabled: true })

    expect(linkTarget(wrapper, 'billing-cta')).toBe('/subscriptions')
    expect(wrapper.get('[data-testid="billing-cta"]').text()).toBe('home.pricing.subscribe')
  })

  it('falls back to the dashboard when neither payments nor subscriptions are enabled', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({ payment_enabled: false, subscription_enabled: false })

    expect(linkTarget(wrapper, 'billing-cta')).toBe('/dashboard')
    expect(wrapper.get('[data-testid="billing-cta"]').text()).toBe('home.pricing.dashboard')
  })
})

describe('HomeView model plaza entry', () => {
  it('links the hero secondary CTA to the model plaza when it is public', () => {
    const wrapper = mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })

    expect(linkTarget(wrapper, 'hero-secondary-cta')).toBe('/model-plaza')
    expect(linkTargets(wrapper)).toContain('/model-plaza')
  })

  it('hides the model plaza entry from anonymous visitors when sign-in is required', () => {
    const wrapper = mountHome({
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
      doc_url: 'https://docs.example.com',
    })

    expect(linkTargets(wrapper)).not.toContain('/model-plaza')
    // Secondary CTA falls back to the documentation link instead.
    expect(wrapper.get('[data-testid="hero-secondary-cta"]').attributes('href')).toBe(
      'https://docs.example.com/',
    )
  })

  it('shows the model plaza entry to authenticated visitors when sign-in is required', () => {
    authStore.isAuthenticated = true
    const wrapper = mountHome({ model_plaza_enabled: true, model_plaza_require_auth: true })

    expect(linkTargets(wrapper)).toContain('/model-plaza')
  })

  it('hides the model plaza entry when the feature flag is off', () => {
    const wrapper = mountHome({ model_plaza_enabled: false, model_plaza_require_auth: false })

    expect(linkTargets(wrapper)).not.toContain('/model-plaza')
  })

  it('falls back to the in-page capabilities anchor when nothing else is configured', () => {
    const wrapper = mountHome({ doc_url: '', model_plaza_enabled: false })

    expect(wrapper.get('[data-testid="hero-secondary-cta"]').attributes('href')).toBe('#capabilities')
  })
})

describe('HomeView footer and public settings reuse', () => {
  it('renders documentation, contact details and the version from public settings', () => {
    const wrapper = mountHome({
      doc_url: 'https://docs.example.com',
      contact_info: 'support@example.com',
      version: '1.2.3',
    })

    expect(wrapper.get('[data-testid="footer-docs"]').attributes('href')).toBe(
      'https://docs.example.com/',
    )
    expect(wrapper.get('[data-testid="footer-contact"]').text()).toContain('support@example.com')
    expect(wrapper.get('[data-testid="footer-contact"] a').attributes('href')).toBe(
      'mailto:support@example.com',
    )
    expect(wrapper.get('[data-testid="version-badge"]').text()).toBe('1.2.3')
  })

  it('links a URL contact value and leaves plain text unlinked', () => {
    const urlContact = mountHome({ contact_info: 'https://t.me/example' })
    expect(urlContact.get('[data-testid="footer-contact"] a').attributes('href')).toBe(
      'https://t.me/example',
    )

    const textContact = mountHome({ contact_info: 'WeChat: sub2api' })
    expect(textContact.find('[data-testid="footer-contact"] a').exists()).toBe(false)
    expect(textContact.get('[data-testid="footer-contact"]').text()).toContain('WeChat: sub2api')
  })

  it('uses the configured site name, subtitle and logo in the landing page', () => {
    const wrapper = mountHome({
      site_name: 'Acme Gateway',
      site_subtitle: 'Acme model gateway',
      site_logo: '/acme.svg',
    })

    expect(wrapper.get('[data-testid="landing-home"] header').text()).toContain('Acme Gateway')
    expect(wrapper.get('[data-testid="landing-home"] header img').attributes('src')).toBe(
      '/acme.svg',
    )
    expect(wrapper.text()).toContain('Acme model gateway')
  })

  it('loads public settings only when the store has not loaded them yet', () => {
    appStore.publicSettingsLoaded = false
    mountHome()
    appStore.publicSettingsLoaded = true

    expect(appStore.fetchPublicSettings).toHaveBeenCalledOnce()
  })
})