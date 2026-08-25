import type { DataSourceConfig } from '../datasource/types'
import type { RawFileSourceType } from './types'

export interface RawSourceCacheTarget {
  readonly sourceId: string
  readonly sourceType: RawFileSourceType
  readonly rootPath: string
}

const RAW_FILE_SOURCE_TYPES = new Set<RawFileSourceType>(['alist', 'clouddrive2', 'webdav', 'local', '115', '123', 'quark'])

export function changedRawSourceCacheTarget(
  previous: DataSourceConfig,
  next: DataSourceConfig,
): RawSourceCacheTarget | null {
  if (!isRawFileSourceType(previous.type))
    return null

  const previousPhysicalRoot = configuredRootPath(previous)
  const nextPhysicalRoot = configuredRootPath(next)
  if (previous.type === next.type && previousPhysicalRoot === nextPhysicalRoot)
    return null

  return {
    sourceId: previous.id,
    sourceType: previous.type,
    // Local providers expose `/` to the scanner even though their physical
    // root lives in config. Rebinding the same source id must clear that key.
    rootPath: previous.type === 'local' ? '/' : previousPhysicalRoot,
  }
}

function isRawFileSourceType(type: DataSourceConfig['type']): type is RawFileSourceType {
  return RAW_FILE_SOURCE_TYPES.has(type as RawFileSourceType)
}

function configuredRootPath(config: DataSourceConfig): string {
  const rootPath = typeof config.extra?.rootPath === 'string' ? config.extra.rootPath.trim() : ''
  return rootPath || '/'
}
