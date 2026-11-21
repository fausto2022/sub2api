export default {
  tickets: {
    title: 'Ticket Management',
    description: 'Review and answer user tickets. Replies can notify the user by email; status and priority are editable.',
    searchPlaceholder: 'Search subject / content / user email',
    filterStatus: 'Status',
    filterPriority: 'Priority',
    allStatus: 'All statuses',
    allPriority: 'All priorities',
    stats: {
      open: 'Open',
      pending: 'Awaiting user',
      resolved: 'Resolved',
      closed: 'Closed',
      total: 'Total'
    },
    statsLoadFailed: 'Failed to load ticket stats',
    loadFailed: 'Failed to load tickets',
    empty: {
      title: 'No tickets',
      description: 'No tickets match the current filters.'
    },
    columns: {
      id: 'ID',
      subject: 'Ticket',
      user: 'User',
      status: 'Status',
      priority: 'Priority',
      replies: 'Replies',
      updatedAt: 'Updated',
      actions: 'Actions'
    },
    view: 'View',
    unknownUser: 'Unknown user',
    detailTitle: 'Ticket details',
    detailLoadFailed: 'Failed to load ticket details',
    submitter: 'Submitter',
    createdAt: 'Created',
    closedHint: 'This ticket is closed. Change its status back to Open before replying.',
    statusLabel: 'Status',
    priorityLabel: 'Priority',
    saveChanges: 'Save changes',
    saving: 'Saving…',
    updateSuccess: 'Ticket updated',
    updateFailed: 'Failed to update ticket',
    replyTitle: 'Reply to user',
    replyPlaceholder: 'Write a reply…',
    notifyEmail: 'Notify user by email',
    notifyEmailHint: 'When checked, the reply is emailed to the submitter; skipped when the email is missing or unverified.',
    sendReply: 'Send reply',
    sendingReply: 'Sending…',
    replySuccess: 'Reply sent',
    replyEmailSuccess: 'Reply sent and the user was notified by email',
    replyEmailFailed: 'Reply sent, but the email notification failed: {error}',
    replyFailed: 'Failed to send reply',
    replyContentRequired: 'Please enter a reply',
    replyContentTooLong: 'Reply cannot exceed {max} characters'
  }
}