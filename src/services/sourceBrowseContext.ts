import type { MediaItem, MediaLibrary } from '@/services/datasource/types'

export type SourceBrowseViewMode = 'media-library' | 'folders'

export interface SourceBrowseNode {
  readonly id: string
  readonly name: string
  readonly type: MediaItem['type'] | MediaLibrary['type']
  readonly isSearch?: boolean
}

export interface SourceBrowseContext {
  readonly sourceId: string
  readonly viewMode: SourceBrowseViewMode
  readonly selectedLibrary: MediaLibrary | null
  readonly navigationStack: readonly SourceBrowseNode[]
  readonly selectedScannedCategoryId: string | null
  readonly searchKeyword: string
  readonly scrollTop: number
}

interface StoredSourceBrowseContext {
  readonly context: SourceBrowseContext
  readonly updatedAt: number
}

const CONTEXT_TTL_MS = 6 * 60 * 60 * 1000
const MAX_CONTEXTS = 64
const contexts = new Map<string, StoredSourceBrowseContext>()

export function saveSourceBrowseContext(
  context: SourceBrowseContext,
  existingContextId?: string | null,
): string {
  pruneSourceBrowseContexts()
  const contextId = existingContextId && contexts.has(existingContextId)
    ? existingContextId
    : createContextId()
  contexts.set(contextId, {
    context: cloneContext(context),
    updatedAt: Date.now(),
  })
  pruneSourceBrowseContexts()
  return contextId
}

export function loadSourceBrowseContext(
  contextId: string | null | undefined,
  sourceId: string,
): SourceBrowseContext | null {
  if (!contextId)
    return null
  const stored = contexts.get(contextId)
  if (!stored)
    return null
  if (Date.now() - stored.updatedAt > CONTEXT_TTL_MS) {
    contexts.delete(contextId)
    return null
  }
  if (stored.context.sourceId !== sourceId)
    return null
  return cloneContext(stored.context)
}

export function sourceBrowseContextIdFromQuery(value: unknown): string | null {
  if (typeof value === 'string' && value.trim())
    return value
  if (Array.isArray(value)) {
    const first = value.find(entry => typeof entry === 'string' && entry.trim())
    return typeof first === 'string' ? first : null
  }
  return null
}

function cloneContext(context: SourceBrowseContext): SourceBrowseContext {
  return {
    ...context,
    selectedLibrary: context.selectedLibrary ? { ...context.selectedLibrary } : null,
    navigationStack: context.navigationStack.map(node => ({ ...node })),
  }
}

function createContextId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function')
    return `browse-${crypto.randomUUID()}`
  return `browse-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

function pruneSourceBrowseContexts(): void {
  const oldestAllowed = Date.now() - CONTEXT_TTL_MS
  for (const [contextId, stored] of contexts) {
    if (stored.updatedAt < oldestAllowed)
      contexts.delete(contextId)
  }
  while (contexts.size > MAX_CONTEXTS) {
    const oldestContextId = contexts.keys().next().value
    if (typeof oldestContextId !== 'string')
      break
    contexts.delete(oldestContextId)
  }
}
