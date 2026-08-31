export const VIDEO_FILE_EXTENSIONS = [
  'mkv',
  'mp4',
  'avi',
  'mov',
  'wmv',
  'flv',
  'webm',
  'm4v',
  'ts',
  'm2ts',
  'rmvb',
  'mpg',
  'mpeg',
  '3gp',
  'ogv',
  'divx',
  'vob',
  'iso',
] as const

const VIDEO_FILE_EXTENSION_SET = new Set<string>(VIDEO_FILE_EXTENSIONS)
const URL_LIKE_PROVIDER_PATH_RE = /^(?:https?|webdav|ftp|sftp|file|blob):/i
const SENSITIVE_PROVIDER_PATH_QUERY_KEYS = new Set([
  'api_key',
  'apikey',
  'access_key',
  'access-token',
  'accesskeyid',
  'access_token',
  'auth_key',
  'authkey',
  'authorization',
  'awsaccesskeyid',
  'cookie',
  'expires',
  'exp',
  'ossaccesskeyid',
  'passkey',
  'password',
  'passwd',
  'pwd',
  'security-token',
  'sig',
  'sign',
  'signature',
  'token',
  'x-amz-credential',
  'x-amz-expires',
  'x-amz-security-token',
  'x-amz-signature',
  'x-oss-credential',
  'x-oss-signature',
])

export function isVideoFileExtension(extension: string): boolean {
  const normalized = extension.trim().replace(/^\./, '').toLowerCase()
  return VIDEO_FILE_EXTENSION_SET.has(normalized)
}

export function getVideoFileExtension(value: string): string | null {
  const basename = safeProviderBasename(value) ?? value
  const match = /\.([a-z0-9]{1,12})$/i.exec(basename)
  if (!match)
    return null

  const extension = match[1].toLowerCase()
  return isVideoFileExtension(extension) ? extension : null
}

export function isVideoFileName(value: string): boolean {
  return getVideoFileExtension(value) != null
}

export function isLikelySensitiveProviderPath(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed)
    return false

  if (URL_LIKE_PROVIDER_PATH_RE.test(trimmed))
    return true

  const queryIndex = trimmed.indexOf('?')
  if (queryIndex < 0)
    return false

  const query = trimmed.slice(queryIndex + 1).split('#')[0]
  const params = new URLSearchParams(query)
  for (const key of params.keys()) {
    if (SENSITIVE_PROVIDER_PATH_QUERY_KEYS.has(key.toLowerCase()))
      return true
  }

  return false
}

export function normalizeProviderPath(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed)
    return '/'

  const rooted = trimmed.replace(/\\/g, '/').startsWith('/')
    ? trimmed.replace(/\\/g, '/')
    : `/${trimmed.replace(/\\/g, '/')}`
  const normalized = rooted.replace(/\/+/g, '/')
  const unsafeSegment = normalized
    .split('/')
    .filter(Boolean)
    .some(segment => isUnsafeRelativeSegment(segment))

  if (unsafeSegment)
    throw new Error('Provider path contains unsafe relative segments.')

  return normalized === '/' ? '/' : normalized.replace(/\/+$/, '')
}

export function tryNormalizeProviderPath(value: string | undefined): string | null {
  try {
    return normalizeProviderPath(value)
  }
  catch {
    return null
  }
}

export function isPathWithinRoot(path: string, rootPath: string): boolean {
  const normalizedPath = normalizeProviderPath(path)
  const normalizedRoot = normalizeProviderPath(rootPath)
  return normalizedRoot === '/'
    || normalizedPath === normalizedRoot
    || normalizedPath.startsWith(`${normalizedRoot}/`)
}

export function relativeProviderPath(path: string, rootPath: string): string {
  const normalizedPath = normalizeProviderPath(path)
  const normalizedRoot = normalizeProviderPath(rootPath)

  if (!isPathWithinRoot(normalizedPath, normalizedRoot))
    throw new Error('Provider path is outside the selected root.')

  if (normalizedPath === normalizedRoot)
    return ''

  return normalizedRoot === '/'
    ? normalizedPath.slice(1)
    : normalizedPath.slice(normalizedRoot.length + 1)
}

export function joinProviderPath(parentPath: string, name: string): string {
  const parent = normalizeProviderPath(parentPath)
  const trimmedName = name.trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  if (!trimmedName)
    return parent
  return normalizeProviderPath(`${parent}/${trimmedName}`)
}

export function providerParentPath(path: string): string {
  const normalized = normalizeProviderPath(path)
  if (normalized === '/')
    return '/'
  const index = normalized.lastIndexOf('/')
  return index <= 0 ? '/' : normalized.slice(0, index)
}

export function providerBasename(path: string | undefined): string | undefined {
  if (!path)
    return undefined
  const normalized = normalizeProviderPath(path)
  return normalized.split('/').filter(Boolean).at(-1)
}

export function splitProviderPath(path: string): string[] {
  return normalizeProviderPath(path).split('/').filter(Boolean)
}

export function stripFileExtension(value: string): string {
  return value.replace(/\.[a-z0-9]{1,12}$/i, '')
}

function safeProviderBasename(path: string): string | undefined {
  try {
    return providerBasename(path)
  }
  catch {
    return path.replace(/\\/g, '/').split('/').filter(Boolean).at(-1)
  }
}

function isUnsafeRelativeSegment(segment: string): boolean {
  let current = segment
  for (let attempt = 0; attempt < 2; attempt += 1) {
    if (current === '.' || current === '..')
      return true

    try {
      const decoded = decodeURIComponent(current)
      if (decoded === current)
        return false
      current = decoded
    }
    catch {
      return false
    }
  }

  return current === '.' || current === '..'
}
