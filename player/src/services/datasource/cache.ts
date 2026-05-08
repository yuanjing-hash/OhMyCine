export class SourceMetadataCache {
  private readonly entries = new Map<string, unknown>()

  async getOrSet<T>(key: string, loader: () => Promise<T>): Promise<T> {
    if (this.entries.has(key))
      return this.entries.get(key) as T

    const value = await loader()
    this.entries.set(key, value)
    return value
  }

  clear(): void {
    this.entries.clear()
  }

  get size(): number {
    return this.entries.size
  }
}
