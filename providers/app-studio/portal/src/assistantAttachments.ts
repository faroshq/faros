import type { ProjectAssistantAttachmentReceipt } from './types'

/** Client-side safety bound for the browser upload path. */
export const MAX_ASSISTANT_ATTACHMENT_BYTES = 8 << 20
export const MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES = 1 << 20
export const MAX_ASSISTANT_ATTACHMENTS_PER_TURN = 8
export const MAX_ASSISTANT_ATTACHMENT_AGGREGATE_BYTES = 20 << 20
/** Keep ordinary paste inline; larger content becomes a durable text receipt. */
export const ASSISTANT_LARGE_PASTE_BYTES = 10 << 10
export const ASSISTANT_IMAGE_ACCEPT = 'image/png,image/jpeg,image/webp'
export const ASSISTANT_TEXT_ACCEPT = '.md,.txt,text/plain,text/markdown'
export const ASSISTANT_ATTACHMENT_ACCEPT = `${ASSISTANT_IMAGE_ACCEPT},${ASSISTANT_TEXT_ACCEPT}`
export const ASSISTANT_ATTACHMENT_RESOLUTION_ERROR = 'Resolve attachment uploads before creating the project (retry or remove the failed attachment).'

const TEXT_FILE_EXTENSIONS = /\.(?:md|txt)$/iu
const IMAGE_FILE_EXTENSIONS = /\.(?:png|jpe?g|webp)$/iu
const SUPPORTED_IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp'])
const SUPPORTED_TEXT_TYPES = new Set(['text/plain', 'text/markdown'])
const FILENAME_FALLBACK_TYPES = new Set(['application/octet-stream', 'binary/octet-stream'])

export type AssistantAttachmentStatus = 'staged' | 'uploading' | 'ready' | 'error' | 'deleting'

export interface AssistantStagedAttachment {
  clientID: string
  file: File
  receipt?: ProjectAssistantAttachmentReceipt
  projectName?: string
  status: AssistantAttachmentStatus
  error?: string
  retryable?: boolean
  retryAction?: 'upload' | 'delete'
}

/**
 * Keep the attachment error banner derived from candidate state. A transient
 * upload error must not survive after that candidate succeeds or is removed,
 * while another failed candidate still keeps an actionable message visible.
 */
export function assistantAttachmentErrorMessage(
  attachments: readonly Pick<AssistantStagedAttachment, 'status' | 'error'>[],
): string {
  const detail = attachments.find((attachment) => attachment.status === 'error' && attachment.error?.trim())?.error?.trim()
  if (detail) return detail
  return attachments.some((attachment) => attachment.status === 'error')
    ? ASSISTANT_ATTACHMENT_RESOLUTION_ERROR
    : ''
}

/** A receipt is still usable only when its immutable content identity matches. */
export function assistantAttachmentReceiptsMatch(
  expected: Pick<ProjectAssistantAttachmentReceipt, 'id' | 'filename' | 'contentType' | 'sizeBytes' | 'sha256'>,
  actual: Pick<ProjectAssistantAttachmentReceipt, 'id' | 'filename' | 'contentType' | 'sizeBytes' | 'sha256'>,
): boolean {
  return expected.id === actual.id &&
    expected.filename === actual.filename &&
    expected.contentType === actual.contentType &&
    expected.sizeBytes === actual.sizeBytes &&
    expected.sha256.toLowerCase() === actual.sha256.toLowerCase()
}

/** Return ready client candidates whose provisional receipt is no longer listed. */
export function staleAssistantAttachmentClientIDs(
  candidates: readonly Pick<AssistantStagedAttachment, 'clientID' | 'receipt' | 'status'>[],
  listedReceipts: readonly ProjectAssistantAttachmentReceipt[],
): string[] {
  return candidates
    .filter((candidate) => candidate.status === 'ready' && candidate.receipt)
    .filter((candidate) => !listedReceipts.some((receipt) => assistantAttachmentReceiptsMatch(candidate.receipt!, receipt)))
    .map((candidate) => candidate.clientID)
}

/** Recognize only a precise attachment receipt failure as retryable recovery. */
export function isAssistantAttachmentReceiptUnavailableError(error: unknown): boolean {
  const record = error && typeof error === 'object' ? error as { status?: unknown } : undefined
  const status = typeof record?.status === 'number' ? record.status : undefined
  const message = error instanceof Error ? error.message : typeof error === 'string' ? error : ''
  return (status === 400 || status === 404 || status === 409) &&
    /attachment/i.test(message) &&
    /(expired|invalid|missing|no longer|not available|not found|stale|unavailable|does not exist|receipt)/i.test(message)
}

/** Project untrusted API data into the immutable receipt used by content parts. */
export function projectAssistantAttachmentReceipt(value: unknown): ProjectAssistantAttachmentReceipt | null {
  if (!value || typeof value !== 'object') return null
  const raw = value as Record<string, unknown>
  const id = typeof raw.id === 'string' ? raw.id.trim() : ''
  const filename = typeof raw.filename === 'string' ? raw.filename.trim() : ''
  const contentType = typeof raw.contentType === 'string' ? raw.contentType.trim().toLowerCase() : ''
  const sizeBytes = typeof raw.sizeBytes === 'number' && Number.isSafeInteger(raw.sizeBytes) ? raw.sizeBytes : -1
  const sha256 = typeof raw.sha256 === 'string' ? raw.sha256.trim().toLowerCase() : ''
  const createdAt = typeof raw.createdAt === 'string' ? raw.createdAt.trim() : ''
  const supportedContentType = contentType === 'image/png' || contentType === 'image/jpeg' || contentType === 'image/webp' || contentType === 'text/plain' || contentType === 'text/markdown'
  if (!id || !filename || !supportedContentType || sizeBytes < 0 || sizeBytes > MAX_ASSISTANT_ATTACHMENT_BYTES || (contentType.startsWith('text/') && sizeBytes > MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES) || !/^[a-f0-9]{64}$/u.test(sha256) || !createdAt) return null
  return { id, filename, contentType, sizeBytes, sha256, createdAt }
}

export function assistantAttachmentIsImage(file: Pick<File, 'name' | 'type'>): boolean {
  const contentType = file.type.trim().toLowerCase()
  return SUPPORTED_IMAGE_TYPES.has(contentType) || ((FILENAME_FALLBACK_TYPES.has(contentType) || !contentType) && IMAGE_FILE_EXTENSIONS.test(file.name.trim()))
}

export function assistantAttachmentIsText(file: Pick<File, 'name' | 'type'>): boolean {
  const contentType = file.type.trim().toLowerCase()
  return (SUPPORTED_TEXT_TYPES.has(contentType) || FILENAME_FALLBACK_TYPES.has(contentType) || !contentType) && TEXT_FILE_EXTENSIONS.test(file.name.trim())
}

export function assistantAttachmentIsSupported(file: Pick<File, 'name' | 'type'>): boolean {
  const contentType = file.type.trim().toLowerCase()
  if (contentType) {
    return assistantAttachmentIsImage(file) || assistantAttachmentIsText(file)
  }
  const filename = file.name.trim()
  return IMAGE_FILE_EXTENSIONS.test(filename) || TEXT_FILE_EXTENSIONS.test(filename)
}

export function assistantAttachmentMaxBytes(file: Pick<File, 'name' | 'type'>): number {
  return assistantAttachmentIsText(file) ? MAX_ASSISTANT_TEXT_ATTACHMENT_BYTES : MAX_ASSISTANT_ATTACHMENT_BYTES
}

export function assistantAttachmentPart(receipt: ProjectAssistantAttachmentReceipt) {
  return { type: 'attachment' as const, attachment: receipt }
}

export interface AssistantAttachmentCandidate {
  file?: Pick<File, 'size'>
  receipt?: Pick<ProjectAssistantAttachmentReceipt, 'sizeBytes'>
  status?: AssistantAttachmentStatus
}

/**
 * Apply the same browser-side checks to every attachment surface. Invalid
 * candidates are intentionally not counted against the turn limits so users
 * can correct them without first removing an error chip.
 */
export function assistantAttachmentValidationError(
  file: Pick<File, 'name' | 'type' | 'size'>,
  existing: readonly AssistantAttachmentCandidate[] = [],
): string | null {
  if (!assistantAttachmentIsSupported(file)) {
    return 'Only PNG, JPEG, WebP screenshots and .txt or .md files can be attached.'
  }
  const maxBytes = assistantAttachmentMaxBytes(file)
  if (file.size > maxBytes) {
    return `Attachments must be ${Math.floor(maxBytes / (1024 * 1024)) || 1} MiB or smaller.`
  }
  const active = existing.filter((candidate) => candidate.status !== 'error')
  if (active.length >= MAX_ASSISTANT_ATTACHMENTS_PER_TURN) {
    return `A turn can contain at most ${MAX_ASSISTANT_ATTACHMENTS_PER_TURN} attachments.`
  }
  const aggregateBytes = active.reduce((total, candidate) => total + (candidate.file?.size ?? candidate.receipt?.sizeBytes ?? 0), 0)
  if (aggregateBytes + file.size > MAX_ASSISTANT_ATTACHMENT_AGGREGATE_BYTES) {
    return 'Attachments in one turn must total 20 MiB or less.'
  }
  return null
}

export function newAssistantStagedAttachment(file: File, error?: string): AssistantStagedAttachment {
  return {
    clientID: `attachment:${Date.now()}:${Math.random().toString(36).slice(2)}`,
    file,
    status: error ? 'error' : 'staged',
    ...(error ? { error } : {}),
    ...(error ? { retryable: false } : {}),
  }
}
