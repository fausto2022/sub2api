export default {
  tickets: {
    title: '工单管理',
    description: '查看并回复用户工单；回复可勾选邮件通知，状态与优先级可随时调整。',
    searchPlaceholder: '搜索标题 / 内容 / 用户邮箱',
    filterStatus: '状态',
    filterPriority: '优先级',
    allStatus: '全部状态',
    allPriority: '全部优先级',
    stats: {
      open: '待处理',
      pending: '待用户回复',
      resolved: '已解决',
      closed: '已关闭',
      total: '总计'
    },
    statsLoadFailed: '加载工单统计失败',
    loadFailed: '加载工单列表失败',
    empty: {
      title: '暂无工单',
      description: '当前筛选条件下没有工单。'
    },
    columns: {
      id: 'ID',
      subject: '工单',
      user: '用户',
      status: '状态',
      priority: '优先级',
      replies: '回复数',
      updatedAt: '更新时间',
      actions: '操作'
    },
    view: '查看',
    unknownUser: '未知用户',
    detailTitle: '工单详情',
    detailLoadFailed: '加载工单详情失败',
    submitter: '提交人',
    createdAt: '创建时间',
    closedHint: '该工单已关闭，如需继续沟通请先把状态改回“待处理”。',
    statusLabel: '状态',
    priorityLabel: '优先级',
    saveChanges: '保存修改',
    saving: '保存中…',
    updateSuccess: '工单已更新',
    updateFailed: '更新工单失败',
    replyTitle: '回复用户',
    replyPlaceholder: '输入回复内容…',
    notifyEmail: '邮件通知用户',
    notifyEmailHint: '勾选后会用邮件把回复发送给提交人；邮箱缺失或未验证时不会发送。',
    sendReply: '发送回复',
    sendingReply: '发送中…',
    replySuccess: '回复已发送',
    replyEmailSuccess: '回复已发送，邮件通知成功',
    replyEmailFailed: '回复已发送，但邮件通知失败：{error}',
    replyFailed: '发送回复失败',
    replyContentRequired: '请输入回复内容',
    replyContentTooLong: '回复不能超过 {max} 个字符'
  }
}