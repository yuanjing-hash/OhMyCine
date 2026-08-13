export const FILESYSTEM_ROOTS_ENDPOINT = '/api/v1/filesystem/roots'

export function directoryRootLabel(platform: string | null | undefined) {
  return platform?.toLowerCase() === 'windows' ? '此电脑' : '文件系统'
}
