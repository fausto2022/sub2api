import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'

import TicketAttachmentUploader from '../TicketAttachmentUploader.vue'
import {
  TICKET_ATTACHMENT_MAX_BYTES,
  deleteTicketAttachment,
  uploadTicketAttachment,
  type TicketAttachment
} from '@/api/tickets'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      // 测试里把所有 `tickets.*` 键都视为“存在”，与真实 i18n 的 te() 行为对齐。
      te: (key: string) => key.startsWith('tickets.')
    })
  }
})

vi.mock('@/api/tickets', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/tickets')>()
  return {
    ...actual,
    uploadTicketAttachment: vi.fn(),
    deleteTicketAttachment: vi.fn()
  }
})

const mockedUpload = vi.mocked(uploadTicketAttachment)
const mockedDelete = vi.mocked(deleteTicketAttachment)

let wrapper: VueWrapper | null = null
let nextAttachmentId = 1

function makeAttachment(id: number, name = `img-${id}.png`): TicketAttachment {
  return {
    id,
    url: `/api/v1/tickets/attachments/${id}`,
    original_name: name,
    content_type: 'image/png',
    byte_size: 1024,
    width: 640,
    height: 480
  }
}

/** jsdom 的 File.size 由内容推导，这里覆写成测试需要的体积。 */
function makeFile(name = 'shot.png', type = 'image/png', size = 1024): File {
  const file = new File([new Uint8Array(Math.min(size, 32))], name, { type })
  Object.defineProperty(file, 'size', { value: size, configurable: true })
  return file
}

async function selectFiles(input: DOMWrapper<Element>, files: File[]): Promise<void> {
  Object.defineProperty(input.element, 'files', { value: files, configurable: true })
  await input.trigger('change')
}

function mountUploader(): VueWrapper {
  wrapper = mount(TicketAttachmentUploader)
  return wrapper
}

beforeEach(() => {
  nextAttachmentId = 1
  mockedUpload.mockReset()
  mockedDelete.mockReset()
  mockedUpload.mockImplementation(async () => makeAttachment(nextAttachmentId++))
  mockedDelete.mockResolvedValue(undefined)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

describe('TicketAttachmentUploader', () => {
  it('renders the three upload entry points (click zone, file input, paste hint)', () => {
    const vm = mountUploader()

    const zone = vm.get('[data-testid="ticket-attachment-dropzone"]')
    const input = vm.get('input[type="file"]')

    expect(zone.attributes('role')).toBe('button')
    expect(input.attributes('accept')).toBe('image/png,image/jpeg,image/webp,image/gif')
    expect(input.attributes('multiple')).toBeDefined()
    expect(vm.text()).toContain('tickets.attachments.pasteHint')
    expect(vm.text()).toContain('tickets.attachments.dropHint')
  })

  it('uploads a file picked through the file input and renders the thumbnail', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [makeFile('picked.png')])
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(1)
    expect(mockedUpload.mock.calls[0][0].name).toBe('picked.png')

    const thumb = vm.get('[data-testid="ticket-attachment-thumb"]')
    expect(thumb.attributes('src')).toBe('/api/v1/tickets/attachments/1')
    expect(thumb.attributes('alt')).toBe('picked.png')
    expect(vm.emitted('update:modelValue')?.at(-1)).toEqual([[1]])
  })

  it('uploads files dropped onto the dropzone', async () => {
    const vm = mountUploader()
    const zone = vm.get('[data-testid="ticket-attachment-dropzone"]')

    const event = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'dataTransfer', { value: { files: [makeFile('dropped.png')] } })
    zone.element.dispatchEvent(event)
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(1)
    expect(mockedUpload.mock.calls[0][0].name).toBe('dropped.png')
    expect(vm.findAll('[data-testid="ticket-attachment-thumb"]')).toHaveLength(1)
  })

  it('uploads an image pasted from the clipboard (Ctrl+V)', async () => {
    mountUploader()

    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', { value: { files: [makeFile('paste.png')] } })
    document.dispatchEvent(paste)
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(1)
    expect(mockedUpload.mock.calls[0][0].name).toBe('paste.png')
    expect(paste.defaultPrevented).toBe(true)
  })

  it('ignores plain-text paste without files', async () => {
    mountUploader()

    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', { value: { files: [] } })
    document.dispatchEvent(paste)
    await flushPromises()

    expect(mockedUpload).not.toHaveBeenCalled()
    expect(paste.defaultPrevented).toBe(false)
  })

  it('rejects files beyond the 3-image limit and disables the entry point', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [
      makeFile('a.png'),
      makeFile('b.png'),
      makeFile('c.png')
    ])
    await flushPromises()
    expect(mockedUpload).toHaveBeenCalledTimes(3)

    const zone = vm.get('[data-testid="ticket-attachment-dropzone"]')
    expect(zone.attributes('aria-disabled')).toBe('true')
    expect(zone.attributes('tabindex')).toBe('-1')
    expect(vm.get('input[type="file"]').attributes('disabled')).toBeDefined()

    // 已达上限后再拖入第 4 张：明确提示且不上传。
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [makeFile('d.png')] } })
    zone.element.dispatchEvent(drop)
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(3)
    expect(vm.text()).toContain('tickets.attachments.limitReached')
    expect(vm.get('[data-testid="ticket-attachment-error"]').text()).toContain('"max":3')
    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(3)
  })

  it('rejects files larger than 5MB', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [
      makeFile('huge.png', 'image/png', TICKET_ATTACHMENT_MAX_BYTES + 1)
    ])
    await flushPromises()

    expect(mockedUpload).not.toHaveBeenCalled()
    const error = vm.get('[data-testid="ticket-attachment-error"]').text()
    expect(error).toContain('tickets.attachments.tooLarge')
    expect(error).toContain('"max":"5 MB"')
    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(0)
  })

  it('accepts a file of exactly 5MB (client check is a fast pre-filter)', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [
      makeFile('exact.png', 'image/png', TICKET_ATTACHMENT_MAX_BYTES)
    ])
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(1)
    expect(vm.findAll('[data-testid="ticket-attachment-thumb"]')).toHaveLength(1)
  })

  it('rejects unsupported formats (text and SVG)', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [
      makeFile('notes.txt', 'text/plain'),
      makeFile('vector.svg', 'image/svg+xml')
    ])
    await flushPromises()

    expect(mockedUpload).not.toHaveBeenCalled()
    const errors = vm.findAll('[data-testid="ticket-attachment-error"]')
    expect(errors).toHaveLength(2)
    expect(errors[0].text()).toContain('tickets.attachments.typeNotAllowed')
    expect(errors[0].text()).toContain('notes.txt')
    expect(errors[1].text()).toContain('vector.svg')
  })

  it('infers the type from the extension when the browser reports no MIME type', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [makeFile('clipboard-image.png', '')])
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(1)
    expect(vm.findAll('[data-testid="ticket-attachment-thumb"]')).toHaveLength(1)
  })

  it('shows upload progress while the request is in flight', async () => {
    const vm = mountUploader()
    let release: () => void = () => {}
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })

    mockedUpload.mockImplementation(async (_file, options) => {
      options?.onProgress?.(42)
      await gate
      return makeAttachment(9)
    })

    await selectFiles(vm.get('input[type="file"]'), [makeFile('slow.png')])
    await vm.vm.$nextTick()

    const progress = vm.get('[data-testid="ticket-attachment-progress"]')
    expect(progress.text()).toContain('42%')
    expect(vm.emitted('update:pending')?.at(-1)).toEqual([true])

    release()
    await flushPromises()

    expect(vm.find('[data-testid="ticket-attachment-progress"]').exists()).toBe(false)
    expect(vm.findAll('[data-testid="ticket-attachment-thumb"]')).toHaveLength(1)
    expect(vm.emitted('update:pending')?.at(-1)).toEqual([false])
  })

  it('marks failed uploads with a retry action and retries successfully', async () => {
    const vm = mountUploader()
    mockedUpload.mockRejectedValueOnce({
      status: 413,
      code: 'TICKET_ATTACHMENT_TOO_LARGE',
      message: 'payload too large'
    })

    await selectFiles(vm.get('input[type="file"]'), [makeFile('fail.png')])
    await flushPromises()

    expect(vm.get('[data-testid="ticket-attachment-item"]').attributes('data-status')).toBe('error')
    expect(vm.get('[data-testid="ticket-attachment-retry"]').text()).toContain('tickets.attachments.retry')
    expect(vm.text()).toContain('tickets.attachments.errors.TICKET_ATTACHMENT_TOO_LARGE')

    await vm.get('[data-testid="ticket-attachment-retry"]').trigger('click')
    await flushPromises()

    expect(mockedUpload).toHaveBeenCalledTimes(2)
    expect(vm.get('[data-testid="ticket-attachment-item"]').attributes('data-status')).toBe('success')
    expect(vm.findAll('[data-testid="ticket-attachment-thumb"]')).toHaveLength(1)
  })

  it('falls back to the server message when the error code is unknown', async () => {
    const vm = mountUploader()
    mockedUpload.mockRejectedValueOnce({ status: 500, message: 'boom from server' })

    await selectFiles(vm.get('input[type="file"]'), [makeFile('oops.png')])
    await flushPromises()

    expect(vm.get('[data-testid="ticket-attachment-error"]').text()).toBe('boom from server')
  })

  it('deletes an uploaded attachment and emits the remaining ids', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [makeFile('a.png'), makeFile('b.png')])
    await flushPromises()
    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(2)

    await vm.get('[data-testid="ticket-attachment-remove"]').trigger('click')
    await flushPromises()

    expect(mockedDelete).toHaveBeenCalledWith(1)
    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(1)
    expect(vm.emitted('update:modelValue')?.at(-1)).toEqual([[2]])
  })

  it('keeps the UI usable when the server already bound the deleted attachment', async () => {
    const vm = mountUploader()
    mockedDelete.mockRejectedValueOnce({ status: 409, code: 'TICKET_ATTACHMENT_ALREADY_BOUND' })

    await selectFiles(vm.get('input[type="file"]'), [makeFile('bound.png')])
    await flushPromises()

    await vm.get('[data-testid="ticket-attachment-remove"]').trigger('click')
    await flushPromises()

    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(0)
    expect(vm.find('[data-testid="ticket-attachment-error"]').exists()).toBe(false)
    expect(vm.emitted('update:modelValue')?.at(-1)).toEqual([[]])
  })

  it('clears every attachment after reset()', async () => {
    const vm = mountUploader()

    await selectFiles(vm.get('input[type="file"]'), [makeFile('a.png')])
    await flushPromises()
    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(1)

    ;(vm.vm as unknown as { reset: () => void }).reset()
    await flushPromises()

    expect(vm.findAll('[data-testid="ticket-attachment-item"]')).toHaveLength(0)
    expect(vm.emitted('update:modelValue')?.at(-1)).toEqual([[]])
  })
})
