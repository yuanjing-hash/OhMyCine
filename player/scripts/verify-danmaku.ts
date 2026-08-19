import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { inferDanmakuEpisode, safeMediaName, safeSearchKeyword } from '../src/services/danmaku/client'
import { parseDanmakuComments, parseDanmakuMatches, parseDanmakuSearch } from '../src/services/danmaku/parser'
import { sanitizeDanmakuSettings } from '../src/services/danmaku/settings'
import { findDanmakuTimelineWindow } from '../src/services/danmaku/timeline'
import type { DanmakuComment } from '../src/services/danmaku/types'
import { interpolatedDanmakuTime } from '../src/services/danmaku/clock'
import { resolveDanmakuMediaIdentity } from '../src/services/danmaku/identity'
import { normalizeDanmakuSeriesTitle, selectExactStructuredDanmakuMatch } from '../src/services/danmaku/selection'

const root = resolve(import.meta.dirname, '..')
const read = (path: string) => readFileSync(resolve(root, path), 'utf8')

const matchResponse = parseDanmakuMatches({ success: true, isMatched: true, matches: [{ episodeId: 123, animeId: 9, animeTitle: '作品', episodeTitle: '第1话', shift: 1.5 }] })
assert.equal(matchResponse.exact, true)
assert.equal(matchResponse.matches.length, 1)
assert.equal(matchResponse.matches[0].episodeId, 123)
assert.equal(parseDanmakuMatches({ success: true, isMatched: false, matches: matchResponse.matches }).exact, false)

const searchResponse = parseDanmakuSearch({ success: true, hasMore: true, animes: [{ animeId: 9, animeTitle: '作品', typeDescription: 'TV动画', episodes: [{ episodeId: 123, episodeTitle: '第1话' }] }] })
assert.equal(searchResponse.hasMore, true)
assert.equal(searchResponse.animes[0].episodes[0].episodeId, 123)
const exactStructuredMatch = selectExactStructuredDanmakuMatch(parseDanmakuSearch({
  success: true,
  animes: [
    { animeId: 11, animeTitle: '莉可丽丝 新作', episodes: [{ episodeId: 111, episodeTitle: '第1话' }] },
    { animeId: 12, animeTitle: '安琪莉可', episodes: [{ episodeId: 121, episodeTitle: '第1话' }] },
    { animeId: 13, animeTitle: '莉可丽丝', episodes: [{ episodeId: 131, episodeTitle: '第1话' }] },
    { animeId: 14, animeTitle: '莉可丽丝第二季', episodes: [{ episodeId: 141, episodeTitle: '第1话' }] },
  ],
}), '莉可丽丝')
assert.equal(exactStructuredMatch?.episodeId, 131)
assert.equal(normalizeDanmakuSeriesTitle(' Ｌｙｃｏｒｉｓ·Ｒｅｃｏｉｌ '), 'lycorisrecoil')
assert.equal(selectExactStructuredDanmakuMatch(parseDanmakuSearch({
  animes: [
    { animeId: 11, animeTitle: '莉可丽丝 新作', episodes: [{ episodeId: 111, episodeTitle: '第1话' }] },
    { animeId: 12, animeTitle: '安琪莉可', episodes: [{ episodeId: 121, episodeTitle: '第1话' }] },
  ],
}), '莉可丽丝'), null)
assert.equal(selectExactStructuredDanmakuMatch(parseDanmakuSearch({
  animes: [{ animeId: 13, animeTitle: '莉可丽丝', episodes: [
    { episodeId: 131, episodeTitle: '第1话' },
    { episodeId: 132, episodeTitle: '第2话' },
  ] }],
}), '莉可丽丝'), null)
assert.equal(selectExactStructuredDanmakuMatch(parseDanmakuSearch({
  animes: [
    { animeId: 13, animeTitle: '莉可丽丝', episodes: [{ episodeId: 131, episodeTitle: '第1话' }] },
    { animeId: 15, animeTitle: '莉可丽丝', episodes: [{ episodeId: 151, episodeTitle: '第1话' }] },
  ],
}), '莉可丽丝'), null)

const comments = parseDanmakuComments({ comments: [
  { cid: 1, p: '12.5,1,16777215,user', m: '滚动' },
  { cid: 2, p: '20,4,16711680,user', m: '底部' },
  { cid: 3, p: '30,5,255,user', m: '顶部' },
  { cid: 4, p: 'bad,7,0,user', m: '无效' },
] }, 1.5)
assert.deepEqual(comments.map(item => [item.time, item.mode, item.color]), [
  [14, 'scroll', '#ffffff'], [21.5, 'bottom', '#ff0000'], [31.5, 'top', '#0000ff'],
])

assert.equal(safeMediaName('/mnt/media/Show.S01E01.mkv'), 'Show.S01E01')
assert.equal(safeMediaName('https://server/video?api_key=secret'), '未命名影片')
assert.equal(inferDanmakuEpisode('Show.S01E03.mkv'), '3')
assert.equal(safeSearchKeyword('Fate/stay night'), 'Fate/stay night')
assert.deepEqual(resolveDanmakuMediaIdentity({
  mediaTitle: '慢慢的',
  fileName: 'Lycoris.Recoil.S01E01.mkv',
  seriesName: '莉可丽丝',
  seasonNumber: 1,
  episodeNumber: 1,
}), {
  matchName: '莉可丽丝.S01E01',
  searchTitle: '莉可丽丝',
  episode: '1',
})
assert.deepEqual(resolveDanmakuMediaIdentity({ mediaTitle: '慢慢的', fileName: '', seriesName: '莉可丽丝', seasonNumber: 1, episodeNumber: 1 }), {
  matchName: '莉可丽丝.S01E01',
  searchTitle: '莉可丽丝',
  episode: '1',
})
const redirectIdentity = resolveDanmakuMediaIdentity({
  mediaTitle: 'stream',
  fileName: 'stream',
  seriesName: '莉可丽丝',
  seasonNumber: 1,
  episodeNumber: 1,
})
assert.deepEqual(redirectIdentity, {
  matchName: '莉可丽丝.S01E01',
  searchTitle: '莉可丽丝',
  episode: '1',
})
assert.equal(JSON.stringify(redirectIdentity).includes('stream'), false)
const signedRedirectIdentity = resolveDanmakuMediaIdentity({
  mediaTitle: 'stream',
  fileName: 'https://media.example.test/proxy/stream?sig=sensitive-canary',
  seriesName: '莉可丽丝',
  seasonNumber: 1,
  episodeNumber: 1,
})
assert.deepEqual(signedRedirectIdentity, redirectIdentity)
assert.equal(JSON.stringify(signedRedirectIdentity).includes('sensitive-canary'), false)
const opaqueProviderIdentity = resolveDanmakuMediaIdentity({
  mediaTitle: '流浪地球',
  fileName: '37428',
})
assert.deepEqual(opaqueProviderIdentity, {
  matchName: '流浪地球',
  searchTitle: '流浪地球',
  episode: '',
})
assert.equal(JSON.stringify(opaqueProviderIdentity).includes('37428'), false)
assert.equal(interpolatedDanmakuTime({ mediaTime: 10, wallTime: 1_000 }, 1_500, 1.5, true), 10.75)
assert.equal(interpolatedDanmakuTime({ mediaTime: 10, wallTime: 1_000 }, 1_500, 1.5, false), 10)
const frames144Hz = Array.from({ length: 145 }, (_, index) => interpolatedDanmakuTime({ mediaTime: 10, wallTime: 1_000 }, 1_000 + index * (1_000 / 144), 1, true))
assert.ok(frames144Hz.every((time, index) => index === 0 || time > frames144Hz[index - 1]))
assert.ok(Math.abs(frames144Hz.at(-1)! - 11) < 1e-9)

const largeTimeline: DanmakuComment[] = Array.from({ length: 50_000 }, (_, index) => ({
  id: String(index),
  time: index / 10,
  mode: 'scroll',
  color: '#ffffff',
  text: `弹幕 ${index}`,
}))
const visibleWindow = findDanmakuTimelineWindow(largeTimeline, 2_500, 16)
assert.equal(visibleWindow.end - visibleWindow.start, 161)
assert.ok(visibleWindow.start > 20_000)
const sanitized = sanitizeDanmakuSettings({ opacity: 99, speed: -1, displayArea: 0.33, blockKeywords: ['剧透', '剧透'] })
assert.equal(sanitized.opacity, 1)
assert.equal(sanitized.speed, 0.5)
assert.equal(sanitized.displayArea, 0.75)
assert.deepEqual(sanitized.blockKeywords, ['剧透'])
const repairedVisibility = sanitizeDanmakuSettings({
  enabled: true,
  showScroll: false,
  showTop: false,
  showBottom: false,
})
assert.equal(repairedVisibility.enabled, true)
assert.equal(repairedVisibility.showScroll, true)
assert.equal(repairedVisibility.showTop, false)
assert.equal(repairedVisibility.showBottom, false)

const rust = read('src-tauri/src/commands/danmaku.rs')
assert.match(rust, /MAX_RESPONSE_BYTES/)
assert.match(rust, /redirect\(redirect::Policy::none\(\)\)/)
assert.match(rust, /X-Signature/)
assert.match(rust, /api\/v2\/search\/episodes/)
assert.doesNotMatch(rust, /"fileHash": ""/)
assert.match(rust, /拒绝了不安全跳转/)
assert.doesNotMatch(rust, /mobile_proxy/)

const player = read('src/views/PlayerView.vue')
assert.match(player, /<DanmakuOverlay/)
assert.match(player, /loadDanmakuForMedia/)
assert.doesNotMatch(player, /danmaku.*route\.query/i)
const overlayMount = player.match(/<DanmakuOverlay[^>]*\/>/)?.[0] ?? ''
assert.ok(overlayMount)
assert.match(overlayMount, /v-if="hasMedia"/)
assert.doesNotMatch(overlayMount, /shouldShowChrome|player-chrome-hidden|v-show|Transition/)
assert.match(player, /<Teleport to="body">\s*<DanmakuOverlay/)
const overlayIndex = player.indexOf(overlayMount)
const desktopChromeIndex = player.indexOf('class="player-bottom-chrome')
const mobileChromeIndex = player.indexOf('<MobilePlayerControls')
assert.ok(overlayIndex >= 0 && desktopChromeIndex > overlayIndex && mobileChromeIndex > overlayIndex)

const desktop = read('src/components/player/PlayerControls.vue')
const mobile = read('src/components/player/MobilePlayerControls.vue')
for (const source of [desktop, mobile]) {
  assert.match(source, /toggleDanmaku/)
  assert.match(source, /DanmakuSettingsContent/)
  assert.match(source, /DanmakuToggleIcon/)
}
assert.doesNotMatch(`${desktop}\n${mobile}`, />\s*弹\s*<\/button>/)

const searchDialog = read('src/components/player/DanmakuSearchDialog.vue')
assert.match(searchDialog, /is-mobile/)
assert.match(searchDialog, /anime\.episodes/)
assert.match(player, /<DanmakuSearchDialog/)
assert.match(player, /loadDanmakuForMedia\(currentDanmakuMediaIdentity\(\)/)
assert.match(player, /const playbackItem = currentPlaybackItem\(\)/)
assert.match(player, /fileName: seriesName\?\.trim\(\) \? '' : currentDanmakuFileName\(\)/)
const danmakuFileNameHelper = player.match(/function currentDanmakuFileName\(\): string \{[\s\S]*?\n\}/)?.[0] ?? ''
assert.ok(danmakuFileNameHelper)
assert.doesNotMatch(danmakuFileNameHelper, /mediaPath\.value/)
assert.match(danmakuFileNameHelper, /context\?\.locator\.kind === 'localPath'/)
assert.match(danmakuFileNameHelper, /isVideoFileName\(fileName\)/)
const composable = read('src/composables/useDanmaku.ts')
assert.match(composable, /searchDanmaku\(settings\.value, identity\.searchTitle, identity\.episode\)/)
assert.match(composable, /selectExactStructuredDanmakuMatch\(searchResponse, identity\.searchTitle\)/)
assert.doesNotMatch(composable, /selected = nextMatches\.length === 1/)
assert.match(composable, /searchedBySeries = true\s+error\.value = null/)
assert.match(composable, /function resetForMediaChange\(\)/)
assert.match(composable, /const currentGeneration = \+\+generation\s+if \(!settings\.value\.enabled\)/)
assert.match(player, /resetDanmakuUiForMediaChange\(\)\s+await saveCurrentProgress/)
assert.match(player, /danmakuSearchGeneration !== currentGeneration/)
const overlay = read('src/components/player/DanmakuOverlay.vue')
assert.match(overlay, /findDanmakuTimelineWindow/)
assert.match(overlay, /MAX_COMMENTS_PER_FRAME/)
assert.match(overlay, /MAX_CANDIDATES_PER_FRAME/)
assert.match(overlay, /data-danmaku-render-layer/)
assert.match(overlay, /pointer-events-none fixed inset-0/)
assert.match(overlay, /contain: strict/)
assert.match(overlay, /isolation: isolate/)
assert.match(overlay, /transform: translateZ\(0\)/)
assert.match(overlay, /will-change: transform/)
assert.match(overlay, /frame = requestAnimationFrame\(draw\)/)
assert.doesNotMatch(overlay, /shouldShowChrome|player-chrome-hidden|player-chrome-(?:top|bottom)|v-show/)
assert.doesNotMatch(player, /player-chrome-hidden|is-chrome-hidden/)
assert.match(player, /playerChromeStore\.setVisible\(visible\)/)
const settingsContent = read('src/components/player/DanmakuSettingsContent.vue')
assert.doesNotMatch(settingsContent, /<select[^>]+danmaku-match/)
assert.doesNotMatch(settingsContent, /selectMatch/)

const mobileProxy = read('src-tauri/src/mpv/mobile_proxy.rs')
assert.match(mobileProxy, /AndroidStreamProxyState/)
assert.match(mobileProxy, /redirect/)

console.log('Danmaku API parsing, privacy boundary, desktop/mobile controls, and Android 302 isolation verified.')
