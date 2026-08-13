import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { MediaActionAdapter, MediaActionTarget } from '../src/services/mediaActions/types'
import { createCollectionMediaActionAdapter } from '../src/services/mediaActions/collectionAdapter'
import { MediaActionController } from '../src/services/mediaActions/controller'
import { createPlayedStateMediaActionAdapter } from '../src/services/mediaActions/playedStateAdapter'
import { createMediaActionTarget } from '../src/services/mediaActions/types'
import { areAllKnownPlayableChildrenCompleted, playbackCompletionKey } from '../src/services/playbackHistory'
import { annotateMissingCollectionSources } from '../src/services/mediaCollections'

const root = resolve(import.meta.dirname, '..')

const target = createMediaActionTarget({
  id: 'movie-1',
  sourceId: 'source-1',
  libraryId: 'library-1',
  name: 'Test Movie',
  type: 'movie',
  path: '/provider/path/that-must-not-cross-the-menu-boundary.mkv',
}, 'alist', 'OpenList')

assert.deepEqual(target, {
  kind: 'media',
  sourceId: 'source-1',
  sourceType: 'alist',
  itemId: 'movie-1',
  libraryId: 'library-1',
  mediaType: 'movie',
  display: { name: 'Test Movie', sourceName: 'OpenList' },
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

const menuSource = readFileSync(resolve(root, 'src/components/media/MediaActionMenu.vue'), 'utf8')
const hostSource = readFileSync(resolve(root, 'src/components/media/MediaActionHost.vue'), 'utf8')
const inputSource = readFileSync(resolve(root, 'src/services/mediaActions/input.ts'), 'utf8')
const cardSource = readFileSync(resolve(root, 'src/components/media/MediaCard.vue'), 'utf8')
const playerSource = readFileSync(resolve(root, 'src/views/PlayerView.vue'), 'utf8')
const mobileControlsSource = readFileSync(resolve(root, 'src/components/player/MobilePlayerControls.vue'), 'utf8')
const appSource = readFileSync(resolve(root, 'src/App.vue'), 'utf8')
const historyRustSource = readFileSync(resolve(root, 'src-tauri/src/commands/history.rs'), 'utf8')
const sourceViewSource = readFileSync(resolve(root, 'src/views/SourceLibraryView.vue'), 'utf8')
const maintenanceSource = readFileSync(resolve(root, 'src/services/mediaActions/maintenanceAdapter.ts'), 'utf8')
const androidLocalFileSource = readFileSync(resolve(root, 'src-tauri/src/commands/local_file_android.rs'), 'utf8')
assert.match(menuSource, /ArrowDown/)
assert.match(menuSource, /disabledReason/)
assert.match(hostSource, /presentation/)
assert.match(hostSource, /MediaActionConfirmationDialog/)
assert.match(inputSource, /LONG_PRESS_DELAY_MS = 520/)
assert.match(inputSource, /LONG_PRESS_MOVEMENT_PX = 12/)
assert.match(inputSource, /handleMediaActionKeyboard/)
assert.match(inputSource, /event\.key === 'ContextMenu'/)
assert.match(inputSource, /event\.shiftKey && event\.key === 'F10'/)
assert.match(inputSource, /window\.addEventListener\('scroll', cancelAllPendingLongPresses, true\)/)
assert.match(cardSource, /beginMediaActionLongPress/)
assert.match(cardSource, /suppressMediaActionClick/)
assert.match(cardSource, /contextMenuMode === 'custom'/)
assert.match(playerSource, /suppressPlayerContextMenuUntil/)
assert.match(playerSource, /pointerType === 'mouse' && event\.button === 2/)
assert.match(playerSource, /touchGestureSession !== null/)
assert.doesNotMatch(playerSource, /event\.detail > 0/)
assert.match(mobileControlsSource, /openPlaybackDetail/)
assert.match(mobileControlsSource, /navigateSettings/)
assert.match(appSource, /document\.addEventListener\('contextmenu', suppressNativeContextMenu\)/)
assert.match(readFileSync(resolve(root, 'src/views/HomeView.vue'), 'utf8'), /tabindex="0"/)
assert.match(readFileSync(resolve(root, 'src/views/HomeView.vue'), 'utf8'), /handleHomeCardKey/)
assert.match(readFileSync(resolve(root, 'src/components/media/GlobalSearchWorkspace.vue'), 'utf8'), /handleSearchItemKey/)
assert.match(historyRustSource, /completed = 0/)
assert.match(historyRustSource, /completion_state_and_continue_removal_are_independent/)
assert.doesNotMatch(sourceViewSource, /work-context-menu/)
assert.match(sourceViewSource, /registerMaintenanceHandler/)
assert.match(maintenanceSource, /refreshMetadata/)
assert.match(maintenanceSource, /rescanLibrary/)
assert.match(maintenanceSource, /isScanOwnedTarget/)
assert.match(maintenanceSource, /cache\.candidates\.some/)
assert.match(maintenanceSource, /getRawScannedMediaDetail/)
assert.match(maintenanceSource, /editSubtitles/)
const editorHostSource = readFileSync(resolve(root, 'src/components/media/MediaEditorHost.vue'), 'utf8')
const mediaEditingSource = readFileSync(resolve(root, 'src/services/mediaEditing.ts'), 'utf8')
assert.match(appSource, /MediaEditorHost/)
assert.match(editorHostSource, /Player 数据库和受控缓存/)
assert.match(editorHostSource, /updateMetadata/)
assert.match(editorHostSource, /updateArtworkFromUrl/)
assert.match(editorHostSource, /downloadAndSelectLocalSubtitle/)
assert.match(mediaEditingSource, /saveRawSourceScanCache/)
assert.match(mediaEditingSource, /saveMediaPlaybackPreference/)
assert.doesNotMatch(mediaEditingSource, /writeFile|rename|move/)
assert.match(androidLocalFileSource, /pub async fn local_file_delete_owned/)
assert.match(androidLocalFileSource, /\.run::<Value>\(\s*"delete"/)
assert.match(androidLocalFileSource, /pub\(crate\) fn resolve_local_download_source/)
const favoritesSource = readFileSync(resolve(root, 'src/views/FavoritesView.vue'), 'utf8')
const embySource = readFileSync(resolve(root, 'src/services/datasource/emby.ts'), 'utf8')
const collectionAdapterSource = readFileSync(resolve(root, 'src/services/mediaActions/collectionAdapter.ts'), 'utf8')
assert.match(embySource, /Filters: 'IsFavorite'/)
assert.match(embySource, /EnableUserData: 'true'/)
assert.doesNotMatch(embySource, /Fields: 'UserData'/)
assert.match(embySource, /getFavoriteState/)
assert.match(collectionAdapterSource, /source\.getFavoriteState/)
assert.match(favoritesSource, /Player 本地收藏/)
assert.match(favoritesSource, /媒体服务原生/)
assert.match(appSource, /COLLECTIONS_CHANGED_EVENT/)

console.log('media action capability/controller/input/UI contract verification passed')
