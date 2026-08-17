import { afterEach, describe, expect, it, vi } from 'vitest'

import { api, setContext } from './api'

interface FetchCall {
  query: string
  variables: Record<string, unknown>
}

function response(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function graphqlError(message: string): Response {
  return response({ errors: [{ message }] })
}

function request(init?: RequestInit): FetchCall {
  return JSON.parse(String(init?.body)) as FetchCall
}

function indexResponse(query: string): Response | null {
  if (query.includes('__type')) {
    return response({ data: { __type: { fields: [{
      name: 'infrastructure_faros_sh',
      type: { fields: [{
        name: 'v1alpha1',
        type: { fields: [
          { name: 'Widget', type: { name: 'InfrastructureWidget' } },
          { name: 'Widgets', type: { name: 'InfrastructureWidgetList' } },
          { name: 'WidgetYaml', type: { name: 'String' } },
        ] },
      }] },
    }] } } })
  }
  if (query.includes('Templates')) {
    return response({ data: { infrastructure_faros_sh: { v1alpha1: { Templates: { items: [{
      metadata: { name: 'widget' },
      spec: {
        displayName: 'Widget',
        description: 'test',
        instanceCRD: {
          group: 'infrastructure.faros.sh',
          version: 'v1alpha1',
          resource: 'widgets',
          kind: 'Widget',
        },
        schema: JSON.stringify({ type: 'object', properties: { foo: { type: 'string' } } }),
        view: JSON.stringify({ columns: [{ header: 'Foo', path: 'spec.foo' }] }),
      },
    }] } } } } })
  }
  return null
}

function templateListResponse(status?: Record<string, unknown> | null): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: { Templates: { items: [{
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
    },
    ...(status === undefined ? {} : { status }),
  }] } } } } })
}

function templateGetResponse(status?: Record<string, unknown> | null): Response {
  return response({ data: { infrastructure_faros_sh: { v1alpha1: { Template: {
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
      schema: JSON.stringify({ type: 'object' }),
    },
    ...(status === undefined ? {} : { status }),
  } } } } })
}

function readyTemplateStatus(): Record<string, unknown> {
  return {
    observedGeneration: 2,
    backend: { ready: true },
    conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2, reason: 'Ready' }],
  }
}

async function listWithEnrichmentError(tenant: string, message: string) {
  setContext({ tenant, token: `token-${tenant}` })
  vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    const { query } = request(init)
    const index = indexResponse(query)
    if (index) return index
    if (query.includes('Widgets')) {
      return response({ data: { infrastructure_faros_sh: { v1alpha1: { Widgets: { items: [{
        metadata: {
          uid: 'widget-uid',
          name: 'gone',
          generation: 1,
          labels: { 'faros.sh/template': 'widget' },
        },
        status: null,
      }] } } } } })
    }
    if (query.includes('WidgetYaml')) return graphqlError(message)
    throw new Error(`unexpected query: ${query}`)
  }))
  return api.listInstances()
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('listInstances enrichment', () => {
  it('keeps the LIST snapshot when the resource disappears before its Yaml GET', async () => {
    const result = await listWithEnrichmentError(
      'enrichment-exact',
      'widgets.infrastructure.faros.sh "gone" not found',
    )
    expect(result.items).toHaveLength(1)
    expect(result.items[0]).toMatchObject({ name: 'gone', template: 'widget' })
  })

  it('does not hide lookalike enrichment errors', async () => {
    await expect(listWithEnrichmentError(
      'enrichment-lookalike',
      'widgets.infrastructure.faros.sh "gone" not found while decoding',
    )).rejects.toMatchObject({ reason: 'GraphQLError' })
  })

  it('does not hide an exact NotFound for the wrong group-resource', async () => {
    await expect(listWithEnrichmentError(
      'enrichment-wrong-resource',
      'applications.infrastructure.faros.sh "gone" not found',
    )).rejects.toMatchObject({ reason: 'GraphQLError' })
  })
})

describe('instance condition schema compatibility', () => {
  it('retries one older per-kind schema without condition observedGeneration', async () => {
    setContext({ tenant: 'old-condition-schema', token: 'old-schema-token' })
    const widgetQueries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      const index = indexResponse(query)
      if (index) return index
      if (query.includes('Widgets')) {
        widgetQueries.push(query)
        if (query.includes('observedGeneration')) {
          return graphqlError('Cannot query field "observedGeneration" on type "InfrastructureFarosShV1alpha1WidgetStatus".')
        }
        return response({ data: { infrastructure_faros_sh: { v1alpha1: { Widgets: { items: [{
          metadata: { name: 'demo', generation: 1, labels: { 'faros.sh/template': 'widget' } },
          status: { phase: 'Ready', conditions: [{ type: 'Ready', status: 'True' }] },
        }] } } } } })
      }
      if (query.includes('WidgetYaml')) {
        return response({ data: { infrastructure_faros_sh: { v1alpha1: {
          WidgetYaml: JSON.stringify({
            apiVersion: 'infrastructure.faros.sh/v1alpha1',
            kind: 'Widget',
            metadata: { name: 'demo', generation: 1, labels: { 'faros.sh/template': 'widget' } },
            spec: { foo: 'bar' },
            status: { phase: 'Ready', conditions: [{ type: 'Ready', status: 'True' }] },
          }),
        } } } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const first = await api.listInstances()
    const second = await api.listInstances()

    expect(first.items[0]).toMatchObject({ name: 'demo', phase: 'Ready', observedGeneration: undefined })
    expect(second.items[0]).toMatchObject({ name: 'demo', phase: 'Ready', observedGeneration: undefined })
    expect(widgetQueries).toHaveLength(3)
    expect(widgetQueries[0]).toContain('observedGeneration')
    expect(widgetQueries[1]).not.toContain('observedGeneration')
    expect(widgetQueries[2]).not.toContain('observedGeneration')
  })

  it('does not hide a lookalike field error', async () => {
    setContext({ tenant: 'condition-schema-lookalike', token: 'lookalike-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      const index = indexResponse(query)
      if (index) return index
      if (query.includes('Widgets')) {
        return graphqlError('Cannot query field "observedGeneration" while resolving authorization')
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.listInstances()).rejects.toMatchObject({ reason: 'GraphQLError' })
  })
})

describe('Template schema compatibility', () => {
  it('retries an exact GraphQL validation failure without the optional field', async () => {
    setContext({ tenant: 'old-template-view-schema', token: 'old-view-token' })
    const templateQueries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) {
        templateQueries.push(query)
        if (query.includes(' view')) {
          return graphqlError('Cannot query field "view" on type "InfrastructureFarosShV1alpha1TemplateSpec".')
        }
        return templateListResponse(readyTemplateStatus())
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.listTemplates()

    expect(result.items[0]).toMatchObject({ name: 'widget', ready: true, view: undefined })
    expect(templateQueries).toHaveLength(2)
    expect(templateQueries[0]).toContain(' view')
    expect(templateQueries[1]).not.toContain(' view')
  })

  it('does not downgrade capabilities for an HTTP failure with lookalike text', async () => {
    setContext({ tenant: 'template-view-http-error', token: 'view-http-token' })
    let templateQueries = 0
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) {
        templateQueries += 1
        return new Response('Cannot query field "view" while the gateway is unavailable', { status: 503 })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.listTemplates()).rejects.toMatchObject({ reason: 'HTTPError' })
    expect(templateQueries).toBe(1)
  })
})

describe('Template readiness', () => {
  it('treats a GraphQL null status as absent and not ready', async () => {
    setContext({ tenant: 'template-null-status', token: 'null-status-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) return templateListResponse(null)
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.listTemplates()

    expect(result.items[0]).toMatchObject({
      name: 'widget',
      ready: false,
      observedGeneration: undefined,
      conditions: [],
      readinessMessage: 'Waiting for the template controller to observe generation 2.',
    })
  })

  it('treats null top-level status members as absent and not ready', async () => {
    setContext({ tenant: 'template-null-status-members', token: 'null-status-members-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) {
        return templateListResponse({ observedGeneration: null, backend: null, conditions: null })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.listTemplates()

    expect(result.items[0]).toMatchObject({
      name: 'widget',
      ready: false,
      observedGeneration: undefined,
      conditions: [],
      readinessMessage: 'Waiting for the template controller to observe generation 2.',
    })
  })

  it('treats null optional status members as absent and not ready', async () => {
    setContext({ tenant: 'template-null-status-fields', token: 'null-status-fields-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Template(name:')) {
        return templateGetResponse({
          observedGeneration: null,
          backend: { ready: null, message: null },
          conditions: [{
            type: 'Ready',
            status: 'True',
            observedGeneration: null,
            reason: null,
            message: null,
            lastTransitionTime: null,
          }],
        })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.getTemplate('widget')

    expect(result.template).toMatchObject({
      name: 'widget',
      ready: false,
      observedGeneration: undefined,
      conditions: [{ type: 'Ready', status: 'True', observedGeneration: undefined }],
      readinessMessage: 'Waiting for the template controller to observe generation 2.',
    })
  })

  it('degrades a Template with absent status to visible but unavailable', async () => {
    setContext({ tenant: 'template-no-status', token: 'no-status-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) return templateListResponse()
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.listTemplates()

    expect(result.items[0]).toMatchObject({
      name: 'widget',
      ready: false,
      readinessMessage: 'Waiting for the template controller to observe generation 2.',
    })
  })

  it('includes current backend readiness on a direct Template read', async () => {
    setContext({ tenant: 'template-ready', token: 'ready-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('Template(name:')) return templateGetResponse(readyTemplateStatus())
      throw new Error(`unexpected query: ${query}`)
    }))

    const result = await api.getTemplate('widget')

    expect(result.template).toMatchObject({
      name: 'widget',
      ready: true,
      generation: 2,
      observedGeneration: 2,
      conditions: [{ type: 'Ready', status: 'True', observedGeneration: 2 }],
    })
  })

  it('rejects direct provisioning after a fresh unready Template read', async () => {
    setContext({ tenant: 'template-unready-create', token: 'unready-token' })
    const queries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      queries.push(query)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) {
        return templateListResponse({
          observedGeneration: 2,
          backend: { ready: false, message: 'KRO controller is not ready' },
          conditions: [{
            type: 'Ready',
            status: 'False',
            observedGeneration: 2,
            reason: 'BackendError',
            message: 'KRO controller is not ready',
          }],
        })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.createInstance({
      templateName: 'widget',
      name: 'demo',
      values: {},
    })).rejects.toMatchObject({ reason: 'TemplateNotReady', message: 'KRO controller is not ready' })
    expect(queries.some(query => query.includes('applyYaml'))).toBe(false)
  })

  it('provisions after a fresh current-generation backend-ready read', async () => {
    setContext({ tenant: 'template-ready-create', token: 'ready-create-token' })
    const queries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      queries.push(query)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) return templateListResponse(readyTemplateStatus())
      if (query.includes('applyYaml')) {
        return response({ data: { applyYaml: JSON.stringify({
          apiVersion: 'infrastructure.faros.sh/v1alpha1',
          kind: 'Widget',
          metadata: { name: 'demo', labels: { 'faros.sh/template': 'widget' } },
          spec: { foo: 'bar' },
        }) } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const created = await api.createInstance({
      templateName: 'widget',
      name: 'demo',
      values: { foo: 'bar' },
    })

    expect(created).toMatchObject({ name: 'demo', template: 'widget', values: { foo: 'bar' } })
    expect(queries.some(query => query.includes('applyYaml'))).toBe(true)
  })

  it('rejects an apply response for a different resource', async () => {
    setContext({ tenant: 'template-mismatched-apply', token: 'mismatched-apply-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) return indexResponse(query)!
      if (query.includes('Templates')) return templateListResponse(readyTemplateStatus())
      if (query.includes('applyYaml')) {
        return response({ data: { applyYaml: JSON.stringify({
          apiVersion: 'infrastructure.faros.sh/v1alpha1',
          kind: 'Widget',
          metadata: { name: 'another-instance' },
          spec: {},
        }) } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.createInstance({
      templateName: 'widget',
      name: 'demo',
      values: {},
    })).rejects.toMatchObject({
      reason: 'ProtocolError',
      message: 'applyYaml returned a different resource than requested',
    })
  })
})

describe('deleteInstance', () => {
  async function deleteWith(widget: (query: string) => Response): Promise<string[]> {
    const queries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      queries.push(query)
      const index = indexResponse(query)
      if (index) return index
      return widget(query)
    }))
    await api.deleteInstance('demo')
    return queries
  }

  it('is idempotent when the instance is already absent during discovery', async () => {
    setContext({ tenant: 'delete-instance-absent', token: 'absent-token' })
    const queries = await deleteWith(query => {
      if (query.includes('WidgetYaml')) return graphqlError('widgets.infrastructure.faros.sh "demo" not found')
      throw new Error(`unexpected query: ${query}`)
    })

    expect(queries.some(query => query.includes('deleteWidget'))).toBe(false)
  })

  it('does not claim absence while an instance kind is not probeable', async () => {
    setContext({ tenant: 'delete-instance-api-pending', token: 'api-pending-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) {
        return response({ data: { __type: { fields: [{
          name: 'infrastructure_faros_sh',
          type: { fields: [{ name: 'v1alpha1', type: { fields: [] } }] },
        }] } } })
      }
      if (query.includes('Templates')) return templateListResponse(readyTemplateStatus())
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.deleteInstance('demo', 'widget')).rejects.toMatchObject({ reason: 'ProviderNotReady' })
  })

  it('is idempotent when a successful delete response was lost', async () => {
    setContext({ tenant: 'delete-instance-lost-response', token: 'lost-token' })
    const queries = await deleteWith(query => {
      if (query.includes('WidgetYaml')) {
        return response({ data: { infrastructure_faros_sh: { v1alpha1: {
          WidgetYaml: JSON.stringify({
            apiVersion: 'infrastructure.faros.sh/v1alpha1',
            kind: 'Widget',
            metadata: { name: 'demo', labels: { 'faros.sh/template': 'widget' } },
            spec: {},
          }),
        } } } })
      }
      if (query.includes('deleteWidget')) return graphqlError('widgets.infrastructure.faros.sh "demo" not found')
      throw new Error(`unexpected query: ${query}`)
    })

    expect(queries.some(query => query.includes('deleteWidget'))).toBe(true)
  })

  it('does not hide a lookalike delete error', async () => {
    setContext({ tenant: 'delete-instance-lookalike', token: 'delete-lookalike-token' })
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      const index = indexResponse(query)
      if (index) return index
      if (query.includes('WidgetYaml')) {
        return response({ data: { infrastructure_faros_sh: { v1alpha1: {
          WidgetYaml: JSON.stringify({
            apiVersion: 'infrastructure.faros.sh/v1alpha1',
            kind: 'Widget',
            metadata: { name: 'demo', labels: { 'faros.sh/template': 'widget' } },
            spec: {},
          }),
        } } } })
      }
      if (query.includes('deleteWidget')) {
        return graphqlError('widgets.infrastructure.faros.sh "demo" not found while resolving authorization')
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.deleteInstance('demo')).rejects.toMatchObject({ reason: 'GraphQLError' })
  })
})

describe('instance identity', () => {
  it('requires the template when the same name exists under multiple kinds', async () => {
    setContext({ tenant: 'duplicate-instance-names', token: 'duplicate-instance-token' })
    const yamlQueries: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) {
        return response({ data: { __type: { fields: [{
          name: 'infrastructure_faros_sh',
          type: { fields: [{
            name: 'v1alpha1',
            type: { fields: [
              { name: 'Widget', type: { name: 'InfrastructureWidget' } },
              { name: 'Widgets', type: { name: 'InfrastructureWidgetList' } },
              { name: 'WidgetYaml', type: { name: 'String' } },
              { name: 'Application', type: { name: 'InfrastructureApplication' } },
              { name: 'Applications', type: { name: 'InfrastructureApplicationList' } },
              { name: 'ApplicationYaml', type: { name: 'String' } },
            ] },
          }] },
        }] } } })
      }
      if (query.includes('Templates')) {
        const template = (name: string, resource: string, kind: string) => ({
          metadata: { name },
          spec: {
            displayName: name,
            instanceCRD: { group: 'infrastructure.faros.sh', version: 'v1alpha1', resource, kind },
            schema: JSON.stringify({ type: 'object' }),
          },
        })
        return response({ data: { infrastructure_faros_sh: { v1alpha1: { Templates: { items: [
          template('widget', 'widgets', 'Widget'),
          template('application', 'applications', 'Application'),
          template('application-alias', 'applications', 'Application'),
        ] } } } } })
      }
      if (query.includes('WidgetYaml') || query.includes('ApplicationYaml')) {
        yamlQueries.push(query)
        const kind = query.includes('ApplicationYaml') ? 'Application' : 'Widget'
        const template = kind === 'Application' ? 'application' : 'widget'
        return response({ data: { infrastructure_faros_sh: { v1alpha1: {
          [`${kind}Yaml`]: JSON.stringify({
            apiVersion: 'infrastructure.faros.sh/v1alpha1',
            kind,
            metadata: { name: 'demo', labels: { 'faros.sh/template': template } },
            spec: {},
            status: null,
          }),
        } } } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    await expect(api.getInstance('demo')).rejects.toMatchObject({ reason: 'InstanceAmbiguous' })
    yamlQueries.length = 0

    await expect(api.getInstance('demo', 'application')).resolves.toMatchObject({
      name: 'demo',
      template: 'application',
    })
    expect(yamlQueries).toHaveLength(1)
    expect(yamlQueries[0]).toContain('ApplicationYaml')

    await expect(api.getInstance('demo', 'application-alias')).rejects.toMatchObject({
      reason: 'InstanceNotFound',
    })
  })
})

describe('authority discovery cache', () => {
  it('evicts the least-recently-used authority after eight identities', async () => {
    let introspections = 0
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const { query } = request(init)
      if (query.includes('__type')) {
        introspections += 1
        return response({ data: { __type: { fields: [{
          name: 'infrastructure_faros_sh',
          type: { fields: [{ name: 'v1alpha1', type: { fields: [] } }] },
        }] } } })
      }
      if (query.includes('Templates')) {
        return response({ data: { infrastructure_faros_sh: { v1alpha1: { Templates: { items: [] } } } } })
      }
      throw new Error(`unexpected query: ${query}`)
    }))

    const authorities = Array.from({ length: 9 }, () => ({}))
    for (let i = 0; i < authorities.length; i += 1) {
      await api.listInstances({
        tenant: `bounded-${i}`,
        token: `rotated-secret-${i}`,
        authority: authorities[i],
      })
    }
    await api.listInstances({
      tenant: 'bounded-8',
      token: 'rotated-secret-8',
      authority: authorities[8],
    })
    expect(introspections).toBe(9)

    await api.listInstances({
      tenant: 'bounded-0',
      token: 'rotated-secret-0',
      authority: authorities[0],
    })

    expect(introspections).toBe(10)
  })
})
