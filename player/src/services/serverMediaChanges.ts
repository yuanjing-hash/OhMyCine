export const SERVER_LIBRARY_REFRESH_EVENT = 'ohmycine:server-library-refresh'

export interface ServerLibraryRefreshDetail {
  sourceId: string
  libraryIds: string[]
  libraryRevisions: Record<string, number>
  resyncRequired: boolean
  version: number
}

export function dispatchServerLibraryRefresh(detail: ServerLibraryRefreshDetail) {
  window.dispatchEvent(new CustomEvent<ServerLibraryRefreshDetail>(SERVER_LIBRARY_REFRESH_EVENT, {
    detail: {
      ...detail,
      libraryIds: [...new Set(detail.libraryIds)],
      libraryRevisions: { ...detail.libraryRevisions },
    },
  }))
}
