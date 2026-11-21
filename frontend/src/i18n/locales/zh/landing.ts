export default {
  batchImageGuide: {
    title: '图片批量生成',
    description: '一次提交多条提示词，任务完成后可统一下载图片结果'
  },
  // Home Page — 默认落地页（自定义首页 / 紧凑首页模式不使用这些键）
  home: {
    skipToContent: '跳到主要内容',
    viewDocs: '查看文档',
    docs: '文档',
    switchToLight: '切换到浅色模式',
    switchToDark: '切换到深色模式',
    dashboard: '控制台',
    login: '登录',
    goToDashboard: '进入控制台',
    nav: {
      ariaLabel: '主导航',
      capabilities: '能力',
      models: '模型',
      pricing: '计费'
    },
    hero: {
      badge: '统一 API 网关',
      title: '一个 API，接入全系前沿模型',
      description: '统一 OpenAI / Claude / Gemini 兼容协议，按量计费、密钥与配额管理一站完成。',
      primaryCtaGuest: '免费注册',
      secondaryCtaModels: '查看模型',
      secondaryCtaDocs: '查看文档',
      secondaryCtaLearn: '了解能力',
      chips: {
        protocol: 'OpenAI / Claude / Gemini 兼容',
        billing: '按量计费',
        keys: '密钥与配额'
      }
    },
    code: {
      title: '快速开始',
      copy: '复制',
      copied: '已复制',
      copyLabel: '复制示例代码',
      endpointLabel: 'API 地址',
      note: '示例中的 base URL 取自站点公开设置，模型 ID 以站点实际提供的列表为准。'
    },
    capabilities: {
      title: '为生产环境准备的能力',
      subtitle: '接入、计费、运维与用量核算在同一条链路上完成。',
      items: {
        unifiedAccess: {
          title: '多模型统一接入',
          desc: '一个密钥调用多家供应商的模型，无需分别申请与维护账号。'
        },
        protocolCompat: {
          title: '协议兼容',
          desc: '同时兼容 OpenAI、Claude 与 Gemini 的请求格式，现有 SDK 与工具可直接复用。'
        },
        usageBilling: {
          title: '按量计费',
          desc: '按实际消耗的 tokens 计费，支持余额与订阅两种付费方式（以站点开关为准）。'
        },
        keysQuota: {
          title: '密钥与配额',
          desc: '为每个密钥设置额度、限额与有效期，按项目隔离调用。'
        },
        channelMonitor: {
          title: '渠道监控',
          desc: '查看上游渠道的健康与响应情况，异常时按配置切换。'
        },
        usageAnalytics: {
          title: '用量与账单',
          desc: '按模型与日期查看请求量、token 消耗与费用明细。'
        }
      }
    },
    models: {
      title: '模型矩阵',
      subtitle: '按供应商分组的常见模型，实际可用清单以站点为准。',
      note: '名称与图标仅用于说明接入范围，不代表当前站点已上架全部模型。',
      groups: {
        openai: 'OpenAI',
        anthropic: 'Anthropic',
        google: 'Google',
        deepseek: 'DeepSeek',
        qwen: '通义千问',
        moonshot: 'Kimi',
        zhipu: '智谱 GLM'
      }
    },
    pricing: {
      title: '按量计费，用多少付多少',
      signUp: '免费注册',
      topUp: '充值余额',
      topUpOrSubscribe: '充值或订阅',
      subscribe: '查看订阅',
      dashboard: '进入控制台',
      manage: '查看用量与账单',
      descriptionGuest: '注册并登录后即可创建密钥调用模型，用量与账单在控制台实时可查。',
      descriptionBoth: '充值余额或购买订阅后即可调用，用量与账单在控制台实时可查。',
      descriptionTopUp: '充值余额后按实际用量计费，用量与账单在控制台实时可查。',
      descriptionSubscribe: '订阅由站点管理员开通，额度与到期时间在控制台可查。',
      descriptionNone: '本站点暂未开放在线充值或订阅，请联系站点管理员获取额度。'
    },
    footer: {
      resources: '资源',
      contact: '联系方式',
      contactEmpty: '如需帮助，请通过站点管理员公布的方式联系。',
      version: '版本',
      builtFor: '为开发者提供统一的模型接入层。',
      allRightsReserved: '保留所有权利。'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key 用量查询',
    subtitle: '输入您的 API Key 以查看实时消费金额与使用状态',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: '查询',
    querying: '查询中...',
    privacyNote: '您的 Key 仅在浏览器本地处理，不会被存储',
    dateRange: '统计范围:',
    dateRangeToday: '今日',
    dateRange7d: '7 天',
    dateRange30d: '30 天',
    dateRange90d: '90 天',
    dateRangeCustom: '自定义',
    apply: '应用',
    used: '已使用',
    detailInfo: '详细信息',
    tokenStats: 'Token 统计',
    dailyDetail: '按日明细',
    modelStats: '模型用量统计',
    // Table headers
    date: '日期',
    model: '模型',
    requests: '请求数',
    inputTokens: '输入 Tokens',
    outputTokens: '输出 Tokens',
    cacheCreationTokens: '缓存创建',
    cacheReadTokens: '缓存读取',
    cacheWriteTokens: '缓存写入',
    totalTokens: '总 Tokens',
    cost: '费用',
    // Status
    quotaMode: 'Key 限额模式',
    walletBalance: '钱包余额',
    // Ring card titles
    totalQuota: '总额度',
    limit5h: '5 小时限额',
    limitDaily: '日限额',
    limit7d: '7 天限额',
    limitWeekly: '周限额',
    limitMonthly: '月限额',
    // Detail rows
    remainingQuota: '剩余额度',
    expiresAt: '过期时间',
    todayExpires: '(今日到期)',
    daysLeft: '({days} 天)',
    usedQuota: '已用额度',
    resetNow: '即将重置',
    subscriptionType: '订阅类型',
    billingType: '计费方式',
    subscriptionExpires: '订阅到期',
    // Usage stat cells
    todayRequests: '今日请求',
    todayInputTokens: '今日输入',
    todayOutputTokens: '今日输出',
    todayTokens: '今日 Tokens',
    todayCacheCreation: '今日缓存创建',
    todayCacheRead: '今日缓存读取',
    todayCost: '今日费用',
    rpmTpm: 'RPM / TPM',
    totalRequests: '累计请求',
    totalInputTokens: '累计输入',
    totalOutputTokens: '累计输出',
    totalTokensLabel: '累计 Tokens',
    totalCacheCreation: '累计缓存创建',
    totalCacheRead: '累计缓存读取',
    totalCost: '累计费用',
    avgDuration: '平均耗时',
    // Messages
    enterApiKey: '请输入 API Key',
    querySuccess: '查询成功',
    queryFailed: '查询失败',
    queryFailedRetry: '查询失败，请稍后重试',
    noDailyUsage: '暂无按日用量数据',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API 安装向导',
    description: '配置您的 Sub2API 实例',
    database: {
      title: '数据库配置',
      description: '连接到您的 PostgreSQL 数据库',
      host: '主机',
      port: '端口',
      username: '用户名',
      password: '密码',
      databaseName: '数据库名称',
      sslMode: 'SSL 模式',
      passwordPlaceholder: '密码',
      ssl: {
        disable: '禁用',
        require: '要求',
        verifyCa: '验证 CA',
        verifyFull: '完全验证'
      }
    },
    redis: {
      title: 'Redis 配置',
      description: '连接到您的 Redis 服务器',
      host: '主机',
      port: '端口',
      username: '用户名（可选）',
      password: '密码（可选）',
      database: '数据库',
      usernamePlaceholder: '默认用户留空',
      passwordPlaceholder: '密码',
      enableTls: '启用 TLS',
      enableTlsHint: '连接 Redis 时使用 TLS（公共 CA 证书）'
    },
    admin: {
      title: '管理员账户',
      description: '创建您的管理员账户',
      email: '邮箱',
      password: '密码',
      confirmPassword: '确认密码',
      passwordPlaceholder: '至少 8 个字符',
      confirmPasswordPlaceholder: '确认密码',
      passwordMismatch: '密码不匹配'
    },
    ready: {
      title: '准备安装',
      description: '检查您的配置并完成安装',
      database: '数据库',
      redis: 'Redis',
      adminEmail: '管理员邮箱'
    },
    status: {
      testing: '测试中...',
      success: '连接成功',
      testConnection: '测试连接',
      installing: '安装中...',
      completeInstallation: '完成安装',
      completed: '安装完成！',
      redirecting: '正在跳转到登录页面...',
      restarting: '服务正在重启，请稍候...',
      timeout: '服务重启时间超出预期，请手动刷新页面。'
    }
  },

  // Common
}
