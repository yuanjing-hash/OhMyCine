/** Fences asynchronous detail, season and progress reads after navigation. */
export class MediaDetailRequestScope {
  private generation = 0

  begin(sameContext: () => boolean): () => boolean {
    const generation = ++this.generation
    return () => generation === this.generation && sameContext()
  }

  invalidate() {
    this.generation += 1
  }
}
