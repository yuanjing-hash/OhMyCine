export const SERVER_LIBRARY_REFRESH_EVENT = 'ohmycine:server-library-refresh'

export interface ServerLibraryRefreshDetail {
  sourceIds: string[]
}

export function dispatchServerLibraryRefresh(sourceIds: readonly string[]) {
  window.dispatchEvent(new CustomEvent<ServerLibraryRefreshDetail>(SERVER_LIBRARY_REFRESH_EVENT, {
    detail: { sourceIds: [...new Set(sourceIds)] },
  }))
}
