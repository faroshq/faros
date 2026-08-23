import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const app = readFileSync(resolve(process.cwd(), 'src/App.vue'), 'utf8')
const styles = readFileSync(resolve(process.cwd(), 'src/style.css'), 'utf8')

describe('Edges route tabs', () => {
  it('uses PortalKit tabs for route navigation and keeps the route contract', () => {
    expect(app).toMatch(/import Tabs from '\.\/portalkit\/Tabs\.vue'/)
    expect(app).toMatch(/const edgeRouteTabs = \[[\s\S]*id: 'edges'[\s\S]*Server[\s\S]*id: 'workloads'[\s\S]*Boxes[\s\S]*id: 'services'[\s\S]*Plug/)

    const routeTabs = app.match(/<Tabs[\s\S]*?@select="\(id\) => navigate\(id === 'edges' \? '' : id\)"\s*\/>/)?.[0]
    expect(routeTabs).toBeTruthy()
    expect(routeTabs).toMatch(/v-if="!wizardOpen && !selected"/)
    expect(routeTabs).toMatch(/:tabs="edgeRouteTabs"/)
    expect(routeTabs).toMatch(/:active="view"/)
    expect(routeTabs).toMatch(/aria-label="Edges sections"/)
    expect(routeTabs).not.toMatch(/wiz-steps|wiz-step|style=/)
  })

  it('keeps wizard progression styling and overlay route guards intact', () => {
    expect(styles).toMatch(/faros-provider-edges \.edges-app \{[^}]*display: flex;[^}]*flex-direction: column;[^}]*gap: 16px;/)
    expect(app).toMatch(/<Workloads v-if="view === 'workloads' && !wizardOpen && !selected" \/>/)
    expect(app).toMatch(/<Services v-else-if="view === 'services' && !wizardOpen && !selected" \/>/)
    expect(app).toMatch(/<Wizard v-else-if="wizardOpen"/)
    expect(app).toMatch(/<Detail[\s\S]*v-else-if="selected"/)
    expect(styles).toMatch(/\.wiz-steps\s*\{[\s\S]*\.wiz-step\s*\{[\s\S]*\.wiz-step\.active\s*\{[\s\S]*\.wiz-step\.done\s*\{/)
  })
})
