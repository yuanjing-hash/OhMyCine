export interface RuntimeLogFilters {
  from: string; to: string; keyword: string; level: string; module: string; component: string; pluginId: string
  requestId: string; taskId: string; libraryId: string; connectionId: string; storageId: string; downloaderId: string; scanRunId: string
}

const mapping: Array<[keyof RuntimeLogFilters, string]> = [
  ['from','from'],['to','to'],['keyword','keyword'],['level','level'],['module','module'],['component','component'],['pluginId','plugin_id'],
  ['requestId','request_id'],['taskId','task_id'],['libraryId','library_id'],['connectionId','connection_id'],['storageId','storage_id'],['downloaderId','downloader_id'],['scanRunId','scan_run_id'],
]

export function emptyRuntimeLogFilters(now = new Date()): RuntimeLogFilters {
  return { from: new Date(now.getTime()-24*60*60*1000).toISOString(), to: now.toISOString(), keyword:'',level:'',module:'',component:'',pluginId:'',requestId:'',taskId:'',libraryId:'',connectionId:'',storageId:'',downloaderId:'',scanRunId:'' }
}

export function filtersFromQuery(query: Record<string, unknown>, now = new Date()): RuntimeLogFilters {
  const filters = emptyRuntimeLogFilters(now)
  for (const [field,key] of mapping) if (typeof query[key] === 'string' && query[key]) filters[field] = String(query[key])
  return filters
}

export function filtersToParams(filters: RuntimeLogFilters) {
  const params = new URLSearchParams()
  for (const [field,key] of mapping) if (filters[field].trim()) params.set(key, filters[field].trim())
  return params
}

export function filtersToQuery(filters: RuntimeLogFilters) {
  return Object.fromEntries(filtersToParams(filters).entries())
}
