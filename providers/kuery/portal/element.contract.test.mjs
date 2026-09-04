import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const element = readFileSync(new URL('./src/element.ts', import.meta.url), 'utf8')
const app = readFileSync(new URL('./src/App.vue', import.meta.url), 'utf8')
const inventory = readFileSync(new URL('./src/components/InventoryView.vue', import.meta.url), 'utf8')
const playground = readFileSync(new URL('./src/components/PlaygroundView.vue', import.meta.url), 'utf8')
const impact = readFileSync(new URL('./src/components/ImpactView.vue', import.meta.url), 'utf8')

test('the provider contract is a thin reactive light-DOM Vue mount', () => {
  assert.match(element, /createApp\(App, \{ state: this\.state \}\)/u)
  assert.match(element, /this\.app\.mount\(this\)/u)
  assert.match(element, /this\.app\?\.unmount\(\)/u)
  assert.match(element, /set farosContext[\s\S]*this\.state\.context = value/u)
})

test('top-level navigation and inventory use PortalKit contracts', () => {
  assert.match(app, /import Tabs from '\.\/portalkit\/Tabs\.vue'/u)
  assert.match(app, /<Tabs[^>]*:active="active"/u)
  assert.match(inventory, /import ResourceTable from '\.\.\/portalkit\/ResourceTable\.vue'/u)
  assert.match(inventory, /pageSize: request\.pageSize, cursor: request\.cursor, count: true, filters: request\.filters/u)
  assert.match(inventory, /searchable search-placeholder="Exact resource name…"/u)
  assert.match(inventory, /pagination-mode="server"/u)
  assert.match(inventory, /:page-info="pager\.pageInfo"/u)
  assert.match(inventory, /@submit\.prevent="applyFacetFilters"/u)
  assert.match(inventory, /id="inventory-kind-filter"/u)
  assert.match(inventory, /id="inventory-namespace-filter"/u)
  assert.doesNotMatch(inventory, /learned from pages you visit/u)
  assert.match(inventory, /@row-click="inspect"/u)
})

test('playground exposes labeled editor and live result status', () => {
  assert.match(playground, /id="query-editor-label"/u)
  assert.match(playground, /aria-labelledby="query-editor-label"/u)
  assert.match(playground, /role="status" aria-live="polite"/u)
  assert.match(playground, /Correct the QuerySpec and run it again/u)
})

test('impact drill-down preserves mounted tab state and falls back to the host route', () => {
  assert.match(app, /<ImpactView v-if="impact"/u)
  assert.match(app, /<div v-show="!impact" class="kuery-collection-surfaces">/u)
  assert.doesNotMatch(app, /<template v-else>/u)
  assert.match(app, /:active="!impact && active === 'topology'"/u)
  assert.match(app, /:active="!impact && active === 'playground'"/u)
  assert.match(impact, /<ResourceBackLink href="\/providers\/kuery"/u)
  assert.doesNotMatch(impact, /href="\/ui\/providers\/kuery"/u)
})

test('edge discovery fences late responses to the request and context that started them', () => {
  assert.match(app, /let edgesRequestID = 0/u)
  assert.match(app, /const requestID = \+\+edgesRequestID/u)
  assert.match(app, /const requestContext = context\.value/u)
  assert.match(app, /const isCurrent = \(\): boolean =>[\s\S]*edgesRequestID === requestID[\s\S]*identity\.value === requestIdentity[\s\S]*\(context\.value\?\.token \?\? null\) === requestToken/u)
  assert.match(app, /if \(!isCurrent\(\)\) return[\s\S]*edges\.value = parsed\.edges/u)
  assert.match(app, /if \(isCurrent\(\)\) \{[\s\S]*edgesLoading\.value = false/u)
  assert.match(app, /watch\(\[identity, token\],[\s\S]*currentIdentity === previousIdentity && currentToken !== previousToken/u)
})

test('secondary views mount lazily and stay mounted after first visit', () => {
  assert.match(app, /const visited = ref<Record<TabID, boolean>>\(\{ topology: true, inventory: false, playground: false \}\)/u)
  assert.match(app, /visited\.value\[id\] = true/u)
  assert.match(app, /<InventoryView v-if="visited\.inventory" v-show="active === 'inventory'"/u)
  assert.match(app, /<PlaygroundView v-if="visited\.playground" v-show="active === 'playground'"/u)
  assert.doesNotMatch(app, /<InventoryView v-show="active === 'inventory'"/u)
  assert.doesNotMatch(app, /<PlaygroundView v-show="active === 'playground'"/u)
})
