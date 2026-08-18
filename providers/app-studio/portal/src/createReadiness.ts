export interface ProjectCreateReadiness {
  gitConnection: {
    ready: boolean
    connectionRef?: string
    message?: string
  }
  // GitOps is an optional delivery capability. Direct delivery does not need
  // Deployments, so its absence must not block project creation. Keep this
  // separate from the Git connection readiness rather than treating the
  // provider catalog's process health as tenant access.
  gitOps?: {
    available: boolean
    reason?: string
    message?: string
  }
}

export interface CreateSetupItemsInput {
  readiness: ProjectCreateReadiness | null
  llmConfigured: boolean
  checkingGit: boolean
}

export interface CreateSetupItem {
  id: 'git' | 'llm'
  label: string
  status: 'checking' | 'ready' | 'missing'
  actionLabel?: string
  action?: 'connect-git' | 'setup-llm'
}

const defaultGitConnectionMessage = 'You need to connect to a Git account before you can continue'
const defaultGitOpsMessage = 'Open Providers, enable Deployments, and approve its requested target access to use reviewed production.'

export function gitConnectionReady(readiness: ProjectCreateReadiness | null): boolean {
  return readiness?.gitConnection.ready === true
}

export function gitOpsAvailable(readiness: ProjectCreateReadiness | null): boolean {
  return readiness?.gitOps?.available === true
}

export function gitOpsReadinessMessage(readiness: ProjectCreateReadiness | null): string {
  if (gitOpsAvailable(readiness)) return ''
  const detail = readiness?.gitOps?.reason?.trim() || readiness?.gitOps?.message?.trim()
  return detail ? `Open Providers and update access for Deployments or App Studio: ${detail}` : defaultGitOpsMessage
}

export function createPromptBlockedMessage(readiness: ProjectCreateReadiness | null): string {
  if (gitConnectionReady(readiness)) return ''
  return readiness?.gitConnection.message?.trim() || defaultGitConnectionMessage
}

export function canSubmitCreatePrompt(prompt: string, readiness: ProjectCreateReadiness | null): boolean {
  return prompt.trim().length > 0 && gitConnectionReady(readiness)
}

export function createSetupItems(input: CreateSetupItemsInput): CreateSetupItem[] {
  const gitReady = gitConnectionReady(input.readiness)
  if (gitReady && input.llmConfigured) return []

  const items: CreateSetupItem[] = [gitSetupItem(gitReady, input.checkingGit)]

  items.push(
    input.llmConfigured
      ? {
        id: 'llm',
        label: 'LLM credentials',
        status: 'ready',
      }
      : {
        id: 'llm',
        label: 'LLM credentials',
        status: 'missing',
        actionLabel: 'Set up LLM',
        action: 'setup-llm',
      },
  )

  return items
}

function gitSetupItem(gitReady: boolean, checkingGit: boolean): CreateSetupItem {
  if (checkingGit) {
    return {
      id: 'git',
      label: 'Git connection',
      status: 'checking',
    }
  }
  if (gitReady) {
    return {
      id: 'git',
      label: 'Git connection',
      status: 'ready',
    }
  }
  return {
    id: 'git',
    label: 'Git connection',
    status: 'missing',
    actionLabel: 'Connect Git',
    action: 'connect-git',
  }
}
