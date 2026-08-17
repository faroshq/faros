import { describe, expect, it } from 'vitest'

import { createResourceTombstones } from './refresh'

describe('resource tombstones', () => {
  it('keeps a Back or direct-route stale same-UID read marked until list absence', () => {
    const tombstones = createResourceTombstones()

    tombstones.add('demo', 'old-uid')
    expect(tombstones.has('demo', 'old-uid')).toBe(true)

    // A newly mounted detail/list route sees the same shared marker, so a
    // stale read cannot repaint the acknowledged UID as active.
    tombstones.reconcile([{ name: 'demo', uid: 'old-uid' }])
    expect(tombstones.has('demo', 'old-uid')).toBe(true)

    tombstones.reconcile([])
    expect(tombstones.has('demo', 'old-uid')).toBe(false)
  })

  it('retains a tombstone through termination and stale snapshots until true absence', () => {
    const tombstones = createResourceTombstones()

    tombstones.add('demo', 'old-uid')

    // listInstances renders this terminating resource as Deleting and returns
    // its raw identity for marker reconciliation.
    tombstones.reconcile([{ name: 'demo', uid: 'old-uid' }])
    expect(tombstones.has('demo', 'old-uid')).toBe(true)

    // An older list snapshot that still presents the object as active must not
    // resurrect the acknowledged deletion.
    tombstones.reconcile([{ name: 'demo', uid: 'old-uid' }])
    expect(tombstones.has('demo', 'old-uid')).toBe(true)

    tombstones.reconcile([])
    expect(tombstones.has('demo', 'old-uid')).toBe(false)

    tombstones.reconcile([{ name: 'demo', uid: 'new-uid' }])
    expect(tombstones.has('demo', 'new-uid')).toBe(false)
  })

  it('reveals a same-name replacement with a different UID', () => {
    const tombstones = createResourceTombstones()
    tombstones.add('demo', 'old-uid')

    tombstones.reconcile([{ name: 'demo', uid: 'new-uid' }])

    expect(tombstones.has('demo', 'new-uid')).toBe(false)
  })

  it('clears acknowledged deletions when authority changes', () => {
    const tombstones = createResourceTombstones()
    tombstones.add('demo', 'old-uid')

    tombstones.clear()

    expect(tombstones.has('demo', 'old-uid')).toBe(false)
  })
})
