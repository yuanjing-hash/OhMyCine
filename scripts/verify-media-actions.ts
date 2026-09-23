import assert from 'node:assert/strict'
import type { MediaActionAdapter, MediaActionTarget } from '../src/services/mediaActions/types'
import { createCollectionMediaActionAdapter } from '../src/services/mediaActions/collectionAdapter'
import { MediaActionController } from '../src/services/mediaActions/controller'
import { createPlayedStateMediaActionAdapter } from '../src/services/mediaActions/playedStateAdapter'
import { createMediaActionTarget } from '../src/services/mediaActions/types'
import { areAllKnownPlayableChildrenCompleted, playbackCompletionKey } from '../src/services/playbackHistory'
import { annotateMissingCollectionSources } from '../src/services/mediaCollections'

const target = createMediaActionTarget({
  id: 'movie-1',
  sourceId: 'source-1',
  libraryId: 'library-1',
  name: 'Test Movie',
  type: 'movie',
  path: '/provider/path/that-must-not-cross-the-menu-boundary.mkv',
}, 'emby', 'Emby')

assert.deepEqual(target, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'emby',
  itemId: 'movie-1',
  libraryId: 'library-1',
  mediaType: 'movie',
  display: { name: 'Test Movie', sourceName: 'Emby' },
})
assert.equal(JSON.stringify(target).includes('provider/path'), false, 'target must not retain provider paths')

let mutationCount = 0
let finishMutation: (() => void) | undefined
let reportMutationStarted: (() => void) | undefined
const mutationStarted = new Promise<void>((resolveStarted) => {
  reportMutationStarted = resolveStarted
})
const adapter: MediaActionAdapter = {
  id: 'test-adapter',
  supports: () => true,
  resolve: () => [
    { action: 'favorite', availability: 'available' },
    { action: 'deleteMedia', availability: 'disabled', disabledReason: '没有删除权限', danger: 'destructive' },
  ],
  execute: async (_target: MediaActionTarget) => {
    mutationCount += 1
    reportMutationStarted?.()
    await new Promise<void>((resolveMutation) => {
      finishMutation = resolveMutation
    })
    return { message: '收藏完成' }
  },
}
const controller = new MediaActionController({ adapters: [adapter] })
const resolved = await controller.resolve(target)
assert.deepEqual(resolved.map(action => action.action), ['favorite', 'deleteMedia'])
assert.equal(resolved[1]?.disabledReason, '没有删除权限')
assert.equal(resolved.some(action => action.action === 'download'), false, 'unsupported actions must remain hidden')

const first = controller.execute(target, 'favorite')
const duplicate = controller.execute(target, 'favorite')
assert.equal(first, duplicate, 'duplicate mutations must share one in-flight execution')
await mutationStarted
assert.equal(mutationCount, 1)
finishMutation?.()
assert.equal((await first).status, 'completed')

let unsafeError = ''
const failingController = new MediaActionController({
  adapters: [{
    ...adapter,
    id: 'failing-adapter',
    resolve: () => [{ action: 'favorite', availability: 'available' }],
    execute: () => { throw new Error('https://example.test/video?token=secret Authorization: Bearer-secret') },
  }],
  onFeedback: feedback => unsafeError = feedback.message,
})
await failingController.execute(target, 'favorite')
assert.equal(unsafeError.includes('secret'), false, 'action errors must be redacted')

const completed = new Set([playbackCompletionKey('source-1', 'episode-1')])
const seriesChildren = [{
  id: 'season-1', sourceId: 'source-1', name: 'Season 1', type: 'season' as const, path: 'season-1', children: [
    { id: 'episode-1', sourceId: 'source-1', name: 'Episode 1', type: 'episode' as const, path: 'episode-1' },
    { id: 'episode-2', sourceId: 'source-1', name: 'Episode 2', type: 'episode' as const, path: 'episode-2' },
  ],
}]
assert.equal(areAllKnownPlayableChildrenCompleted(seriesChildren, completed), false, 'one completed episode must not complete a season')
completed.add(playbackCompletionKey('source-1', 'episode-2'))
assert.equal(areAllKnownPlayableChildrenCompleted(seriesChildren, completed), true, 'all known playable children complete the aggregate')
assert.equal(areAllKnownPlayableChildrenCompleted([], completed), false, 'an aggregate without known playable children is not completed')

let providerMutation = ''
const playedAdapter = createPlayedStateMediaActionAdapter({
  resolveSource: () => ({ setPlayedState: async (_itemId, mutation) => providerMutation = mutation } as never),
})
const embyTarget = { ...target, sourceType: 'emby' as const }
assert.deepEqual((await playedAdapter.resolve(embyTarget)).map(capability => capability.action), ['markPlayed'])
await playedAdapter.execute(embyTarget, 'markPlayed')
assert.equal(providerMutation, 'played')

let liveFavoriteState = true
let favoriteMutation: boolean | undefined
const collectionAdapter = createCollectionMediaActionAdapter(() => ({
  getFavoriteState: async () => liveFavoriteState,
  setFavorite: async (_itemId, favorite) => { favoriteMutation = favorite },
} as never))
assert.deepEqual(
  (await collectionAdapter.resolve(embyTarget)).map(capability => capability.action),
  ['unfavorite', 'addToPlaylist', 'addToCollection'],
  'an Emby item that is currently favorited must expose only unfavorite',
)
const unfavoriteResult = await collectionAdapter.execute(embyTarget, 'unfavorite')
assert.equal(favoriteMutation, false, 'unfavorite must write the provider-native false state')
assert.equal(unfavoriteResult?.invalidations?.[0]?.scopes.includes('collections'), true, 'favorite mutations must refresh the favorites page')
liveFavoriteState = false
assert.equal((await collectionAdapter.resolve(embyTarget))[0]?.action, 'favorite')

const annotated = annotateMissingCollectionSources([{ id: 'local-favorites', name: '收藏', kind: 'favorite', members: [
  { sourceId: 'present', itemId: '1', title: 'Present', mediaType: 'movie', position: 0 },
  { sourceId: 'missing', itemId: '2', title: 'Missing', mediaType: 'movie', position: 0 },
] }], new Set(['present']))
assert.equal(annotated[0]?.members[0]?.missing, false)
assert.equal(annotated[0]?.members[1]?.missing, true)

console.log('media action behavior verification passed')
