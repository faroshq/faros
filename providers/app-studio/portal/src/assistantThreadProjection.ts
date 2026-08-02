import type { ProjectAssistantThreadItem, ProjectMessage } from './types'

export function assistantThreadItemsToMessages(items: ProjectAssistantThreadItem[], projectName: string): ProjectMessage[] {
  const ordered = [...items].sort((left, right) => left.sequence - right.sequence)
  const result: ProjectMessage[] = []
  const assistantByTurn = new Map<string, number>()
  for (const item of ordered) {
    if (item.type !== 'userMessage' && item.type !== 'agentMessage') continue
    const role = item.type === 'userMessage' ? 'user' : 'assistant'
    const metadata: Record<string, unknown> = {}
    if (role === 'assistant') {
      metadata.assistantStatus = item.status === 'failed' ? 'failed' : item.status === 'completed' ? 'completed' : 'running'
      if (item.data?.assistantProgress) metadata.assistantProgress = item.data.assistantProgress
      if (item.turnID) assistantByTurn.set(item.turnID, result.length)
    }
    result.push({
      id: item.id,
      projectID: projectName,
      role,
      content: item.content ?? '',
      metadata,
      createdAt: item.createdAt,
    })
  }
  for (const item of ordered) {
    if (!item.turnID || item.type === 'userMessage' || item.type === 'agentMessage') continue
    const index = assistantByTurn.get(item.turnID)
    if (index === undefined) continue
    const message = result[index]
    const metadata = { ...(message.metadata ?? {}) }
    if (item.type === 'dynamicToolCall' && item.data) {
      const actions = Array.isArray(metadata.assistantActionFeed) ? [...metadata.assistantActionFeed] : []
      const existing = actions.findIndex((action) => typeof action === 'object' && action !== null && (action as { id?: string }).id === item.id)
      if (existing >= 0) actions[existing] = item.data
      else actions.push(item.data)
      metadata.assistantActionFeed = actions
    } else if (item.type === 'plan' && item.data) {
      metadata.assistantPlan = item.data
    } else if ((item.type === 'approval' || item.type === 'input') && item.status === 'in_progress' && item.data) {
      metadata.assistantInterrupt = item.data
    }
    result[index] = { ...message, metadata }
  }
  return result
}

export function maxAssistantThreadSequence(items: ProjectAssistantThreadItem[]): number {
  return items.reduce((current, item) => Math.max(current, item.sequence), 0)
}
