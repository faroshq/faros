import { createRepositorySync, graphqlPath, setTenant, setToken } from './api.js'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const escaped = graphqlPath('../evil')
assert(escaped === '/graphql/..%2Fevil', 'tenant path did not encode traversal input as one segment')
assert(!escaped.includes('/graphql/../'), 'tenant path still contains an unescaped traversal separator')
assert(graphqlPath('org/team') === '/graphql/org%2Fteam', 'tenant slash was not encoded inside the path segment')

const originalFetch = globalThis.fetch
const requests: Array<{ input: RequestInfo | URL; init?: RequestInit }> = []

try {
  setTenant('org/team')
  setToken('portal-token')
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push({ input, init })
    return new Response(JSON.stringify({
      data: {
        deployments_faros_sh: {
          v1alpha1: {
            createRepositorySync: {
              metadata: { name: 'pen-store-production', uid: 'sync-1', generation: 1 },
              spec: {
                repositoryRef: 'pen-store-app',
                ref: 'main',
                path: 'deploy/production',
                intervalSeconds: 30,
                prune: true,
              },
              status: {},
            },
          },
        },
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }) as typeof fetch

  const created = await createRepositorySync({
    name: 'pen-store-production',
    repositoryRef: 'pen-store-app',
    ref: 'main',
    path: 'deploy/production',
    intervalSeconds: 30,
    prune: true,
  })

  assert(created.name === 'pen-store-production', 'created RepositorySync name was not mapped')
  assert(created.repositoryRef === 'pen-store-app', 'created RepositorySync repositoryRef was not mapped')
  assert(created.ref === 'main', 'created RepositorySync ref was not mapped')
  assert(created.path === 'deploy/production', 'created RepositorySync path was not mapped')
  assert(created.intervalSeconds === 30, 'created RepositorySync interval was not mapped')
  assert(created.prune, 'created RepositorySync prune setting was not mapped')
  assert(requests.length === 1, 'createRepositorySync should issue one gateway request')

  const request = requests[0]
  assert(String(request.input) === '/graphql/org%2Fteam', 'create request did not use the encoded tenant path')
  assert(request.init?.method === 'POST', 'create request was not a POST')
  const headers = request.init?.headers as Record<string, string> | undefined
  assert(headers?.Authorization === 'Bearer portal-token', 'create request did not forward the bearer token')
  const body = JSON.parse(String(request.init?.body)) as {
    query: string
    variables: { object: Record<string, unknown> }
  }
  assert(body.query.includes('DeploymentsFarosShV1alpha1RepositorySync_Input!'), 'create mutation used the wrong gateway input type')
  assert(body.query.includes('createRepositorySync(object: $object)'), 'create mutation used the wrong gateway field')
  assert(JSON.stringify(body.variables.object) === JSON.stringify({
    metadata: { name: 'pen-store-production' },
    spec: {
      repositoryRef: 'pen-store-app',
      ref: 'main',
      path: 'deploy/production',
      intervalSeconds: 30,
      prune: true,
    },
  }), 'create mutation variables did not match the RepositorySync API')

  await createRepositorySync({
    name: 'pen-store-defaults',
    repositoryRef: 'pen-store-app',
    ref: '',
    path: '',
  })
  const defaultsRequest = requests.at(1)
  assert(defaultsRequest, 'second createRepositorySync did not issue an additional gateway request')
  const defaultsBody = JSON.parse(String(defaultsRequest.init?.body)) as {
    variables: { object: Record<string, unknown> }
  }
  assert(JSON.stringify(defaultsBody.variables.object) === JSON.stringify({
    metadata: { name: 'pen-store-defaults' },
    spec: { repositoryRef: 'pen-store-app' },
  }), 'blank and undefined optional fields were not omitted from the create object')
} finally {
  globalThis.fetch = originalFetch
  setToken(null)
  setTenant(null)
}
