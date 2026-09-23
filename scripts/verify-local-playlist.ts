import assert from 'node:assert/strict'
import { getPlaybackMediaContext } from '../src/services/playbackContext.ts'
import { createLocalPlaylistContext, isLocalVideoFileName, sortLocalPlaylistFiles } from '../src/services/localPlaylist.ts'

assert.equal(isLocalVideoFileName('Episode 2.MKV'), true)
assert.equal(isLocalVideoFileName('Episode 2.srt'), false)

const files = sortLocalPlaylistFiles([
  { path: 'C:\\Media\\Episode 10.mkv' },
  { path: 'C:\\Media\\Episode 2.mkv' },
  { path: 'C:\\Media\\Episode 1.mkv' },
])
assert.deepEqual(files.map(file => file.path.split('\\').at(-1)), [
  'Episode 1.mkv', 'Episode 2.mkv', 'Episode 10.mkv',
])

const first = await createLocalPlaylistContext([
  { path: 'C:\\Media\\Episode 10.mkv' },
  { path: 'C:\\Media\\Episode 2.mkv' },
  { path: 'C:\\Media\\Episode 2.mkv' },
  { path: 'C:\\Media\\Notes.txt' },
])
const context = getPlaybackMediaContext(first.contextId)
assert.equal(context?.queue?.items.length, 2)
assert.deepEqual(context?.queue?.items.map(item => item.name), ['Episode 2.mkv', 'Episode 10.mkv'])
assert.equal(context?.queue?.items[0].path, context?.locator.kind === 'localPath' ? context.locator.path : null)
assert.equal(context?.queue?.items[0].id, first.itemId)
assert.ok(context?.queue?.items.every(item => item.sourceId === 'local-file' && item.historyIdentity === item.id))
assert.deepEqual(context?.queue?.items.map(item => item.path), [
  'C:\\Media\\Episode 2.mkv',
  'C:\\Media\\Episode 10.mkv',
])

const reopened = await createLocalPlaylistContext([{ path: 'C:\\Media\\Episode 2.mkv' }])
assert.equal(reopened.itemId, first.itemId, 'local playback progress identity must stay stable after reopening')

console.log('local playlist contract verified')
