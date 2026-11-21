export default {
  opsmgmt: {
    title: '运营管理',
    description: '上游对接、扩池（对接渠道）与利润保护',
    refresh: '刷新',
    loading: '加载中…',
    cancel: '取消',
    save: '保存',
    saved: '已保存',
    saving: '保存中…',
    close: '关闭',
    retry: '重试',
    updatedAt: '更新于 {time}',
    notAvailable: '数据不可用',

    nav: {
      group: '运营管理',
      dashboard: '运营看板',
      sites: '上游站点',
      expand: '对接渠道',
      profit: '利润管理'
    },

    kpi: {
      trendUp: '上涨',
      trendDown: '下降',
      trendFlat: '持平',
      compareSevenDay: '较 7 日均值'
    },

    rate: {
      up: '上涨',
      down: '下降',
      flat: '持平'
    },

    source: {
      opsmgmt: '运营管理',
      manual: '手工创建',
      column: '来源',
      filterLabel: '按来源筛选',
      filterAll: '全部来源',
      manualHint: '该对象由管理员手工创建，未关联上游站点',
      site: '上游站点',
      siteId: '站点 ID',
      remoteGroup: '上游分组',
      disabledReason: '停用原因',
      disabledAt: '停用时间',
      reason: {
        margin: '利润率不足'
      }
    },

    dashboard: {
      title: '运营看板',
      description: '今日盈利、保底收入与分组盈利榜',
      range: {
        today: '今日',
        sevenDay: '近 7 日'
      },
      kpi: {
        revenue: '今日收入',
        cost: '今日成本',
        profit: '今日利润',
        margin: '利润率',
        guaranteed: '保底 {amount}（{percent}%）'
      },
      unavailable: {
        title: '成本数据不可用（上游未提供）',
        reason: '原因：{reason}',
        hint: '看板聚合失败时不展示 0，避免把「查不到」误读成「没赚钱」'
      },
      trend: {
        title: '近 7 日收入 / 成本 / 利润',
        subtitle: '收入为 actual_cost，成本为 total_cost，利润 = 收入 − 成本',
        revenue: '收入',
        cost: '成本',
        profit: '利润',
        empty: '暂无趋势数据',
        aria: '{from} 至 {to}：收入 {revenue}，成本 {cost}，利润 {profit}',
        derived: '逐日明细不可用，当前按「今日 / 近 7 日」聚合值绘制'
      },
      groups: {
        title: '分组盈利榜',
        empty: '近 7 日暂无分组用量',
        group: '分组',
        revenue: '收入',
        cost: '成本',
        profit: '利润',
        margin: '利润率',
        requests: '请求数',
        lossBadge: '亏损',
        sampled: '有效天数 {days} / 7'
      }
    },

    sites: {
      title: '上游站点',
      description: '用上游账号密码登录后同步分组与倍率',
      create: '新增站点',
      empty: '还没有上游站点',
      selectHint: '选择左侧站点查看分组与账号快照',
      form: {
        createTitle: '新增上游站点',
        editTitle: '编辑上游站点',
        name: '站点名称',
        namePlaceholder: '例如 api.example.com',
        baseUrl: '上游地址',
        baseUrlPlaceholder: 'https://api.example.com',
        username: '登录邮箱',
        usernamePlaceholder: '上游账号邮箱',
        password: '登录密码',
        passwordKeep: '留空表示不修改密码',
        upstreamApiKey: '上游 API Key（可选）',
        upstreamApiKeyHint: '该站点默认使用的普通用户 Key；扩池时可覆盖',
        clearUpstreamApiKey: '清空已保存的上游 API Key',
        syncInterval: '同步间隔（分钟）',
        nameRequired: '请填写站点名称',
        baseUrlRequired: '请填写上游地址',
        usernameRequired: '请填写登录邮箱',
        passwordRequired: '新增站点必须填写登录密码'
      },
      list: {
        name: '站点',
        baseUrl: '地址',
        status: '状态',
        lastSync: '上次同步',
        actions: '操作'
      },
      status: {
        active: '正常',
        error: '同步失败'
      },
      action: {
        detail: '详情',
        sync: '同步',
        test: '登录测试',
        edit: '编辑',
        delete: '删除'
      },
      deleteConfirm: '删除站点「{name}」？已对接的渠道不会被删除，但会失去上游标签。',
      testSuccess: '登录成功：分组 {groups} 个，耗时 {latency}ms',
      testFailure: '登录失败：{message}',
      syncQueued: '已提交同步任务：{id}',
      syncRunning: '同步中…',
      syncDone: '同步完成：分组 {groups} 个、账号 {accounts} 个',
      syncFailed: '同步失败：{message}',
      detail: {
        statusLine: '状态：{status} · 上次同步 {synced} · 每 {interval} 分钟同步',
        groupsTitle: '分组列表',
        groupsEmpty: '还没有分组快照，请先同步',
        groupName: '分组',
        platform: '平台',
        rate: '倍率',
        rateChanged: '倍率变动',
        minMargin: '上游最低利润率',
        accountsTitle: '账号列表',
        accountsEmpty: '当前登录方式不返回上游账号（账号密码模式下为空）',
        accountName: '账号',
        accountStatus: '状态',
        accountRate: '倍率',
        accountSource: '来源'
      }
    },

    expand: {
      title: '对接渠道',
      description: '把上游分组对接成本地转发渠道（扩池的新语义）',
      step: {
        site: '选择上游站点',
        groups: '选择上游分组',
        key: '填写上游 API Key',
        target: '选择本地目标分组',
        submit: '确认并创建渠道'
      },
      sitePlaceholder: '请选择上游站点',
      groupsEmpty: '该站点还没有分组快照',
      groupsSelected: '已选 {count} 个上游分组',
      keyLabel: '上游 API Key',
      keyPlaceholder: '上游为这些分组开出的普通 Key',
      keyHint: '只加密保存到本地渠道，不会回显；批量对接时所有分组共用该 Key',
      targetLabel: '本地目标分组',
      targetPlaceholder: '请选择本地分组',
      targetHint: '新渠道会绑定到该分组',
      countLabel: '数量',
      countHint: '本次将创建 {count} 个渠道（每个上游分组一个）',
      channelPrefix: '渠道名前缀',
      channelPrefixHint: '留空则用 ups-{site}-{group}',
      submit: '创建渠道',
      submitting: '创建中…',
      taskQueued: '已提交批量任务：{id}',
      progress: {
        title: '任务进度',
        processed: '已处理 {done} / {total}',
        status: {
          pending: '排队中',
          running: '执行中',
          succeeded: '成功',
          failed: '失败'
        }
      },
      result: {
        title: '结果统计',
        empty: '还没有对接记录',
        created: '成功',
        skipped: '跳过',
        failed: '失败',
        item: '上游分组 {ref}',
        viewChannels: '前往渠道列表'
      },
      errors: {
        noSite: '请选择上游站点',
        noGroups: '请至少选择一个上游分组',
        noKey: '请填写上游 API Key',
        noTarget: '请选择本地目标分组',
        submitFailed: '对接失败：{message}',
        noGroupsSynced: '请先同步该站点后再对接'
      }
    },

    profit: {
      title: '利润管理',
      description: '分组级最低利润率、倍率变动自动停用与恢复',
      status: {
        healthy: '健康',
        risk: '风险',
        unsupported: '不支持',
        unknown: '未知'
      },
      settings: {
        title: '全局开关',
        autoProtection: '自动利润保护',
        autoProtectionHint: '关闭时只记录风险并告警（灰度观察），不真正停用渠道',
        autoRecovery: '自动恢复',
        autoRecoveryHint: '利润率恢复健康后自动解除停用',
        minMargin: '最低利润率（%）',
        minMarginHint: '低于该利润率判定为风险；分组可单独覆盖',
        riskConfirmations: '确认次数',
        riskConfirmationsHint: '连续 N 次评估为风险才停用（防抖动）',
        costMaxAge: '成本时效（分钟）',
        guaranteedRatio: '保底收入比例（%）',
        guaranteedRatioHint: '看板「保底收入」按该比例计算',
        syncInterval: '同步间隔（分钟）',
        profitWindowHours: '利润评估窗口（小时）',
        save: '保存设置',
        saved: '设置已保存',
        grayHint: '当前为灰度模式：自动利润保护关闭，只会告警不会停用。'
      },
      pending: {
        title: '待处理（需要人工确认）',
        empty: '没有处于风险状态的利润检查',
        group: '分组',
        rateChange: '倍率变化',
        margin: '利润率',
        status: '状态',
        impact: '影响',
        evaluate: '立即评估',
        evaluating: '评估中…',
        evaluateQueued: '已提交评估任务：{id}',
        onlyWarn: '仅告警',
        autoDisabled: '已自动停用'
      },
      disabled: {
        title: '已自动停用（可一键恢复）',
        empty: '没有被运营管理停用的渠道',
        channel: '渠道',
        site: '上游站点',
        reason: '停用原因',
        at: '停用时间',
        actions: '操作',
        restore: '恢复',
        restoring: '恢复中…',
        restoreSuccess: '已恢复渠道「{name}」'
      },
      history: {
        title: '变更历史',
        empty: '还没有利润检查记录',
        time: '时间',
        group: '分组',
        margin: '利润率',
        status: '状态',
        multiplier: '倍率',
        reliable: '成本可信',
        unreliable: '成本不可信'
      },
      margins: {
        title: '分组最低利润率',
        empty: '没有分组',
        group: '分组',
        own: '分组配置',
        effective: '生效值',
        source: '来源',
        inherited: '继承',
        sourceGroup: '分组',
        sourceUpstream: '上游分组',
        sourceGlobal: '全局'
      }
    },

    groupMargin: {
      title: '运营管理 · 利润保护',
      description: '低于最低利润率时，该分组的渠道将在连续评估后被自动停用',
      minMargin: '最低利润率',
      inputHint: '百分比输入，0–99；留空按继承全局处理',
      inherit: '继承全局设置',
      inheritHint: '继承全局：{value}',
      inheritHintUnknown: '继承全局设置（全局值读取中）',
      rangeError: '最低利润率需在 0%–99% 之间',
      saveFailed: '保存最低利润率失败',
      loadFailed: '读取最低利润率失败',
      source: {
        group: '来源：分组配置',
        upstream: '来源：上游分组',
        global: '来源：全局设置'
      }
    }
  },
  nav: {
    opsMgmt: '运营管理',
    opsMgmtDashboard: '运营看板',
    opsMgmtSites: '上游站点',
    opsMgmtExpand: '对接渠道',
    opsMgmtProfit: '利润管理'
  }
}
