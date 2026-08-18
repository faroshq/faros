import type { CreateRepositorySyncInput } from './types.js'

export interface CreateRepositorySyncFormErrors {
  name: string
  repositoryRef: string
  path: string
  intervalSeconds: string
}

function isDNS1123Subdomain(value: string): boolean {
  if (value.length > 253) return false
  return value.split('.').every(label => (
    label.length > 0
    && label.length <= 63
    && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)
  ))
}

export function repositorySyncPathError(value: string): string {
  const path = value.trim()
  if (!path) return ''
  if (
    path === '.'
    || path === '/'
    || path.startsWith('/')
    || path.startsWith('\\')
    || /^[a-z]:[\\/]/i.test(path)
    || path.split(/[\\/]+/).includes('..')
  ) {
    return 'Use a repository-relative directory without root or parent-directory segments.'
  }
  return ''
}

export function validateCreateRepositorySync(input: CreateRepositorySyncInput): CreateRepositorySyncFormErrors {
  const errors: CreateRepositorySyncFormErrors = {
    name: '',
    repositoryRef: '',
    path: '',
    intervalSeconds: '',
  }
  const name = input.name.trim()
  if (!name) {
    errors.name = 'Name is required.'
  } else if (name !== input.name) {
    errors.name = 'Name cannot begin or end with whitespace.'
  } else if (!isDNS1123Subdomain(name)) {
    errors.name = 'Use a DNS-1123 name: lowercase letters, numbers, hyphens, or dots, starting and ending with a letter or number.'
  }

  if (!input.repositoryRef.trim()) errors.repositoryRef = 'Repository is required.'
  errors.path = repositorySyncPathError(input.path || '')

  const interval = input.intervalSeconds
  if (interval === undefined || !Number.isInteger(interval) || interval < 10 || interval > 3600) {
    errors.intervalSeconds = 'Interval must be a whole number from 10 to 3600 seconds.'
  }
  return errors
}

export function hasCreateRepositorySyncErrors(errors: CreateRepositorySyncFormErrors): boolean {
  return Boolean(errors.name || errors.repositoryRef || errors.path || errors.intervalSeconds)
}
