import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, setContext } from './api'

interface FetchCall {
  query: string
  variables: Record<string, unknown>
}

function response(body: unknown, status = 200): Response {
  return new Response(typeof body === 'string' ? body : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function graphqlError(message: string): Response {
  return response({ errors: [{ message }] })
}

function request(init?: RequestInit): FetchCall {
  return JSON.parse(String(init?.body)) as FetchCall
}

function readyStatus(): Record<string, unknown> {
  return {
    observedGeneration: 2,
    backend: { ready: true },
    conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2, reason: 'Ready' }],
  }
}

function template(status: Record<string, unknown> | null | undefined = readyStatus(), view?: unknown) {
  return {
    metadata: { name: 'widget', generation: 2 },
    spec: {
      displayName: 'Widget',
      description: 'test',
      backend: 'kro',
      instanceCRD: {
        group: 'infrastructure.faros.sh',
        version: 'v1alpha1',
        resource: 'widgets',
        kind: 'Widget',
      },
      schema: JSON.stringify({ type: 'object', properties: { foo: { type: 'string' } } }),
      ...(view === undefined ? {} : { view: JSON.stringify(view) }),
    },
    ...(status === undefined ? {} : { status }),
  }
}

function templateList(status?: Record<string, unknown> | null, view?: unknown): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: {
    Templates: { items: [template(status, view)] },
  } } } })
}

function templateGet(status?: Record<string, unknown> | null): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: {
    Template: template(status),
  } } } })
}

function instance(overrides: Record<string, unknown> = {}) {
  return {
    apiVersion: 'infrastructure.faros.sh/v1alpha1',
    kind: 'Instance',
    metadata: {
      uid: 'instance-uid',
      name: 'demo',
      generation: 2,
      creationTimestamp: '2026-08-17T00:00:00Z',
      labels: { 'faros.sh/template': 'widget' },
    },
    spec: { template: 'widget', values: { foo: 'bar' } },
    status: {
      observedGeneration: 2,
      phase: 'Ready',
      conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2 }],
    },
    ...overrides,
  }
}

function instanceList(items: unknown[]): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: {
    Instances: { items },
  } } } })
}

function instanceYaml(value: unknown): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: {
    InstanceYaml: value === null ? null : JSON.stringify(value),
  } } } })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('flattened Instance API', () => {
  it('lists the stable Instances field without per-template discovery', async () => {
    setContext({ tenant: 'flat-list', token: 'flat-token' })
    const queries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      queries.push(query)
      if (query.includes('Templates')) return templateList(readyStatus())
      if (query.includes('Instances')) return instanceList([instance({ status: null })])
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.listInstances()

    expect(result.items[0]).toMatchObject({ name: 'demo', template: 'widget', phase: 'Pending' })
    expect(queries.some(query => query.includes('__type') || query.includes('Widgets'))).toBe(false)
  })

  it('uses spec.template and spec.values from the stable resource', async () => {
    setContext({ tenant: 'flat-detail', token: 'flat-detail-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('InstanceYaml')) return instanceYaml(instance())
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.getInstance('demo')).resolves.toMatchObject({
      name: 'demo',
      template: 'widget',
      values: { foo: 'bar' },
      phase: 'Ready',
    })
  })
})

describe('Instance response safety', () => {
  it('accepts null status as an unreconciled instance', async () => {
    setContext({ tenant: 'null-status', token: 'null-status-token' })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({ status: null }))))

    await expect(api.getInstance('demo')).resolves.toMatchObject({ phase: 'Pending', conditions: [] })
  })

  it('rejects a non-object status with a stable ProtocolError', async () => {
    setContext({ tenant: 'bad-status', token: 'bad-status-token' })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({ status: 'pending' }))))

    await expect(api.getInstance('demo')).rejects.toMatchObject({
      reason: 'ProtocolError',
      message: 'Instance demo status was not an object',
    })
  })

  it('marks current-generation status as pending until the controller catches up', async () => {
    setContext({ tenant: 'stale-status', token: 'stale-status-token' })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({
      metadata: { name: 'demo', generation: 3 },
      status: {
        observedGeneration: 2,
        phase: 'Ready',
        conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2 }],
      },
    }))))

    await expect(api.getInstance('demo')).resolves.toMatchObject({
      phase: 'Pending',
      message: 'Waiting for the controller to observe generation 3.',
    })
  })

  it('uses top-level observedGeneration instead of a mirrored runtime condition generation', async () => {
    setContext({ tenant: 'generation-authority', token: 'generation-authority-token' })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(instanceYaml(instance({
        metadata: { name: 'demo', generation: 3 },
        status: {
          observedGeneration: 2,
          phase: 'Ready',
          message: 'old runtime status',
          conditions: [{ type: 'Ready', status: 'True', observedGeneration: 99 }],
        },
      })))
      .mockResolvedValueOnce(instanceYaml(instance({
        metadata: { name: 'demo', generation: 3 },
        status: {
          observedGeneration: 3,
          phase: 'Ready',
          conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2 }],
        },
      })))
    vi.stubGlobal('fetch', fetchMock)

    await expect(api.getInstance('demo')).resolves.toMatchObject({
      phase: 'Pending',
      observedGeneration: 2,
      message: 'Waiting for the controller to observe generation 3.',
      conditions: [{ type: 'Ready', observedGeneration: 99 }],
    })
    await expect(api.getInstance('demo')).resolves.toMatchObject({
      phase: 'Ready',
      observedGeneration: 3,
      conditions: [{ type: 'Ready', observedGeneration: 2 }],
    })
  })

  it('keeps a reported Ready phase pending until top-level observedGeneration exists', async () => {
    setContext({ tenant: 'missing-observation', token: 'missing-observation-token' })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({
      metadata: { name: 'demo', generation: 3 },
      status: {
        phase: 'Ready',
        conditions: [{ type: 'Ready', status: 'True', observedGeneration: 3 }],
      },
    }))))

    await expect(api.getInstance('demo')).resolves.toMatchObject({
      phase: 'Pending',
      observedGeneration: undefined,
      message: 'Waiting for the controller to observe generation 3.',
    })
  })

  it.each([
    ['metadata.generation', { metadata: { name: 'demo', generation: '3' } }, 'Instance demo metadata.generation had an invalid shape'],
    ['status.phase', { status: { observedGeneration: 2, phase: {}, conditions: [] } }, 'Instance demo status.phase had an invalid shape'],
    ['status.message', { status: { observedGeneration: 2, phase: 'Pending', message: [], conditions: [] } }, 'Instance demo status.message had an invalid shape'],
  ])('rejects a malformed %s', async (_field, override, message) => {
    setContext({ tenant: `bad-${_field}`, token: `bad-${_field}-token` })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance(override))))

    await expect(api.getInstance('demo')).rejects.toMatchObject({ reason: 'ProtocolError', message })
  })

  it('parses child resources from the full Instance status', async () => {
    setContext({ tenant: 'instance-children', token: 'instance-children-token' })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({
      status: {
        observedGeneration: 2,
        phase: 'Ready',
        conditions: [{ type: 'Ready', status: 'True', observedGeneration: 7 }],
        children: [{
          apiVersion: 'apps/v1',
          kind: 'Deployment',
          name: 'demo-web',
          namespace: 'instance-demo',
          phase: 'Ready',
        }],
      },
    }))))

    await expect(api.getInstance('demo')).resolves.toMatchObject({
      children: [{
        apiVersion: 'apps/v1',
        kind: 'Deployment',
        name: 'demo-web',
        namespace: 'instance-demo',
        phase: 'Ready',
      }],
    })
  })

  it.each([
    ['non-array children', { apiVersion: 'apps/v1' }],
    ['non-object child', ['deployment']],
    ['incomplete child', [{ apiVersion: 'apps/v1', kind: 'Deployment' }]],
    ['malformed optional child field', [{ apiVersion: 'apps/v1', kind: 'Deployment', name: 'demo', namespace: 4 }]],
  ])('rejects %s', async (_case, children) => {
    setContext({ tenant: `bad-children-${_case}`, token: `bad-children-${_case}-token` })
    vi.stubGlobal('fetch', vi.fn(async () => instanceYaml(instance({
      status: { observedGeneration: 2, phase: 'Ready', conditions: [], children },
    }))))

    await expect(api.getInstance('demo')).rejects.toMatchObject({ reason: 'ProtocolError' })
  })
})

describe('list enrichment', () => {
  async function listWithEnrichmentError(tenant: string, message: string) {
    setContext({ tenant, token: `token-${tenant}` })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Templates')) {
        return templateList(readyStatus(), { columns: [{ header: 'Foo', path: 'spec.foo' }] })
      }
      if (query.includes('Instances')) return instanceList([instance({ status: null })])
      if (query.includes('InstanceYaml')) return graphqlError(message)
      throw new Error(`unexpected query: ${query}`)
    }))
    return api.listInstances()
  }

  it('keeps the list snapshot when the Instance disappears before enrichment', async () => {
    const result = await listWithEnrichmentError(
      'enrichment-not-found',
      'instances.infrastructure.faros.sh "demo" not found',
    )
    expect(result.items).toHaveLength(1)
    expect(result.items[0]).toMatchObject({ name: 'demo', template: 'widget' })
  })

  it('does not hide a lookalike NotFound', async () => {
    await expect(listWithEnrichmentError(
      'enrichment-lookalike',
      'instances.infrastructure.faros.sh "demo" not found while decoding',
    )).rejects.toMatchObject({ reason: 'GraphQLError' })
  })

  it('does not hide an exact NotFound for the retired resource', async () => {
    await expect(listWithEnrichmentError(
      'enrichment-old-resource',
      'applications.infrastructure.faros.sh "demo" not found',
    )).rejects.toMatchObject({ reason: 'GraphQLError' })
  })
})

describe('Template compatibility and readiness', () => {
  it('retries only an exact optional-field GraphQL validation failure', async () => {
    setContext({ tenant: 'old-template-view', token: 'old-view-token' })
    const queries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      queries.push(query)
      if (query.includes(' view')) {
        return graphqlError('Cannot query field "view" on type "InfrastructureFarosShV1alpha1TemplateSpec".')
      }
      return templateList(readyStatus())
    }))

    const result = await api.listTemplates()

    expect(result.items[0]).toMatchObject({ name: 'widget', ready: true, view: undefined })
    expect(queries).toHaveLength(2)
    expect(queries[0]).toContain(' view')
    expect(queries[1]).not.toContain(' view')
  })

  it('does not downgrade a capability for an HTTP failure with similar text', async () => {
    setContext({ tenant: 'template-http', token: 'template-http-token' })
    const fetchMock = vi.fn(async () => response('Cannot query field "view" while unavailable', 503))
    vi.stubGlobal('fetch', fetchMock)

    await expect(api.listTemplates()).rejects.toMatchObject({ reason: 'HTTPError' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps a null Template status visible but unavailable', async () => {
    setContext({ tenant: 'template-null-status', token: 'template-null-token' })
    vi.stubGlobal('fetch', vi.fn(async () => templateList(null)))

    const result = await api.listTemplates()
    expect(result.items[0]).toMatchObject({
      name: 'widget',
      ready: false,
      conditions: [],
      readinessMessage: 'Waiting for the template controller to observe generation 2.',
    })
  })

  it('includes current backend readiness on a direct Template read', async () => {
    setContext({ tenant: 'template-ready', token: 'template-ready-token' })
    vi.stubGlobal('fetch', vi.fn(async () => templateGet(readyStatus())))

    await expect(api.getTemplate('widget')).resolves.toMatchObject({
      template: { name: 'widget', ready: true, generation: 2, observedGeneration: 2 },
    })
  })
})

describe('createInstance', () => {
  it('re-reads Template readiness and sends the flattened manifest', async () => {
    setContext({ tenant: 'flat-create', token: 'flat-create-token' })
    let applied: Record<string, unknown> | undefined
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query, variables } = request(init)
      if (query.includes('Templates')) return templateList(readyStatus())
      if (query.includes('applyYaml')) {
        applied = JSON.parse(String(variables.y)) as Record<string, unknown>
        return response({ data: { applyYaml: JSON.stringify(instance()) } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.createInstance({
      templateName: 'widget',
      name: 'demo',
      values: { foo: 'bar' },
    })).resolves.toMatchObject({ name: 'demo', template: 'widget' })
    expect(applied).toMatchObject({
      apiVersion: 'infrastructure.faros.sh/v1alpha1',
      kind: 'Instance',
      metadata: { name: 'demo' },
      spec: { template: 'widget', values: { foo: 'bar' } },
    })
  })

  it('hands the validated create result to detail during an exact propagation miss', async () => {
    setContext({ tenant: 'create-propagation', token: 'create-propagation-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Templates')) return templateList(readyStatus())
      if (query.includes('applyYaml')) return response({ data: { applyYaml: JSON.stringify(instance()) } })
      if (query.includes('InstanceYaml')) {
        return graphqlError('instances.infrastructure.faros.sh "demo" not found')
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await api.createInstance({ templateName: 'widget', name: 'demo', values: { foo: 'bar' } })

    await expect(api.getInstanceDetail('demo')).resolves.toMatchObject({
      instance: { name: 'demo', template: 'widget', values: { foo: 'bar' } },
      template: { name: 'widget' },
    })
  })

  it('does not provision from a stale or backend-unready Template', async () => {
    setContext({ tenant: 'unready-create', token: 'unready-create-token' })
    const fetchMock = vi.fn(async () => templateList({
      observedGeneration: 2,
      backend: { ready: false, message: 'kro is not ready' },
      conditions: [{ type: 'Ready', status: 'False', observedGeneration: 2, message: 'kro is not ready' }],
    }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(api.createInstance({ templateName: 'widget', name: 'demo', values: {} }))
      .rejects.toMatchObject({ reason: 'TemplateNotReady', message: 'kro is not ready' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('rejects an apply response for a different resource', async () => {
    setContext({ tenant: 'mismatch-create', token: 'mismatch-create-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Templates')) return templateList(readyStatus())
      if (query.includes('applyYaml')) {
        return response({ data: { applyYaml: JSON.stringify(instance({ metadata: { name: 'other' } })) } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.createInstance({ templateName: 'widget', name: 'demo', values: {} }))
      .rejects.toMatchObject({ reason: 'ProtocolError', message: 'applyYaml returned a different resource than requested' })
  })
})

describe('Instance NotFound handling', () => {
  it('maps the exact stable Instance miss to InstanceNotFound', async () => {
    setContext({ tenant: 'get-not-found', token: 'get-not-found-token' })
    vi.stubGlobal('fetch', vi.fn(async () => graphqlError('instances.infrastructure.faros.sh "demo" not found')))

    await expect(api.getInstance('demo')).rejects.toMatchObject({ reason: 'InstanceNotFound' })
  })

  it('does not hide a lookalike read error', async () => {
    setContext({ tenant: 'get-lookalike', token: 'get-lookalike-token' })
    vi.stubGlobal('fetch', vi.fn(async () => graphqlError('instances.infrastructure.faros.sh "demo" not found while authorizing')))

    await expect(api.getInstance('demo')).rejects.toMatchObject({ reason: 'GraphQLError' })
  })

  it('makes delete idempotent only for the exact stable Instance miss', async () => {
    setContext({ tenant: 'delete-not-found', token: 'delete-not-found-token' })
    vi.stubGlobal('fetch', vi.fn(async () => graphqlError('instances.infrastructure.faros.sh "demo" not found')))

    await expect(api.deleteInstance('demo')).resolves.toBeUndefined()
  })

  it('does not hide a retired per-template resource miss on delete', async () => {
    setContext({ tenant: 'delete-old-resource', token: 'delete-old-resource-token' })
    vi.stubGlobal('fetch', vi.fn(async () => graphqlError('applications.infrastructure.faros.sh "demo" not found')))

    await expect(api.deleteInstance('demo')).rejects.toMatchObject({ reason: 'GraphQLError' })
  })
})

describe('tenant-safe authority cache', () => {
  it('evicts the least-recently-used Template cache after eight authorities', async () => {
    let templateQueries = 0
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Templates')) {
        templateQueries += 1
        return templateList(readyStatus())
      }
      if (query.includes('Instances')) return instanceList([])
      throw new Error(`unexpected query: ${query}`)
    }))

    const authorities = Array.from({ length: 9 }, () => ({}))
    for (let index = 0; index < authorities.length; index += 1) {
      await api.listInstances({
        tenant: `bounded-${index}`,
        token: `secret-${index}`,
        authority: authorities[index],
      })
    }
    await api.listInstances({ tenant: 'bounded-8', token: 'secret-8', authority: authorities[8] })
    expect(templateQueries).toBe(9)

    await api.listInstances({ tenant: 'bounded-0', token: 'secret-0', authority: authorities[0] })
    expect(templateQueries).toBe(10)
  })
})
