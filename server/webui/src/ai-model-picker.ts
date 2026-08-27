import type { AIProviderModel } from '@/types/api'

export function filterAIModels(models: readonly AIProviderModel[], query: string): AIProviderModel[] {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return [...models]
  return models.filter(model => model.id.toLocaleLowerCase().includes(needle) || model.display_name.toLocaleLowerCase().includes(needle))
}
