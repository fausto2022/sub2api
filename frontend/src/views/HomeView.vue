<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/logo.svg'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/logo.svg'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page: product landing page -->
  <div
    v-else
    data-testid="landing-home"
    class="relative flex min-h-screen flex-col bg-accent-50 text-accent-900 dark:bg-dark-950 dark:text-dark-100"
  >
    <!-- Skip link: first tab stop for keyboard users -->
    <a
      href="#capabilities"
      class="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50 focus:rounded-lg focus:bg-white focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-accent-900 focus:shadow-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:focus:bg-dark-800 dark:focus:text-dark-50 dark:focus-visible:ring-primary-400"
      >{{ t('home.skipToContent') }}</a
    >

    <!-- Background decorations (decorative only, clipped to avoid horizontal scroll) -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
      <div class="absolute inset-0 bg-mesh-gradient"></div>
      <div
        class="absolute -right-24 -top-24 h-72 w-72 rounded-full bg-primary-400/20 blur-3xl motion-safe:animate-pulse-slow motion-reduce:animate-none"
      ></div>
      <div
        class="absolute -bottom-32 -left-24 h-72 w-72 rounded-full bg-cyan-400/10 blur-3xl motion-safe:animate-pulse-slow motion-reduce:animate-none"
      ></div>
    </div>

    <!-- Header / navigation -->
    <header
      class="relative z-20 border-b border-accent-200/70 bg-white/85 backdrop-blur-md dark:border-dark-700/60 dark:bg-dark-950/85"
    >
      <nav
        class="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-2 px-4 py-3 sm:gap-3 sm:px-6 lg:px-8"
        :aria-label="t('home.nav.ariaLabel')"
      >
        <router-link to="/" class="flex min-w-0 items-center gap-2.5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 focus-visible:ring-offset-white dark:focus-visible:ring-primary-400 dark:focus-visible:ring-offset-dark-950">
          <img
            :src="siteLogo || '/logo.svg'"
            :alt="siteName"
            class="h-9 w-9 shrink-0 rounded-xl object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold text-accent-900 dark:text-dark-50">{{
            siteName
          }}</span>
        </router-link>

        <!-- Section anchors: desktop only; mobile keeps the compact icon actions -->
        <div class="hidden items-center gap-1 md:flex">
          <a
            href="#capabilities"
            class="rounded-lg px-3 py-2 text-sm font-medium text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
            >{{ t('home.nav.capabilities') }}</a
          >
          <a
            href="#models"
            class="rounded-lg px-3 py-2 text-sm font-medium text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
            >{{ t('home.nav.models') }}</a
          >
          <a
            href="#pricing"
            class="rounded-lg px-3 py-2 text-sm font-medium text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
            >{{ t('home.nav.pricing') }}</a
          >
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm font-medium text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
          >
            {{ t('home.docs') }}
            <Icon name="externalLink" size="xs" aria-hidden="true" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm font-medium text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
          >
            <Icon name="grid" size="sm" aria-hidden="true" />
            {{ t('nav.modelPlaza') }}
          </router-link>
        </div>

        <div class="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400 md:hidden"
            :aria-label="t('home.viewDocs')"
          >
            <Icon name="book" size="md" aria-hidden="true" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400 md:hidden"
            :aria-label="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" aria-hidden="true" />
          </router-link>
          <button
            type="button"
            data-testid="theme-toggle"
            class="flex h-10 w-10 items-center justify-center rounded-lg text-accent-600 transition-colors duration-150 hover:bg-accent-100 hover:text-accent-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-50 dark:focus-visible:ring-primary-400"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" aria-hidden="true" />
            <Icon v-else name="moon" size="md" aria-hidden="true" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            data-testid="header-cta"
            class="btn btn-primary ml-1 px-3.5 py-2 text-sm"
          >
            <span
              v-if="isAuthenticated && userInitial"
              class="flex h-5 w-5 items-center justify-center rounded-full bg-white/20 text-[10px] font-semibold"
              aria-hidden="true"
              >{{ userInitial }}</span
            >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="relative z-10 flex-1">
      <!-- Hero -->
      <section class="mx-auto max-w-7xl px-4 pb-14 pt-10 sm:px-6 lg:px-8 lg:pb-20 lg:pt-16">
        <div class="grid items-center gap-10 lg:grid-cols-2 lg:gap-14">
          <div class="min-w-0">
            <span class="badge badge-primary">{{ t('home.hero.badge') }}</span>
            <h1
              class="mt-5 text-3xl font-bold leading-tight tracking-tight text-accent-900 sm:text-4xl lg:text-5xl dark:text-dark-50"
            >
              {{ t('home.hero.title') }}
            </h1>
            <p class="mt-4 max-w-xl text-base leading-relaxed text-accent-600 sm:text-lg dark:text-dark-300">
              {{ heroSubtitle }}
            </p>

            <div class="mt-7 flex flex-wrap items-center gap-3">
              <router-link
                :to="primaryCtaTarget"
                data-testid="hero-primary-cta"
                class="btn btn-primary px-5 py-3 text-base shadow-glow"
              >
                {{ primaryCtaLabel }}
                <Icon name="arrowRight" size="md" aria-hidden="true" />
              </router-link>
              <router-link
                v-if="showModelPlazaEntry"
                to="/model-plaza"
                data-testid="hero-secondary-cta"
                class="btn btn-secondary px-5 py-3 text-base"
              >
                {{ t('home.hero.secondaryCtaModels') }}
              </router-link>
              <a
                v-else-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                data-testid="hero-secondary-cta"
                class="btn btn-secondary px-5 py-3 text-base"
              >
                {{ t('home.hero.secondaryCtaDocs') }}
                <Icon name="externalLink" size="sm" aria-hidden="true" />
              </a>
              <a
                v-else
                href="#capabilities"
                data-testid="hero-secondary-cta"
                class="btn btn-secondary px-5 py-3 text-base"
              >
                {{ t('home.hero.secondaryCtaLearn') }}
              </a>
            </div>

            <ul class="mt-7 flex flex-wrap gap-x-5 gap-y-2 text-sm text-accent-600 dark:text-dark-300">
              <li
                v-for="chip in heroChips"
                :key="chip"
                class="flex items-center gap-1.5"
              >
                <Icon
                  name="checkCircle"
                  size="sm"
                  class="text-primary-600 dark:text-primary-400"
                  aria-hidden="true"
                />
                {{ chip }}
              </li>
            </ul>
          </div>

          <!-- Code sample card (uses the public API base URL, copy-to-clipboard) -->
          <div class="min-w-0 lg:w-full">
            <div class="card overflow-hidden">
              <div
                class="flex flex-wrap items-center justify-between gap-2 border-b border-accent-200/70 px-4 py-3 dark:border-dark-700/60"
              >
                <div class="flex min-w-0 items-center gap-2">
                  <span
                    class="flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                  >
                    <Icon name="terminal" size="sm" aria-hidden="true" />
                  </span>
                  <span class="min-w-0 truncate text-sm font-medium text-accent-900 dark:text-dark-100">{{
                    t('home.code.title')
                  }}</span>
                </div>
                <button
                  type="button"
                  data-testid="copy-sample"
                  class="btn btn-secondary px-2.5 py-1.5 text-xs"
                  :aria-label="t('home.code.copyLabel')"
                  @click="copySample"
                >
                  <Icon :name="copied ? 'check' : 'copy'" size="xs" aria-hidden="true" />
                  <span>{{ copied ? t('home.code.copied') : t('home.code.copy') }}</span>
                </button>
              </div>

              <div
                class="border-b border-accent-200/70 bg-accent-50/70 px-4 py-2.5 dark:border-dark-700/60 dark:bg-dark-900/60"
              >
                <p class="text-xs font-medium text-accent-600 dark:text-dark-300">
                  {{ t('home.code.endpointLabel') }}
                </p>
                <p
                  data-testid="api-endpoint"
                  class="mt-1 break-all font-mono text-xs text-accent-800 dark:text-dark-200"
                >
                  {{ apiEndpoint }}
                </p>
              </div>

              <pre
                class="max-w-full overflow-x-auto bg-accent-900 px-4 py-4 text-xs leading-relaxed dark:bg-dark-900"
              ><code data-testid="code-sample" class="font-mono text-dark-100">{{ codeSample }}</code></pre>

              <p class="px-4 py-3 text-xs leading-relaxed text-accent-600 dark:text-dark-300">
                {{ t('home.code.note') }}
              </p>
            </div>
            <p data-testid="copy-status" class="sr-only" role="status" aria-live="polite">
              {{ copied ? t('home.code.copied') : '' }}
            </p>
          </div>
        </div>
      </section>

      <!-- Capability matrix -->
      <section
        id="capabilities"
        aria-labelledby="capabilities-heading"
        class="scroll-mt-6 border-y border-accent-200/70 bg-white/70 py-14 dark:border-dark-700/60 dark:bg-dark-900/40 lg:py-16"
      >
        <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <h2
            id="capabilities-heading"
            class="text-2xl font-bold tracking-tight text-accent-900 sm:text-3xl dark:text-dark-50"
          >
            {{ t('home.capabilities.title') }}
          </h2>
          <p class="mt-3 max-w-2xl text-sm leading-relaxed text-accent-600 sm:text-base dark:text-dark-300">
            {{ t('home.capabilities.subtitle') }}
          </p>

          <div data-testid="capability-grid" class="mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <article
              v-for="capability in capabilities"
              :key="capability.key"
              data-testid="capability-card"
              class="card card-hover p-5"
            >
              <span
                class="flex h-11 w-11 items-center justify-center rounded-xl bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
              >
                <Icon :name="capabilityIcons[capability.key]" size="lg" aria-hidden="true" />
              </span>
              <h3 class="mt-4 text-base font-semibold text-accent-900 dark:text-dark-50">
                {{ capability.title }}
              </h3>
              <p class="mt-2 text-sm leading-relaxed text-accent-600 dark:text-dark-300">
                {{ capability.description }}
              </p>
            </article>
          </div>
        </div>
      </section>

      <!-- Model matrix (static list grouped by vendor) -->
      <section id="models" aria-labelledby="models-heading" class="scroll-mt-6 py-14 lg:py-16">
        <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <h2
            id="models-heading"
            class="text-2xl font-bold tracking-tight text-accent-900 sm:text-3xl dark:text-dark-50"
          >
            {{ t('home.models.title') }}
          </h2>
          <p class="mt-3 max-w-2xl text-sm leading-relaxed text-accent-600 sm:text-base dark:text-dark-300">
            {{ t('home.models.subtitle') }}
          </p>

          <div class="mt-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <div
              v-for="group in modelGroups"
              :key="group.key"
              data-testid="model-group"
              class="card p-5"
            >
              <div class="flex items-center justify-between gap-2">
                <h3 class="min-w-0 truncate text-sm font-semibold text-accent-900 dark:text-dark-50">
                  {{ group.label }}
                </h3>
                <span class="shrink-0 text-xs tabular-nums text-accent-600 dark:text-dark-300">{{
                  group.models.length
                }}</span>
              </div>
              <ul class="mt-3 flex flex-wrap gap-2">
                <li
                  v-for="model in group.models"
                  :key="model"
                  data-testid="model-chip"
                  class="inline-flex min-w-0 items-center gap-1.5 rounded-lg border border-accent-200/70 bg-accent-50 px-2.5 py-1.5 dark:border-dark-700 dark:bg-dark-800/60"
                >
                  <ModelIcon :model="model" size="16px" />
                  <span class="min-w-0 truncate font-mono text-xs text-accent-700 dark:text-dark-200">{{
                    model
                  }}</span>
                </li>
              </ul>
            </div>
          </div>
          <p class="mt-5 text-xs leading-relaxed text-accent-600 dark:text-dark-300">
            {{ t('home.models.note') }}
          </p>
        </div>
      </section>

      <!-- Pricing CTA -->
      <section id="pricing" aria-labelledby="pricing-heading" class="scroll-mt-6 pb-16 lg:pb-20">
        <div class="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div
            class="relative overflow-hidden rounded-3xl border border-primary-200/60 bg-gradient-to-br from-primary-50 via-white to-cyan-50 p-6 sm:p-10 dark:border-primary-900/40 dark:from-dark-900 dark:via-dark-900 dark:to-dark-950"
          >
            <div class="pointer-events-none absolute inset-0 overflow-hidden" aria-hidden="true">
              <div
                class="absolute -right-16 -top-20 h-56 w-56 rounded-full bg-primary-400/10 blur-3xl"
              ></div>
            </div>
            <div class="relative flex flex-col gap-6 lg:flex-row lg:items-center lg:justify-between">
              <div class="min-w-0 max-w-2xl">
                <h2
                  id="pricing-heading"
                  class="text-2xl font-bold tracking-tight text-accent-900 dark:text-dark-50"
                >
                  {{ t('home.pricing.title') }}
                </h2>
                <p class="mt-3 text-sm leading-relaxed text-accent-600 sm:text-base dark:text-dark-300">
                  {{ pricingDescription }}
                </p>
              </div>
              <div class="flex shrink-0 flex-wrap gap-3">
                <router-link
                  :to="billingCtaTarget"
                  data-testid="billing-cta"
                  class="btn btn-primary px-5 py-3"
                >
                  {{ billingCtaLabel }}
                </router-link>
                <router-link
                  v-if="isAuthenticated"
                  :to="dashboardPath"
                  data-testid="billing-dashboard-cta"
                  class="btn btn-secondary px-5 py-3"
                >
                  {{ t('home.pricing.manage') }}
                </router-link>
              </div>
            </div>
          </div>
        </div>
      </section>
    </main>

    <!-- Footer -->
    <footer
      data-testid="landing-footer"
      class="relative z-10 border-t border-accent-200/70 bg-white/80 py-10 dark:border-dark-700/60 dark:bg-dark-950/80"
    >
      <div class="mx-auto grid max-w-7xl gap-8 px-4 sm:grid-cols-2 sm:px-6 lg:grid-cols-4 lg:px-8">
        <div class="min-w-0 lg:col-span-2">
          <div class="flex min-w-0 items-center gap-2.5">
            <img
              :src="siteLogo || '/logo.svg'"
              :alt="siteName"
              class="h-8 w-8 shrink-0 rounded-lg object-contain"
            />
            <span class="min-w-0 truncate text-sm font-semibold text-accent-900 dark:text-dark-50">{{
              siteName
            }}</span>
          </div>
          <p class="mt-2 max-w-sm text-sm leading-relaxed text-accent-600 dark:text-dark-300">
            {{ heroSubtitle }}
          </p>
        </div>

        <div class="min-w-0">
          <h2 class="text-xs font-semibold uppercase tracking-wide text-accent-600 dark:text-dark-300">
            {{ t('home.footer.resources') }}
          </h2>
          <ul class="mt-3 space-y-2 text-sm">
            <li v-if="docUrl">
              <a
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                data-testid="footer-docs"
                class="inline-flex items-center gap-1.5 rounded text-accent-700 transition-colors duration-150 hover:text-primary-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-200 dark:hover:text-primary-300 dark:focus-visible:ring-primary-400"
              >
                {{ t('home.docs') }}
                <Icon name="externalLink" size="xs" aria-hidden="true" />
              </a>
            </li>
            <li v-if="showModelPlazaEntry">
              <router-link
                to="/model-plaza"
                class="inline-flex rounded text-accent-700 transition-colors duration-150 hover:text-primary-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-200 dark:hover:text-primary-300 dark:focus-visible:ring-primary-400"
              >
                {{ t('nav.modelPlaza') }}
              </router-link>
            </li>
            <li>
              <a
                :href="githubUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex items-center gap-1.5 rounded text-accent-700 transition-colors duration-150 hover:text-primary-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-dark-200 dark:hover:text-primary-300 dark:focus-visible:ring-primary-400"
              >
                GitHub
                <Icon name="externalLink" size="xs" aria-hidden="true" />
              </a>
            </li>
          </ul>
        </div>

        <div class="min-w-0">
          <h2 class="text-xs font-semibold uppercase tracking-wide text-accent-600 dark:text-dark-300">
            {{ t('home.footer.contact') }}
          </h2>
          <p data-testid="footer-contact" class="mt-3 break-words text-sm text-accent-700 dark:text-dark-200">
            <a
              v-if="contactInfoHref"
              :href="contactInfoHref"
              target="_blank"
              rel="noopener noreferrer"
              class="rounded hover:text-primary-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:text-primary-300 dark:focus-visible:ring-primary-400"
              >{{ contactInfoText }}</a
            >
            <template v-else>{{ contactInfoText || t('home.footer.contactEmpty') }}</template>
          </p>
          <div class="mt-4 flex items-center gap-2 text-sm">
            <span class="text-accent-600 dark:text-dark-300">{{ t('home.footer.version') }}</span>
            <span data-testid="footer-version" class="min-w-0">
              <VersionBadge :version="siteVersion" />
            </span>
          </div>
        </div>
      </div>

      <div
        class="mx-auto mt-8 flex max-w-7xl flex-col gap-2 border-t border-accent-200/70 px-4 pt-6 text-sm text-accent-600 sm:flex-row sm:items-center sm:justify-between sm:px-6 lg:px-8 dark:border-dark-700/60 dark:text-dark-300"
      >
        <p class="min-w-0 break-words">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <p class="shrink-0">{{ t('home.footer.builtFor') }}</p>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import VersionBadge from '@/components/common/VersionBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()
const { copied, copyToClipboard } = useClipboard()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const siteVersion = computed(() => appStore.cachedPublicSettings?.version || appStore.siteVersion || '')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const apiBaseUrl = computed(() => (appStore.cachedPublicSettings?.api_base_url || appStore.apiBaseUrl || '').trim())
const contactInfoText = computed(() => (appStore.cachedPublicSettings?.contact_info || appStore.contactInfo || '').trim())
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const modelPlazaRequiresAuth = computed(
  () => appStore.cachedPublicSettings?.model_plaza_require_auth === true,
)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value),
)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// ==================== Landing page content ====================

// Hero: the admin-configured subtitle wins; the i18n description is the fallback.
const heroSubtitle = computed(() => siteSubtitle.value || t('home.hero.description'))

const primaryCtaTarget = computed(() => (isAuthenticated.value ? dashboardPath.value : '/login'))
const primaryCtaLabel = computed(() =>
  isAuthenticated.value ? t('home.goToDashboard') : t('home.hero.primaryCtaGuest'),
)

const heroChips = computed(() => [
  t('home.hero.chips.protocol'),
  t('home.hero.chips.billing'),
  t('home.hero.chips.keys'),
])

const capabilityIcons = {
  unifiedAccess: 'cube',
  protocolCompat: 'swap',
  usageBilling: 'creditCard',
  keysQuota: 'key',
  channelMonitor: 'chartBar',
  usageAnalytics: 'document',
} as const

const capabilities = computed(() => [
  {
    key: 'unifiedAccess',
    title: t('home.capabilities.items.unifiedAccess.title'),
    description: t('home.capabilities.items.unifiedAccess.desc'),
  },
  {
    key: 'protocolCompat',
    title: t('home.capabilities.items.protocolCompat.title'),
    description: t('home.capabilities.items.protocolCompat.desc'),
  },
  {
    key: 'usageBilling',
    title: t('home.capabilities.items.usageBilling.title'),
    description: t('home.capabilities.items.usageBilling.desc'),
  },
  {
    key: 'keysQuota',
    title: t('home.capabilities.items.keysQuota.title'),
    description: t('home.capabilities.items.keysQuota.desc'),
  },
  {
    key: 'channelMonitor',
    title: t('home.capabilities.items.channelMonitor.title'),
    description: t('home.capabilities.items.channelMonitor.desc'),
  },
  {
    key: 'usageAnalytics',
    title: t('home.capabilities.items.usageAnalytics.title'),
    description: t('home.capabilities.items.usageAnalytics.desc'),
  },
] as const)

// Static model wall grouped by vendor; ModelIcon (extracted @lobehub/icons paths) supplies the marks.
const modelGroups = computed(() => [
  {
    key: 'openai',
    label: t('home.models.groups.openai'),
    models: ['gpt-5.4', 'gpt-5.4-mini', 'o4-mini'],
  },
  {
    key: 'anthropic',
    label: t('home.models.groups.anthropic'),
    models: ['claude-opus-4-1', 'claude-sonnet-4-5', 'claude-haiku-4-5'],
  },
  {
    key: 'google',
    label: t('home.models.groups.google'),
    models: ['gemini-2.5-pro', 'gemini-2.5-flash'],
  },
  {
    key: 'deepseek',
    label: t('home.models.groups.deepseek'),
    models: ['deepseek-v4-pro', 'deepseek-v4', 'deepseek-reasoner'],
  },
  {
    key: 'qwen',
    label: t('home.models.groups.qwen'),
    models: ['qwen3-max', 'qwen3-coder-plus'],
  },
  {
    key: 'moonshot',
    label: t('home.models.groups.moonshot'),
    models: ['kimi-k2', 'kimi-k2-thinking'],
  },
  {
    key: 'zhipu',
    label: t('home.models.groups.zhipu'),
    models: ['glm-4.6', 'glm-4.5-air'],
  },
])

// API endpoint + copyable curl sample built from the public API base URL.
// A base URL that already ends in /v1 is normalized so the sample never doubles the prefix.
const apiEndpoint = computed(() => {
  const raw = (apiBaseUrl.value || window.location.origin).replace(/\/+$/, '')
  return raw.replace(/\/v1$/i, '')
})

const codeSample = computed(() =>
  [
    `curl ${apiEndpoint.value}/v1/chat/completions \\`,
    '  -H "Authorization: Bearer $SUB2API_API_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '{"model":"<model-id>","messages":[{"role":"user","content":"hello"}]}'`,
  ].join('\n'),
)

function copySample() {
  // Explicit toast message: keeps the confirmation in the page's own namespace instead of
  // depending on the global `common.*` clipboard keys.
  void copyToClipboard(codeSample.value, t('home.code.copied'))
}

// Billing CTA follows the public payment / subscription switches.
const paymentEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.payment))
const subscriptionEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.subscription))
const balanceTopUpEnabled = computed(
  () => paymentEnabled.value && appStore.cachedPublicSettings?.payment_balance_disabled !== true,
)

const billingCtaTarget = computed(() => {
  if (!isAuthenticated.value) return '/login'
  if (paymentEnabled.value) return '/purchase'
  if (subscriptionEnabled.value) return '/subscriptions'
  return dashboardPath.value
})

const billingCtaLabel = computed(() => {
  if (!isAuthenticated.value) return t('home.pricing.signUp')
  if (paymentEnabled.value) {
    if (subscriptionEnabled.value) {
      return balanceTopUpEnabled.value
        ? t('home.pricing.topUpOrSubscribe')
        : t('home.pricing.subscribe')
    }
    return t('home.pricing.topUp')
  }
  if (subscriptionEnabled.value) return t('home.pricing.subscribe')
  return t('home.pricing.dashboard')
})

const pricingDescription = computed(() => {
  if (!isAuthenticated.value) return t('home.pricing.descriptionGuest')
  if (balanceTopUpEnabled.value) {
    return subscriptionEnabled.value
      ? t('home.pricing.descriptionBoth')
      : t('home.pricing.descriptionTopUp')
  }
  if (subscriptionEnabled.value) return t('home.pricing.descriptionSubscribe')
  return t('home.pricing.descriptionNone')
})

// Contact info may be an email, a URL or plain text; only link when it is linkable.
const contactInfoHref = computed(() => {
  const value = contactInfoText.value
  if (!value) return ''
  if (/^https?:\/\//i.test(value)) return sanitizeUrl(value)
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) return `mailto:${value}`
  return ''
})

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()

  // Check auth state
  authStore.checkAuth()

  // Ensure public settings are loaded (will use cache if already loaded from injected config)
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>