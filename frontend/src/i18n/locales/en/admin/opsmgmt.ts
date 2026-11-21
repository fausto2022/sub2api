export default {
  opsmgmt: {
    title: 'Ops Management',
    description: 'Upstream connections, channel expansion and margin protection',
    refresh: 'Refresh',
    loading: 'Loading…',
    cancel: 'Cancel',
    save: 'Save',
    saved: 'Saved',
    saving: 'Saving…',
    close: 'Close',
    retry: 'Retry',
    updatedAt: 'Updated at {time}',
    notAvailable: 'Data unavailable',

    nav: {
      group: 'Ops Management',
      dashboard: 'Dashboard',
      sites: 'Upstream Sites',
      expand: 'Connect Channels',
      profit: 'Margin Management'
    },

    kpi: {
      trendUp: 'up',
      trendDown: 'down',
      trendFlat: 'flat',
      compareSevenDay: 'vs 7-day average'
    },

    rate: {
      up: 'up',
      down: 'down',
      flat: 'flat'
    },

    source: {
      opsmgmt: 'Ops managed',
      manual: 'Manual',
      column: 'Source',
      filterLabel: 'Filter by source',
      filterAll: 'All sources',
      manualHint: 'Created manually and not linked to an upstream site',
      site: 'Upstream site',
      siteId: 'Site ID',
      remoteGroup: 'Upstream group',
      disabledReason: 'Disabled reason',
      disabledAt: 'Disabled at',
      reason: {
        margin: 'Margin below threshold'
      }
    },

    dashboard: {
      title: 'Ops Dashboard',
      description: 'Today’s profit, guaranteed revenue and per-group profit ranking',
      range: {
        today: 'Today',
        sevenDay: 'Last 7 days'
      },
      kpi: {
        revenue: 'Today revenue',
        cost: 'Today cost',
        profit: 'Today profit',
        margin: 'Margin',
        guaranteed: 'Guaranteed {amount} ({percent}%)'
      },
      unavailable: {
        title: 'Cost data unavailable (upstream did not provide it)',
        reason: 'Reason: {reason}',
        hint: 'The dashboard never renders 0 on failure, so “unavailable” is not misread as “no revenue”'
      },
      trend: {
        title: 'Revenue / cost / profit over the last 7 days',
        subtitle: 'Revenue is actual_cost, cost is total_cost, profit = revenue − cost',
        revenue: 'Revenue',
        cost: 'Cost',
        profit: 'Profit',
        empty: 'No trend data yet',
        aria: '{from} to {to}: revenue {revenue}, cost {cost}, profit {profit}',
        derived: 'Daily breakdown unavailable; chart uses the today / 7-day aggregates'
      },
      groups: {
        title: 'Group profit ranking',
        empty: 'No per-group usage in the last 7 days',
        group: 'Group',
        revenue: 'Revenue',
        cost: 'Cost',
        profit: 'Profit',
        margin: 'Margin',
        requests: 'Requests',
        lossBadge: 'Loss',
        sampled: 'Sampled days {days} / 7'
      }
    },

    sites: {
      title: 'Upstream Sites',
      description: 'Sign in with the upstream account, then sync groups and rates',
      create: 'Add site',
      empty: 'No upstream site yet',
      selectHint: 'Pick a site on the left to inspect its groups and accounts',
      form: {
        createTitle: 'Add upstream site',
        editTitle: 'Edit upstream site',
        name: 'Site name',
        namePlaceholder: 'e.g. api.example.com',
        baseUrl: 'Upstream URL',
        baseUrlPlaceholder: 'https://api.example.com',
        username: 'Login email',
        usernamePlaceholder: 'Upstream account email',
        password: 'Login password',
        passwordKeep: 'Leave blank to keep the current password',
        upstreamApiKey: 'Upstream API key (optional)',
        upstreamApiKeyHint: 'Default user key for this site; can be overridden when connecting channels',
        clearUpstreamApiKey: 'Clear the saved upstream API key',
        syncInterval: 'Sync interval (minutes)',
        nameRequired: 'Site name is required',
        baseUrlRequired: 'Upstream URL is required',
        usernameRequired: 'Login email is required',
        passwordRequired: 'Password is required when creating a site'
      },
      list: {
        name: 'Site',
        baseUrl: 'URL',
        status: 'Status',
        lastSync: 'Last sync',
        actions: 'Actions'
      },
      status: {
        active: 'Healthy',
        error: 'Sync failed'
      },
      action: {
        detail: 'Details',
        sync: 'Sync',
        test: 'Test login',
        edit: 'Edit',
        delete: 'Delete'
      },
      deleteConfirm: 'Delete site “{name}”? Connected channels are kept but lose the upstream tag.',
      testSuccess: 'Login OK: {groups} groups in {latency}ms',
      testFailure: 'Login failed: {message}',
      syncQueued: 'Sync task queued: {id}',
      syncRunning: 'Syncing…',
      syncDone: 'Sync finished: {groups} groups, {accounts} accounts',
      syncFailed: 'Sync failed: {message}',
      detail: {
        statusLine: 'Status: {status} · last sync {synced} · every {interval} minutes',
        groupsTitle: 'Groups',
        groupsEmpty: 'No group snapshot yet, run a sync first',
        groupName: 'Group',
        platform: 'Platform',
        rate: 'Rate',
        rateChanged: 'Rate changed',
        minMargin: 'Upstream min margin',
        accountsTitle: 'Accounts',
        accountsEmpty: 'This login mode does not return upstream accounts (empty in password mode)',
        accountName: 'Account',
        accountStatus: 'Status',
        accountRate: 'Rate',
        accountSource: 'Source'
      }
    },

    expand: {
      title: 'Connect Channels',
      description: 'Turn an upstream group into a local forwarding channel (the new meaning of pool expansion)',
      step: {
        site: 'Choose upstream site',
        groups: 'Choose upstream groups',
        key: 'Provide upstream API key',
        target: 'Choose local target group',
        submit: 'Confirm and create'
      },
      sitePlaceholder: 'Select an upstream site',
      groupsEmpty: 'This site has no group snapshot yet',
      groupsSelected: '{count} upstream groups selected',
      keyLabel: 'Upstream API key',
      keyPlaceholder: 'Normal key issued by the upstream for these groups',
      keyHint: 'Stored encrypted on the local channel and never echoed; shared by all groups in a batch',
      targetLabel: 'Local target group',
      targetPlaceholder: 'Select a local group',
      targetHint: 'The new channel will be bound to this group',
      countLabel: 'Count',
      countHint: '{count} channels will be created (one per upstream group)',
      channelPrefix: 'Channel name prefix',
      channelPrefixHint: 'Leave empty to use ups-{site}-{group}',
      submit: 'Create channels',
      submitting: 'Creating…',
      taskQueued: 'Batch task queued: {id}',
      progress: {
        title: 'Task progress',
        processed: 'Processed {done} / {total}',
        status: {
          pending: 'Queued',
          running: 'Running',
          succeeded: 'Succeeded',
          failed: 'Failed'
        }
      },
      result: {
        title: 'Result summary',
        empty: 'No connection attempt yet',
        created: 'Created',
        skipped: 'Skipped',
        failed: 'Failed',
        item: 'Upstream group {ref}',
        viewChannels: 'Open channel list'
      },
      errors: {
        noSite: 'Select an upstream site first',
        noGroups: 'Select at least one upstream group',
        noKey: 'Upstream API key is required',
        noTarget: 'Select a local target group',
        submitFailed: 'Connection failed: {message}',
        noGroupsSynced: 'Sync this site before connecting channels'
      }
    },

    profit: {
      title: 'Margin Management',
      description: 'Per-group minimum margin, auto-disable on rate changes and recovery',
      status: {
        healthy: 'Healthy',
        risk: 'At risk',
        unsupported: 'Unsupported',
        unknown: 'Unknown'
      },
      settings: {
        title: 'Global switches',
        autoProtection: 'Automatic margin protection',
        autoProtectionHint: 'When off, risks are only recorded and warned (canary mode); nothing is disabled',
        autoRecovery: 'Automatic recovery',
        autoRecoveryHint: 'Re-enable a channel once its margin is healthy again',
        minMargin: 'Minimum margin (%)',
        minMarginHint: 'Below this margin is treated as a risk; groups may override it',
        riskConfirmations: 'Confirmations',
        riskConfirmationsHint: 'Disable only after N consecutive risky evaluations (anti-flapping)',
        costMaxAge: 'Cost freshness (minutes)',
        guaranteedRatio: 'Guaranteed revenue ratio (%)',
        guaranteedRatioHint: 'Used by the dashboard “guaranteed revenue” figure',
        syncInterval: 'Sync interval (minutes)',
        profitWindowHours: 'Profit window (hours)',
        save: 'Save settings',
        saved: 'Settings saved',
        grayHint: 'Canary mode: automatic margin protection is off, risks are only warned.'
      },
      pending: {
        title: 'Pending (needs confirmation)',
        empty: 'No profit check is at risk',
        group: 'Group',
        rateChange: 'Rate change',
        margin: 'Margin',
        status: 'Status',
        impact: 'Impact',
        evaluate: 'Evaluate now',
        evaluating: 'Evaluating…',
        evaluateQueued: 'Evaluation task queued: {id}',
        onlyWarn: 'Warn only',
        autoDisabled: 'Auto-disabled'
      },
      disabled: {
        title: 'Auto-disabled (one-click restore)',
        empty: 'No channel was disabled by ops management',
        channel: 'Channel',
        site: 'Upstream site',
        reason: 'Reason',
        at: 'Disabled at',
        actions: 'Actions',
        restore: 'Restore',
        restoring: 'Restoring…',
        restoreSuccess: 'Channel “{name}” restored'
      },
      history: {
        title: 'Change history',
        empty: 'No profit check recorded yet',
        time: 'Time',
        group: 'Group',
        margin: 'Margin',
        status: 'Status',
        multiplier: 'Multiplier',
        reliable: 'Cost reliable',
        unreliable: 'Cost unreliable'
      },
      margins: {
        title: 'Group minimum margins',
        empty: 'No group',
        group: 'Group',
        own: 'Group override',
        effective: 'Effective',
        source: 'Source',
        inherited: 'Inherited',
        sourceGroup: 'Group',
        sourceUpstream: 'Upstream group',
        sourceGlobal: 'Global'
      }
    },

    groupMargin: {
      title: 'Ops management · Margin protection',
      description: 'Below the minimum margin, this group’s channels get auto-disabled after consecutive evaluations',
      minMargin: 'Minimum margin',
      inputHint: 'Percentage input, 0–99; leave empty to inherit the global setting',
      inherit: 'Inherit global setting',
      inheritHint: 'Inherit global: {value}',
      inheritHintUnknown: 'Inherit global setting (loading the global value)',
      rangeError: 'Minimum margin must be between 0% and 99%',
      saveFailed: 'Failed to save the minimum margin',
      loadFailed: 'Failed to load the minimum margin',
      source: {
        group: 'Source: group override',
        upstream: 'Source: upstream group',
        global: 'Source: global setting'
      }
    }
  },
  nav: {
    opsMgmt: 'Ops Management',
    opsMgmtDashboard: 'Ops Dashboard',
    opsMgmtSites: 'Upstream Sites',
    opsMgmtExpand: 'Connect Channels',
    opsMgmtProfit: 'Margin Management'
  }
}
