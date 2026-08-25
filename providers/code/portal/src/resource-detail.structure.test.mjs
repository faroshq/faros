import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const readSource = (file) => readFileSync(resolve(process.cwd(), 'src', file), 'utf8')
const app = readSource('App.vue')
const detail = readSource('views/RepoDetailView.vue')
const style = readSource('style.css')
const sectionCard = readSource('portalkit/ResourceSectionCard.vue')
const statCards = readSource('portalkit/ResourceStatCards.vue')
const farosUI = readSource('portalkit/faros-ui.css')

describe('Code repository resource detail cards', () => {
  it('hides provider tabs for repository detail and preserves the backlink', () => {
    expect(app).toMatch(/<template v-if="!route\.repo">[\s\S]*<Tabs :tabs=/)
    expect(detail).toMatch(/<a class="k-btn k-btn--ghost repo-detail__back" href="\/providers\/code\/repositories" @click\.prevent="emit\('back'\)"[^>]*>[\s\S]*<ArrowLeft/)
    expect(detail).not.toMatch(/:breadcrumbs=|@navigate=|<Tabs\b/)
  })

  it('keeps the fixed header action order and current read/delete behavior', () => {
    expect(detail).toMatch(/const detailRefreshing = computed\(\(\) =>[\s\S]*packagesLoading\.value/)
    expect(detail).toMatch(/<template #meta>[\s\S]*Repository[\s\S]*StatusBadge/)
    expect(detail).toMatch(/const repositoryTitle = computed\([\s\S]*repositoryOwner\.value \? `\$\{repositoryOwner\.value\}\/\$\{repositoryName\}`/)
    expect(detail).toMatch(/:title="repositoryTitle"/)
    expect(detail).toMatch(/<div class="repo-detail__provider-mark"[\s\S]*<Github v-if="isGitHubProvider"[\s\S]*<GitBranch v-else/)
    expect(detail).toMatch(/<div class="repo-detail__actions" role="group" aria-label="Repository actions">/)
    expect(detail).toMatch(/Open repository[\s\S]*k-btn--primary/)
    expect(detail).toMatch(/class="k-btn k-btn--ghost"[\s\S]*@click="loadAll"/)
    expect(detail).toMatch(/<details ref="actionsMenu" class="repo-detail__menu">[\s\S]*Delete repository/)
    expect(detail).toMatch(/@retry="loadRepository"/)
    expect(detail).toMatch(/async function deleteRepository\(\)/)
    expect(detail).toMatch(/confirmDialog\(\{[\s\S]*danger: true/)
    expect(detail).toMatch(/api\.deleteRepository\(current\.name\)/)
    expect(detail).toMatch(/props\.deletions\.acknowledge\(repositoryScope, current\.name, current\.uid\)/)
  })

  it('uses compact provider-owned stat cards and canonical section cards', () => {
    expect(sectionCard).toMatch(/class="k-resource-section-card"/)
    expect(sectionCard).toMatch(/class="k-resource-section-card__actions"/)
    expect(sectionCard).toMatch(/class="k-resource-section-card__body"/)
    expect(statCards).toMatch(/interface ResourceStatCard/)
    expect(farosUI).toMatch(/\.k-resource-stat-cards\s*\{[\s\S]*grid-template-columns: repeat\(3/)
    expect(farosUI).toMatch(/\.k-resource-stat-cards--compact \.k-resource-stat-card\s*\{[\s\S]*min-height:/)
    expect(detail).toMatch(/import ResourceStatCards, \{ type ResourceStatCard \}/)
    expect(detail).toMatch(/import ResourceSectionCard from '..\/portalkit\/ResourceSectionCard\.vue'/)
    expect(detail).toMatch(/const repositoryStatCards = computed<ResourceStatCard\[\]>\(\(\) => \[/)
    expect(detail).toMatch(/<template #summary>[\s\S]*<ResourceStatCards :cards="repositoryStatCards" density="compact" aria-label="Repository summary" \/>/)
    expect(detail).toMatch(/id: 'integration'[\s\S]*id: 'provider'[\s\S]*id: 'type'[\s\S]*id: 'owner'[\s\S]*id: 'default-branch'[\s\S]*id: 'visibility'/)
    expect(detail).not.toMatch(/repo-overview|repository-overview|repo-detail-section|repo-section-summary|Resource details/)
    expect(detail).toMatch(/<ResourceSectionCard\s+id="repository-integration"[\s\S]*repo-integration-card__description/)
    expect(detail).toMatch(/<ResourceSectionCard id="repository-access"[\s\S]*#actions/)
    expect(detail).toMatch(/<ResourceSectionCard id="repository-packages"[\s\S]*#actions/)
    expect(detail).toMatch(/<ResourceSectionCard id="repository-conditions"[\s\S]*title="Health"/)
    expect(style).toMatch(/\.repo-detail__sections\s*\{[\s\S]*gap:/)
    expect(detail).toMatch(/<div v-if="connectionExpanded" id="repository-integration-editor" class="repo-integration-editor">/)
    expect(detail).toMatch(/<div v-if="accessExpanded" id="repository-access-content" class="grid-2">/)
    expect(detail).toMatch(/<div v-if="repo && packagesExpanded" id="repository-packages-content" class="repo-domain-block">/)
    expect(detail).not.toMatch(/technicalExpanded|repository-technical|repo-technical/)
  })

  it('keeps repository health in an always-visible conditions card', () => {
    const integration = detail.match(/<ResourceSectionCard\s+id="repository-integration"[\s\S]*?<\/ResourceSectionCard>/)?.[0] ?? ''
    const conditions = detail.match(/<ResourceSectionCard\s+id="repository-conditions"[\s\S]*?<\/ResourceSectionCard>/)?.[0] ?? ''
    expect(integration).toMatch(/<p class="repo-integration-card__description">/)
    expect(integration).toMatch(/class="repo-integration-card__connection"/)
    expect(integration).toMatch(/integrationHealth/)
    expect(integration).toMatch(/:aria-expanded="connectionExpanded" aria-controls="repository-integration-editor"[\s\S]*Change/)
    expect(integration).not.toMatch(/API version|Generation|Labels|Clone URL|SSH URL|Faros ID/)
    expect(conditions).toMatch(/<ConditionsPanel[\s\S]*:conditions="repo\?\.conditions \|\| \[\]"/)
    expect(conditions).toMatch(/Provider status[\s\S]*Repository ID[\s\S]*Browser URL/)
    expect(detail).not.toMatch(/configurationRows|metadataRows|repositoryYaml|toYaml|YAML \/ read-only object|Technical details|technicalExpanded/)
    expect(detail).not.toMatch(/secretRef|privateKey|token/i)
    expect(detail).toMatch(/<div class="repo-section-card__facts" aria-label="Access counts">[\s\S]*deploy keys[\s\S]*collaborators/)
    expect(detail).toMatch(/<div class="repo-section-card__facts" aria-label="Package summary">[\s\S]*visible[\s\S]*packagesSummaryStatus/)
    expect(detail).toMatch(/<ArrowLeftRight[\s\S]*Change/)
    expect(detail).toMatch(/<Users[\s\S]*Manage access/)
    expect(detail).toMatch(/<PackageOpen[\s\S]*View packages/)
  })
})
