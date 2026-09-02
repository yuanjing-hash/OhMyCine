import assert from 'node:assert/strict'
import fs from 'node:fs'

const detailView = fs.readFileSync(new URL('../src/views/MediaDetailView.vue', import.meta.url), 'utf8')
const detailHero = fs.readFileSync(new URL('../src/components/media/MediaDetailHero.vue', import.meta.url), 'utf8')
const immersiveRail = fs.readFileSync(new URL('../src/components/media/ImmersiveMediaRail.vue', import.meta.url), 'utf8')

assert.equal((detailView.match(/<ImmersiveMediaRail/g) ?? []).length, 2)
assert.match(detailView, /<ImmersiveMediaRail label="剧照与截图">[\s\S]*class="stills-strip"/)
assert.match(detailView, /<ImmersiveMediaRail label="演职员">[\s\S]*class="people-strip"/)
assert.match(detailView, /\.person-portrait[\s\S]*aspect-ratio:\s*2\s*\/\s*3/)
assert.match(detailView, /\.still-card img[\s\S]*aspect-ratio:\s*16\s*\/\s*9/)
assert.match(detailView, /grid-template-columns:\s*repeat\(auto-fit, minmax\(min\(100%, 18rem\), 1fr\)\)/)
assert.doesNotMatch(detailView, /detail\.stills[\s\S]{0,500}cinema-scrollbar/)

assert.match(immersiveRail, /scrollbar-width:\s*none/)
assert.match(immersiveRail, /::-webkit-scrollbar[\s\S]*display:\s*none/)
assert.match(immersiveRail, /scrollBy\(\{[\s\S]*behavior:\s*'smooth'/)
assert.match(immersiveRail, /:disabled="!canScrollBackward"/)
assert.match(immersiveRail, /:disabled="!canScrollForward"/)
assert.match(immersiveRail, /@keydown="handleRailKeydown"/)
assert.match(immersiveRail, /\(hover: none\) and \(pointer: coarse\)[\s\S]*\.immersive-rail-button[\s\S]*display:\s*none/)

assert.match(detailHero, /min-height:\s*clamp\(34rem, 58vh, 45rem\)/)
assert.match(detailHero, /detail-hero-horizontal-shade/)
assert.match(detailHero, /detail-hero-vertical-shade/)

console.log(JSON.stringify({
  nativeRailChromeHidden: true,
  glassRailNavigation: true,
  embyInspiredPortraitHierarchy: true,
  responsiveTouchRail: true,
  immersiveHeroPreserved: true,
}, null, 2))
