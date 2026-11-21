export default {
  batchImageGuide: {
    title: 'Batch Image Generation',
    description: 'Submit multiple prompts in one job and download the generated images when complete'
  },
  // Home Page — default landing page (custom home / compact home modes do not use these keys)
  home: {
    skipToContent: 'Skip to main content',
    viewDocs: 'View Documentation',
    docs: 'Docs',
    switchToLight: 'Switch to Light Mode',
    switchToDark: 'Switch to Dark Mode',
    dashboard: 'Dashboard',
    login: 'Login',
    goToDashboard: 'Go to Dashboard',
    nav: {
      ariaLabel: 'Main navigation',
      capabilities: 'Capabilities',
      models: 'Models',
      pricing: 'Pricing'
    },
    hero: {
      badge: 'Unified API gateway',
      title: 'One API for every frontier model',
      description:
        'OpenAI, Claude and Gemini compatible protocols, usage-based billing, and key & quota management in one place.',
      primaryCtaGuest: 'Sign up free',
      secondaryCtaModels: 'Browse models',
      secondaryCtaDocs: 'Read the docs',
      secondaryCtaLearn: 'See capabilities',
      chips: {
        protocol: 'OpenAI / Claude / Gemini compatible',
        billing: 'Pay as you go',
        keys: 'Keys and quotas'
      }
    },
    code: {
      title: 'Quick start',
      copy: 'Copy',
      copied: 'Copied',
      copyLabel: 'Copy the sample code',
      endpointLabel: 'API base URL',
      note: 'The base URL comes from the public site settings; model IDs follow what this site actually serves.'
    },
    capabilities: {
      title: 'Built for production workloads',
      subtitle: 'Access, billing, operations and usage accounting on a single path.',
      items: {
        unifiedAccess: {
          title: 'One key, many models',
          desc: 'Call models from multiple providers with a single key, without separate applications or accounts.'
        },
        protocolCompat: {
          title: 'Protocol compatible',
          desc: 'Speaks OpenAI, Claude and Gemini request formats, so existing SDKs and tools keep working.'
        },
        usageBilling: {
          title: 'Usage-based billing',
          desc: 'Billed on tokens actually consumed, with balance top-ups and subscriptions where the site enables them.'
        },
        keysQuota: {
          title: 'Keys and quotas',
          desc: 'Set credit, rate limits and expiry per key, and isolate usage by project.'
        },
        channelMonitor: {
          title: 'Channel monitoring',
          desc: 'Inspect upstream channel health and response behaviour, with switching per your configuration.'
        },
        usageAnalytics: {
          title: 'Usage and invoices',
          desc: 'Review request counts, token consumption and cost details by model and by date.'
        }
      }
    },
    models: {
      title: 'Model matrix',
      subtitle: 'Common models grouped by provider; the live catalogue depends on this site.',
      note: 'Names and icons only illustrate the integration scope and do not imply every model is enabled here.',
      groups: {
        openai: 'OpenAI',
        anthropic: 'Anthropic',
        google: 'Google',
        deepseek: 'DeepSeek',
        qwen: 'Qwen',
        moonshot: 'Kimi',
        zhipu: 'Zhipu GLM'
      }
    },
    pricing: {
      title: 'Pay only for what you use',
      signUp: 'Sign up free',
      topUp: 'Top up balance',
      topUpOrSubscribe: 'Top up or subscribe',
      subscribe: 'View subscriptions',
      dashboard: 'Go to dashboard',
      manage: 'View usage and invoices',
      descriptionGuest:
        'Sign in to create a key and start calling models; usage and invoices are available in the dashboard.',
      descriptionBoth:
        'Top up your balance or purchase a subscription to start calling; usage and invoices are in the dashboard.',
      descriptionTopUp:
        'Top up your balance to pay per usage; usage and invoices are available in the dashboard.',
      descriptionSubscribe:
        'Subscriptions are provisioned by the site administrator; quota and expiry are shown in the dashboard.',
      descriptionNone:
        'Online top-ups and subscriptions are not enabled on this site. Contact the site administrator for credit.'
    },
    footer: {
      resources: 'Resources',
      contact: 'Contact',
      contactEmpty: 'For help, reach the administrator through the contact method published on this site.',
      version: 'Version',
      builtFor: 'A unified model access layer for developers.',
      allRightsReserved: 'All rights reserved.'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key Usage',
    subtitle: 'Enter your API Key to view real-time spending and usage status',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: 'Query',
    querying: 'Querying...',
    privacyNote: 'Your Key is processed locally in the browser and will not be stored',
    dateRange: 'Date Range:',
    dateRangeToday: 'Today',
    dateRange7d: '7 Days',
    dateRange30d: '30 Days',
    dateRange90d: '90 Days',
    dateRangeCustom: 'Custom',
    apply: 'Apply',
    used: 'Used',
    detailInfo: 'Detail Information',
    tokenStats: 'Token Statistics',
    dailyDetail: 'Daily Detail',
    modelStats: 'Model Usage Statistics',
    // Table headers
    date: 'Date',
    model: 'Model',
    requests: 'Requests',
    inputTokens: 'Input Tokens',
    outputTokens: 'Output Tokens',
    cacheCreationTokens: 'Cache Creation',
    cacheReadTokens: 'Cache Read',
    cacheWriteTokens: 'Cache Write',
    totalTokens: 'Total Tokens',
    cost: 'Cost',
    // Status
    quotaMode: 'Key Quota Mode',
    walletBalance: 'Wallet Balance',
    // Ring card titles
    totalQuota: 'Total Quota',
    limit5h: '5-Hour Limit',
    limitDaily: 'Daily Limit',
    limit7d: '7-Day Limit',
    limitWeekly: 'Weekly Limit',
    limitMonthly: 'Monthly Limit',
    // Detail rows
    remainingQuota: 'Remaining Quota',
    expiresAt: 'Expires At',
    todayExpires: '(expires today)',
    daysLeft: '({days} days)',
    usedQuota: 'Used Quota',
    resetNow: 'Resetting soon',
    subscriptionType: 'Subscription Type',
    billingType: 'Billing Type',
    subscriptionExpires: 'Subscription Expires',
    // Usage stat cells
    todayRequests: 'Today Requests',
    todayInputTokens: 'Today Input',
    todayOutputTokens: 'Today Output',
    todayTokens: 'Today Tokens',
    todayCacheCreation: 'Today Cache Creation',
    todayCacheRead: 'Today Cache Read',
    todayCost: 'Today Cost',
    rpmTpm: 'RPM / TPM',
    totalRequests: 'Total Requests',
    totalInputTokens: 'Total Input',
    totalOutputTokens: 'Total Output',
    totalTokensLabel: 'Total Tokens',
    totalCacheCreation: 'Total Cache Creation',
    totalCacheRead: 'Total Cache Read',
    totalCost: 'Total Cost',
    avgDuration: 'Avg Duration',
    // Messages
    enterApiKey: 'Please enter an API Key',
    querySuccess: 'Query successful',
    queryFailed: 'Query failed',
    queryFailedRetry: 'Query failed, please try again later',
    noDailyUsage: 'No daily usage data',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API Setup',
    description: 'Configure your Sub2API instance',
    database: {
      title: 'Database Configuration',
      description: 'Connect to your PostgreSQL database',
      host: 'Host',
      port: 'Port',
      username: 'Username',
      password: 'Password',
      databaseName: 'Database Name',
      sslMode: 'SSL Mode',
      passwordPlaceholder: 'Password',
      ssl: {
        disable: 'Disable',
        require: 'Require',
        verifyCa: 'Verify CA',
        verifyFull: 'Verify Full'
      }
    },
    redis: {
      title: 'Redis Configuration',
      description: 'Connect to your Redis server',
      host: 'Host',
      port: 'Port',
      username: 'Username (optional)',
      password: 'Password (optional)',
      database: 'Database',
      usernamePlaceholder: 'Leave empty for default user',
      passwordPlaceholder: 'Password',
      enableTls: 'Enable TLS',
      enableTlsHint: 'Use TLS when connecting to Redis (public CA certs)'
    },
    admin: {
      title: 'Admin Account',
      description: 'Create your administrator account',
      email: 'Email',
      password: 'Password',
      confirmPassword: 'Confirm Password',
      passwordPlaceholder: 'Min 8 characters',
      confirmPasswordPlaceholder: 'Confirm password',
      passwordMismatch: 'Passwords do not match'
    },
    ready: {
      title: 'Ready to Install',
      description: 'Review your configuration and complete setup',
      database: 'Database',
      redis: 'Redis',
      adminEmail: 'Admin Email'
    },
    status: {
      testing: 'Testing...',
      success: 'Connection Successful',
      testConnection: 'Test Connection',
      installing: 'Installing...',
      completeInstallation: 'Complete Installation',
      completed: 'Installation completed!',
      redirecting: 'Redirecting to login page...',
      restarting: 'Service is restarting, please wait...',
      timeout: 'Service restart is taking longer than expected. Please refresh the page manually.'
    }
  },

  // Common
}
