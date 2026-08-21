import { describe, expect, test } from 'vitest'
import { emptyRuntimeLogFilters, filtersFromQuery, filtersToParams, filtersToQuery } from './runtime-logs'

describe('runtime log filters', () => {
  test('round trips only non-sensitive filter fields', () => {
    const filters=emptyRuntimeLogFilters(new Date('2026-08-13T12:00:00Z')); filters.module='storage';filters.operation='incremental_strm_generation';filters.pluginId='plugin.demo';filters.keyword='timeout'
    const query=filtersToQuery(filters);expect(query).toMatchObject({module:'storage',operation:'incremental_strm_generation',plugin_id:'plugin.demo',keyword:'timeout'});expect(JSON.stringify(query)).not.toContain('message')
    expect(filtersFromQuery(query)).toMatchObject({pluginId:'plugin.demo',operation:'incremental_strm_generation'})
  })
  test('serializes advanced correlation ids',()=>{const filters=emptyRuntimeLogFilters();filters.libraryId='7';filters.requestId='req-1';const params=filtersToParams(filters);expect(params.get('library_id')).toBe('7');expect(params.get('request_id')).toBe('req-1')})
})
