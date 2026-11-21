export default {
  // Sidebar navigation additions, merged into common.nav (see locales/en/index.ts).
  nav: {
    tickets: 'Tickets',
    ticketManagement: 'Ticket Management'
  },

  tickets: {
    title: 'My Tickets',
    description: 'Report an issue, follow its progress, and get support replies by email.',
    newTicket: 'New Ticket',
    loadFailed: 'Failed to load tickets',
    replyCount: '{count} replies',
    lastReplyAt: 'Last reply',
    createdAt: 'Created {time}',
    noReplyYet: 'No replies yet',
    viewDetail: 'View details',
    filterStatus: 'Status filter',
    allStatus: 'All statuses',
    statusLabels: {
      open: 'Open',
      pending: 'Awaiting you',
      resolved: 'Resolved',
      closed: 'Closed'
    },
    priorityLabels: {
      low: 'Low',
      normal: 'Normal',
      high: 'High',
      urgent: 'Urgent'
    },
    categoryLabels: {
      general: 'General',
      billing: 'Billing',
      api: 'API',
      account: 'Account',
      other: 'Other'
    },
    empty: {
      title: 'No tickets yet',
      description: 'Ran into a problem? Open a ticket and we will get back to you soon.'
    },
    create: {
      title: 'New Ticket',
      subtitle: 'Describe the issue in detail so we can help faster.',
      subject: 'Subject',
      subjectPlaceholder: 'Summarize your issue in one line',
      category: 'Category',
      priority: 'Priority',
      content: 'Description',
      contentPlaceholder: 'Describe the symptom, steps to reproduce and what you expected…',
      submit: 'Submit ticket',
      submitting: 'Submitting…',
      success: 'Ticket submitted',
      failed: 'Failed to submit ticket',
      subjectRequired: 'Please enter a subject',
      subjectTooLong: 'Subject cannot exceed {max} characters',
      contentRequired: 'Please describe the issue',
      contentTooLong: 'Description cannot exceed {max} characters'
    },
    detail: {
      backToList: 'Back to tickets',
      loadFailed: 'Failed to load ticket',
      notFound: 'This ticket does not exist or you are not allowed to view it',
      ticketId: 'Ticket ID',
      replies: 'Conversation',
      emptyReplies: 'No replies yet — support will answer here.',
      authorUser: 'User',
      authorSupport: 'Support',
      emailNotified: 'Notified by email',
      closedNotice: 'This ticket is closed and can no longer receive replies. Open a new ticket for new issues.',
      replyPlaceholder: 'Write a reply…',
      replySubmit: 'Send reply',
      replySubmitting: 'Sending…',
      replySuccess: 'Reply sent',
      replyFailed: 'Failed to send reply',
      replyRequired: 'Please enter a reply',
      replyTooLong: 'Reply cannot exceed {max} characters',
      close: 'Close ticket',
      closeConfirmTitle: 'Close ticket',
      closeConfirmMessage: 'Once closed, replies are no longer possible. Close this ticket?',
      closeSuccess: 'Ticket closed',
      closeFailed: 'Failed to close ticket'
    },
    attachments: {
      label: 'Image attachments',
      dropHint: 'Drag images here, or click to choose',
      pasteHint: 'You can also paste a screenshot (Ctrl+V)',
      select: 'Choose images',
      countHint: '{count}/{max} images added',
      limitReached: 'You can add at most {max} images',
      tooLarge: '{name} exceeds the {max} size limit',
      typeNotAllowed: '{name} is not supported. Only PNG, JPEG, WebP and GIF are allowed',
      emptyFile: '{name} is empty and cannot be uploaded',
      unnamedFile: 'Unnamed image',
      uploadInProgress: 'Images are still uploading. Please wait before submitting',
      uploadFailed: 'Image upload failed. Please try again',
      deleteFailed: 'Failed to delete the image',
      retry: 'Retry',
      remove: 'Remove',
      viewOriginal: 'View full size',
      openOriginal: 'Open in new window',
      closePreview: 'Close preview',
      errors: {
        TICKET_ATTACHMENT_TOO_LARGE: 'The image exceeds the 5MB size limit',
        TICKET_ATTACHMENT_TYPE_NOT_ALLOWED: 'This image type is not supported. Only PNG, JPEG, WebP and GIF are allowed',
        TICKET_ATTACHMENT_DIMENSION_EXCEEDED: 'The image dimensions exceed the limit (width and height must be 8000 pixels or less)',
        TICKET_ATTACHMENT_DAILY_QUOTA_EXCEEDED: 'Daily image upload quota reached. Please try again tomorrow',
        TICKET_ATTACHMENT_NOT_FOUND: 'The attachment does not exist or has been cleaned up',
        TICKET_ATTACHMENT_ALREADY_BOUND: 'This attachment is already bound to a ticket and cannot be deleted'
      }
    }
  }
}