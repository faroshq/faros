import type { ProjectMessage } from './types'

export interface AssistantRun {
  id: string
  status: 'pending_permission' | 'pending_input' | 'running' | 'completed' | 'aborted' | 'failed' | 'interrupted'
  revision: number
  activeMessageID: string
  clientRequestID?: string
  userMessageID?: string
}

export interface AssistantSnapshot {
  run: AssistantRun
  message: ProjectMessage
}

export interface ConversationState<TMessage extends ProjectMessage = ProjectMessage> {
  messages: TMessage[]
  runs: Record<string, AssistantRun>
}

export function assistantRunTerminal(status: AssistantRun['status']): boolean {
  return status === 'completed' || status === 'aborted' || status === 'failed' || status === 'interrupted'
}

// Control hydration is deliberately separate from message merge: a reload may
// receive the same durable revision after local UI state was discarded.
export function canHydrateConversationRun(current: AssistantRun | undefined, incoming: AssistantRun): boolean {
  if (!current) return true
  if (incoming.revision < current.revision) return false
  return !(assistantRunTerminal(current.status) && !assistantRunTerminal(incoming.status) && incoming.revision === current.revision)
}

export function normalizeSnapshotMessage(message: ProjectMessage & { projectName?: string }): ProjectMessage {
  return { ...message, projectID: message.projectID || message.projectName || '' }
}

// Snapshot messages are authoritative and keyed by their durable IDs. Revisions
// make reconnects and simultaneous browser tabs safe: stale snapshots are ignored.
export function mergeConversationSnapshot<TMessage extends ProjectMessage>(
  state: ConversationState<TMessage>,
  snapshot: AssistantSnapshot,
): ConversationState<TMessage> {
  const previous = state.runs[snapshot.run.id]
  if (previous && snapshot.run.revision <= previous.revision) return state
  const index = state.messages.findIndex((item) => item.id === snapshot.message.id)
  const messages = [...state.messages]
  if (index < 0) messages.push(snapshot.message as TMessage)
  else messages[index] = snapshot.message as TMessage
  return { messages, runs: { ...state.runs, [snapshot.run.id]: snapshot.run } }
}

export function replaceOptimisticUserMessage<TMessage extends ProjectMessage>(
  messages: TMessage[],
  optimisticID: string,
  persisted: ProjectMessage,
): TMessage[] {
  const withoutPersisted = messages.filter((item) => item.id !== persisted.id)
  const index = withoutPersisted.findIndex((item) => item.id === optimisticID)
  if (index < 0) return [...withoutPersisted, persisted as TMessage]
  const next = [...withoutPersisted]
  next[index] = persisted as TMessage
  return next
}

interface ConversationRunTransport {
  connect(runID: string, afterRevision: number): Promise<void>
  abort(runID: string): Promise<void>
  setTimeout(fn: () => void, delay: number): ReturnType<typeof setTimeout>
  clearTimeout(timer: ReturnType<typeof setTimeout>): void
}

export class ConversationRunController {
  private runID = ''
  private revision = 0
  private retry = 0
  private retryTimer: ReturnType<typeof setTimeout> | undefined
  private disconnected = false
  private disconnectStream: (() => void) | undefined
  private generation = 0

  constructor(private readonly transport: ConversationRunTransport) {}

  start(runID: string, revision: number) {
    this.disconnect()
    this.generation++
    this.runID = runID
    this.revision = revision
    this.retry = 0
    this.disconnected = false
    void this.connect(this.generation)
  }

  setRevision(revision: number) { this.revision = Math.max(this.revision, revision) }
  markHealthySnapshot(revision: number) {
    this.setRevision(revision)
    this.retry = 0
  }
  setDisconnect(disconnect: () => void) { this.disconnectStream = disconnect }

  disconnect() {
    this.disconnected = true
    if (this.retryTimer !== undefined) this.transport.clearTimeout(this.retryTimer)
    this.retryTimer = undefined
    this.disconnectStream?.()
    this.disconnectStream = undefined
  }

  async stop() {
    if (!this.runID) return
    await this.transport.abort(this.runID)
    this.disconnect()
  }

  private async connect(generation: number) {
    if (this.disconnected || !this.runID) return
    try {
      await this.transport.connect(this.runID, this.revision)
      if (this.disconnected || generation !== this.generation) return
      this.scheduleReconnect(generation)
    } catch {
      if (this.disconnected || generation !== this.generation) return
      this.scheduleReconnect(generation)
    }
  }

  private scheduleReconnect(generation: number) {
    const delay = Math.min(1_000 * 2 ** this.retry, 10_000)
    this.retry++
    this.retryTimer = this.transport.setTimeout(() => { void this.connect(generation) }, delay)
  }
}
