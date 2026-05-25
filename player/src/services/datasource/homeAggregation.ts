import type { DataSource, HomeSection } from './types'

export async function collectHomeSectionsFromSources(sources: readonly DataSource[]): Promise<HomeSection[]> {
  const settled = await Promise.allSettled(
    sources.map(source => collectHomeSectionsFromSource(source)),
  )

  return settled.flatMap(result => result.status === 'fulfilled' ? result.value : [])
}

async function collectHomeSectionsFromSource(source: DataSource): Promise<HomeSection[]> {
  if (!source.getHomeSections)
    return []

  try {
    const sections = await source.getHomeSections()
    return sections.filter(hasVisibleHomeItems)
  }
  catch {
    return []
  }
}

function hasVisibleHomeItems(section: HomeSection): boolean {
  return section.items.length > 0
}
