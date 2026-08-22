import assert from 'node:assert/strict'
import { streamVariantDescription, streamVariantLabel, usableStreamVariants } from '../src/services/streamVariants'

const variants = usableStreamVariants([
  { id: '1080', label: '1080P', available: true, width: 1920, height: 1080, bitrate: 8_000_000, videoCodec: 'AVC' },
  { id: '1080', label: 'duplicate', available: true },
  { id: '4k', label: '4K', available: false, unavailableReason: '账号不可用' },
  { id: '720', label: '', available: true, height: 720 },
])

assert.deepEqual(variants.map(item => item.id), ['1080', '720'])
assert.equal(streamVariantLabel(variants[0]), '1080P')
assert.equal(streamVariantLabel(variants[1]), '720p')
assert.equal(streamVariantDescription(variants[0]), '1920×1080 · 8.0 Mbps · AVC')

console.log('stream quality contract verified')
