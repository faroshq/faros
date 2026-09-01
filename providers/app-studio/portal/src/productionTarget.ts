import type { ProjectComponent, ProjectComponentMapping, ProductionTemplateComponent } from './types'

export function productionTargetMappingsComplete(
  targets: ProductionTemplateComponent[],
  projectComponents: ProjectComponent[],
  mappings: ProjectComponentMapping[],
): boolean {
  if (!targets.length) return false
  const targetNames = new Set(targets.map((component) => component.name))
  const projectNames = new Set(projectComponents.map((component) => component.name))
  const complete = mappings.filter((mapping) => mapping.componentRef.trim() && mapping.targetComponent.trim())
  return complete.length === targetNames.size &&
    new Set(complete.map((mapping) => mapping.targetComponent)).size === targetNames.size &&
    new Set(complete.map((mapping) => mapping.componentRef)).size === complete.length &&
    complete.every((mapping) => targetNames.has(mapping.targetComponent) && projectNames.has(mapping.componentRef))
}

export function updateProductionTargetMapping(
  mappings: ProjectComponentMapping[],
  targetComponent: string,
  componentRef: string,
): ProjectComponentMapping[] {
  const target = targetComponent.trim()
  const source = componentRef.trim()
  return [
    ...mappings.filter((mapping) => mapping.targetComponent !== target && (!source || mapping.componentRef !== source)),
    ...(source ? [{ targetComponent: target, componentRef: source }] : []),
  ].sort((left, right) => left.targetComponent.localeCompare(right.targetComponent))
}
