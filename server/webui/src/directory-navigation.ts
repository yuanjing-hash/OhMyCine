export const FILESYSTEM_ROOTS_ENDPOINT = '/api/v1/filesystem/roots'
export const DOWNLOAD_STAGING_DIRECTORY_ENDPOINT = '/api/v1/settings/downloads/directory'

export function directoryPickerInitialEndpoint(initialEndpoint?: string) {
  return initialEndpoint || FILESYSTEM_ROOTS_ENDPOINT
}

export function directoryPickerFallbackEndpoint(initialEndpoint: string) {
  return initialEndpoint === FILESYSTEM_ROOTS_ENDPOINT ? null : FILESYSTEM_ROOTS_ENDPOINT
}

export function storageDirectoryEndpoint(storageId: number) {
  return `/api/v1/storages/${storageId}/directory`
}

export function directoryPickerBrowseEndpoint(storageId?: number | null, providerConnectionId?: number | null) {
  if (storageId) return storageDirectoryEndpoint(storageId)
  if (providerConnectionId) return `/api/v1/connections/${providerConnectionId}/directories`
  return '/api/v1/filesystem/directories'
}

export function displayedStagingPath(configuredPath: string, selectedPath: string) {
  return selectedPath || configuredPath
}

export function directoryRootLabel(platform: string | null | undefined) {
  return platform?.toLowerCase() === 'windows' ? '此电脑' : '文件系统'
}
