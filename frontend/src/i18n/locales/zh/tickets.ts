export default {
  // 侧边栏导航增补键：与 common.nav 合并（见 locales/zh/index.ts）。
  nav: {
    tickets: '工单',
    ticketManagement: '工单管理'
  },

  tickets: {
    title: '我的工单',
    description: '提交问题、跟踪处理进度，并通过邮件接收客服回复通知。',
    newTicket: '新建工单',
    loadFailed: '加载工单列表失败',
    replyCount: '{count} 条回复',
    lastReplyAt: '最近回复',
    createdAt: '创建于 {time}',
    noReplyYet: '暂无回复',
    viewDetail: '查看详情',
    filterStatus: '状态筛选',
    allStatus: '全部状态',
    statusLabels: {
      open: '待处理',
      pending: '待您回复',
      resolved: '已解决',
      closed: '已关闭'
    },
    priorityLabels: {
      low: '低',
      normal: '普通',
      high: '高',
      urgent: '紧急'
    },
    categoryLabels: {
      general: '通用',
      billing: '账单',
      api: 'API',
      account: '账号',
      other: '其他'
    },
    empty: {
      title: '还没有工单',
      description: '遇到问题？提交一张工单，我们会尽快回复。'
    },
    create: {
      title: '新建工单',
      subtitle: '请尽量描述清楚问题，便于我们快速定位。',
      subject: '标题',
      subjectPlaceholder: '一句话描述你的问题',
      category: '分类',
      priority: '优先级',
      content: '问题描述',
      contentPlaceholder: '请描述问题现象、复现步骤与期望结果…',
      submit: '提交工单',
      submitting: '提交中…',
      success: '工单已提交',
      failed: '提交工单失败',
      subjectRequired: '请填写工单标题',
      subjectTooLong: '标题不能超过 {max} 个字符',
      contentRequired: '请填写问题描述',
      contentTooLong: '内容不能超过 {max} 个字符'
    },
    detail: {
      backToList: '返回工单列表',
      loadFailed: '加载工单详情失败',
      notFound: '工单不存在或无权访问',
      ticketId: '工单号',
      replies: '对话',
      emptyReplies: '还没有回复，客服稍后会在这里回复。',
      authorUser: '用户',
      authorSupport: '客服',
      emailNotified: '已通过邮件通知',
      closedNotice: '该工单已关闭，无法继续回复；如有新问题请新建工单。',
      replyPlaceholder: '输入回复内容…',
      replySubmit: '发送回复',
      replySubmitting: '发送中…',
      replySuccess: '回复已发送',
      replyFailed: '发送回复失败',
      replyRequired: '请输入回复内容',
      replyTooLong: '回复不能超过 {max} 个字符',
      close: '关闭工单',
      closeConfirmTitle: '关闭工单',
      closeConfirmMessage: '关闭后将无法继续回复，确定要关闭该工单吗？',
      closeSuccess: '工单已关闭',
      closeFailed: '关闭工单失败'
    },
    attachments: {
      label: '图片附件',
      dropHint: '拖拽图片到此处，或点击选择',
      pasteHint: '也可以直接粘贴截图（Ctrl+V）',
      select: '选择图片',
      countHint: '已添加 {count}/{max} 张',
      limitReached: '最多只能添加 {max} 张图片',
      tooLarge: '{name} 超过 {max} 大小限制',
      typeNotAllowed: '{name} 格式不支持，仅支持 PNG、JPEG、WebP、GIF',
      emptyFile: '{name} 是空文件，无法上传',
      unnamedFile: '未命名图片',
      uploadInProgress: '图片还在上传中，请稍候再提交',
      uploadFailed: '图片上传失败，请重试',
      deleteFailed: '删除图片失败',
      retry: '重试',
      remove: '删除',
      viewOriginal: '查看原图',
      openOriginal: '在新窗口打开',
      closePreview: '关闭预览',
      errors: {
        TICKET_ATTACHMENT_TOO_LARGE: '图片超过 5MB 大小限制',
        TICKET_ATTACHMENT_TYPE_NOT_ALLOWED: '图片格式不支持，仅支持 PNG、JPEG、WebP、GIF',
        TICKET_ATTACHMENT_DIMENSION_EXCEEDED: '图片尺寸超出限制（宽高需不超过 8000 像素）',
        TICKET_ATTACHMENT_DAILY_QUOTA_EXCEEDED: '今日上传图片数量已达上限，请明天再试',
        TICKET_ATTACHMENT_NOT_FOUND: '附件不存在或已被清理',
        TICKET_ATTACHMENT_ALREADY_BOUND: '该附件已绑定到工单，无法删除'
      }
    }
  }
}