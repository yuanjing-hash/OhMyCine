/** Server-owned artwork references are deliberately narrower than arbitrary URLs. */
export function isProtectedServerArtworkPath(pathname: string): boolean {
  return /^\/api\/v1\/player\/artwork\/[\w-]{1,4096}$/.test(pathname)
    || /^\/api\/v1\/player\/discovery\/images\/(?:tmdb|douban)\/[\w-]{1,4096}$/.test(pathname)
}

export function isProtectedServerArtworkURL(value: string): boolean {
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol)
      && !url.username && !url.password && !url.search && !url.hash
      && isProtectedServerArtworkPath(url.pathname)
  }
  catch {
    return false
  }
}

export function resolveServerArtworkURL(baseUrl: string, value: unknown): string | undefined {
  if (typeof value !== 'string')
    return undefined
  const reference = value.trim()
  if (!reference || reference.length > 4096)
    return undefined
  try {
    const server = new URL(baseUrl)
    const resolved = new URL(reference, `${server.origin}/`)
    if (resolved.origin !== server.origin || resolved.username || resolved.password || resolved.hash)
      return undefined
    if (isProtectedServerArtworkPath(resolved.pathname))
      return resolved.search ? undefined : resolved.toString()
    // Legacy library and category covers are Server assets. Their signed query
    // values are preserved, while unrelated API and external URLs are rejected.
    if (/^\/api\/v1\/assets\/(?:library-covers|plugin-covers|generated-library-covers)\/[\w.-]{1,256}$/.test(resolved.pathname)
      && !resolved.pathname.includes('..')) {
      return resolved.toString()
    }
  }
  catch {
    // Invalid Server data must never reach an image element.
  }
  return undefined
}
