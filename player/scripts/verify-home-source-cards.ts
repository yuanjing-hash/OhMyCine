import assert from 'node:assert/strict'
import { deriveHomeLatestEmptyState, deriveHomeSourceCards } from '../src/services/datasource/homeSourceCards.ts'
import type { DataSourceConfig } from '../src/services/datasource/types.ts'

const configs: DataSourceConfig[] = [
  {
    id: 'emby-home',
    type: 'emby',
    name: '家庭 Emby',
    displayName: '家庭 Emby',
    order: 0,
    url: 'https://emby.example.test',
    enabled: true,
    extra: { credentialRef: 'datasource:emby-home:emby-credential' },
  },
  {
    id: 'alist-unscanned',
    type: 'alist',
    name: 'OpenList/Alist',
    displayName: 'NAS OpenList',
    order: 1,
    url: 'https://alist.example.test',
    enabled: true,
    extra: {
      credentialRef: 'datasource:alist-unscanned:alist-credential',
      rootPath: '/影视库',
    },
  },
]

const cards = deriveHomeSourceCards(configs, {
  scanCacheReader: () => null,
})

const embyCard = cards.find(card => card.id === 'emby-home')
const openListCard = cards.find(card => card.id === 'alist-unscanned')

assert.equal(cards.length, 2)
assert.equal(embyCard?.isOpenable, true)
assert.equal(embyCard?.statusLabel, '可打开')
assert.equal(embyCard?.actionLabel, '打开媒体库')
assert.equal(openListCard?.isOpenable, true)
assert.equal(openListCard?.scanState, 'pending')
assert.equal(openListCard?.statusLabel, '等待自动索引')
assert.equal(openListCard?.actionLabel, '打开媒体库')
assert.equal(openListCard?.rootPath, '/影视库')

const latestEmptyState = deriveHomeLatestEmptyState(cards)
assert.equal(latestEmptyState.title, '等待 OpenList/Alist 自动索引')
assert.equal(latestEmptyState.actionLabel, '打开媒体库')
assert.equal(latestEmptyState.action.kind, 'source')
if (latestEmptyState.action.kind !== 'source')
  throw new Error('Expected source action for unscanned OpenList.')
assert.equal(latestEmptyState.action.sourceId, 'alist-unscanned')

console.log(JSON.stringify({
  cardCount: cards.length,
  embyOpenable: embyCard?.isOpenable,
  embyStatus: embyCard?.statusLabel,
  openListScanState: openListCard?.scanState,
  openListStatus: openListCard?.statusLabel,
  latestEmptyAction: latestEmptyState.actionLabel,
  latestEmptySourceId: latestEmptyState.action.sourceId,
}, null, 2))
