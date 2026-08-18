import {
  hasCreateRepositorySyncErrors,
  repositorySyncPathError,
  validateCreateRepositorySync,
} from './createRepositorySyncForm.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const valid = validateCreateRepositorySync({
  name: 'pen-store.production',
  repositoryRef: 'pen-store-app',
  path: '.faros/environments/production',
  intervalSeconds: 30,
  prune: true,
})
assert(!hasCreateRepositorySyncErrors(valid), 'valid RepositorySync form was rejected')

for (const intervalSeconds of [10, 3600]) {
  const boundary = validateCreateRepositorySync({
    name: 'pen-store-production',
    repositoryRef: 'pen-store-app',
    intervalSeconds,
  })
  assert(!boundary.intervalSeconds, `valid interval boundary ${intervalSeconds} was rejected`)
}

for (const intervalSeconds of [9, 3601, 10.5]) {
  const invalid = validateCreateRepositorySync({
    name: 'pen-store-production',
    repositoryRef: 'pen-store-app',
    intervalSeconds,
  })
  assert(Boolean(invalid.intervalSeconds), `invalid interval ${intervalSeconds} was accepted`)
}

for (const name of ['', ' Pen-store', 'Pen-store', '-pen-store', 'pen-store-']) {
  const invalid = validateCreateRepositorySync({ name, repositoryRef: 'pen-store-app', intervalSeconds: 30 })
  assert(Boolean(invalid.name), `invalid name ${JSON.stringify(name)} was accepted`)
}

const missingRepository = validateCreateRepositorySync({ name: 'pen-store', repositoryRef: '  ', intervalSeconds: 30 })
assert(Boolean(missingRepository.repositoryRef), 'blank repository was accepted')

for (const path of ['.', '/', '/absolute', '../production', 'environments/../production', String.raw`C:\production`]) {
  assert(Boolean(repositorySyncPathError(path)), `unsafe path ${JSON.stringify(path)} was accepted`)
}
assert(!repositorySyncPathError(''), 'blank path should use the controller default')
assert(!repositorySyncPathError('.faros/production'), 'safe repository-relative path was rejected')
