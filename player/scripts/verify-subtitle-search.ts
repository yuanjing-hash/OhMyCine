import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const root = new URL('../', import.meta.url)
const read = (path: string) => readFile(fileURLToPath(new URL(path, root)), 'utf8')

const controls = await read('src/components/player/PlayerControls.vue')
assert.match(controls, /搜索字幕/)
assert.match(controls, /emit\('searchSubtitles'\)/)
assert.match(controls, /字幕偏移/)
assert.match(controls, /emit\('setSubtitleDelay'/)
assert.match(controls, /type="range"[\s\S]*min="-30"[\s\S]*max="30"/)

const playerView = await read('src/views/PlayerView.vue')
assert.match(playerView, /subtitleSearchRequiresSourceChoice\.value = sourceType === 'emby'/)
assert.match(playerView, /subtitleSearchOrigin\.value = sourceType === 'emby' \? null : 'local'/)
assert.match(playerView, /if \(origin === 'emby'\)[\s\S]*source\.searchSubtitles/)
assert.match(playerView, /else \{[\s\S]*searchLocalSubtitles/)
assert.match(playerView, /currentSubtitleSearchContext\(keyword, keywordMode\)/)
assert.match(playerView, /year: keywordMode === 'custom' \? undefined/)
assert.match(playerView, /imdbId: keywordMode === 'custom' \? undefined/)
assert.match(playerView, /originalTitle: keywordMode === 'custom' \? undefined/)
assert.match(playerView, /duration: keywordMode === 'custom'/)
assert.match(playerView, /keywordMode,/)
assert.match(playerView, /currentLocalSubtitleFilePath\(\)[\s\S]*isAbsoluteLocalMediaPath/)
assert.match(playerView, /remoteMediaUrl: currentRemoteSubtitleMediaUrl\(\)/)
assert.match(playerView, /remoteMediaHeaders: currentRemoteSubtitleMediaUrl\(\) \? \{ \.\.\.mediaHeaders\.value \}/)
assert.match(playerView, /:media-title="currentSubtitleMediaTitle\(\)"/)
assert.match(playerView, /:file-name="currentSubtitleFileName\(\)"/)

const searchDialog = await read('src/components/player/SubtitleSearchDialog.vue')
assert.match(searchDialog, /媒体名称/)
assert.match(searchDialog, /原始文件名/)
assert.match(searchDialog, /自定义/)
assert.match(searchDialog, /search: \[language: SubtitleLanguage, keyword: string, keywordMode: SubtitleKeywordMode\]/)

const emby = await read('src/services/datasource/emby.ts')
assert.match(emby, /'OriginalTitle'/)
assert.match(emby, /originalTitle: nonEmptyString\(item\.OriginalTitle\)/)
assert.match(emby, /RemoteSearch\/Subtitles\/\$\{encodeURIComponent\(language\)\}/)
assert.match(emby, /postPlaybackJson\([\s\S]*RemoteSearch\/Subtitles/)
assert.match(emby, /this\.cache\.clear\(\)/)

const settings = await read('src/services/subtitle/settings.ts')
assert.match(settings, /saveOpenSubtitlesCredential/)
assert.match(settings, /subtitle_login_opensubtitles/)
assert.match(settings, /shooterEnabled/)
assert.match(settings, /xunleiEnabled/)
assert.doesNotMatch(settings, /setAppSetting\([^)]*apiKey/)

const subtitleIndex = await read('src/services/subtitle/index.ts')
assert.match(subtitleIndex, /hasOpenSubtitlesCredential/)
assert.match(subtitleIndex, /const openSubtitlesCredential = await readOpenSubtitlesCredentials\(\)/)
assert.match(subtitleIndex, /settings\.shooterEnabled && canUseHashProviders/)
assert.match(subtitleIndex, /settings\.xunleiEnabled \? providers\.xunlei/)
assert.match(subtitleIndex, /hasKeywordProvider = openSubtitlesActive \|\| settings\.xunleiEnabled/)
assert.match(subtitleIndex, /Promise\.allSettled/)
assert.match(subtitleIndex, /openSubtitlesActive/)
assert.match(subtitleIndex, /OpenSubtitles 已配置但当前处于关闭状态/)
assert.match(subtitleIndex, /输入的关键词没有被查询/)

const settingsView = await read('src/views/SettingsView.vue')
assert.match(settingsView, /await saveOpenSubtitlesCredentials\(nextCredential\)[\s\S]*subtitleForm\.openSubtitlesEnabled = true/)
assert.match(settingsView, /openSubtitlesStatusLabel/)
assert.match(settingsView, /登录已保留但提供器处于关闭状态/)

const useMpv = await read('src/composables/useMpv.ts')
assert.match(useMpv, /prop: 'sub-delay'/)
assert.match(useMpv, /MIN_SUBTITLE_DELAY = -30/)
assert.match(useMpv, /MAX_SUBTITLE_DELAY = 30/)
assert.match(useMpv, /applySubtitleDelay\(DEFAULT_SUBTITLE_DELAY\)/)

const credentialStore = await read('src/services/datasource/credentialStore.ts')
assert.match(credentialStore, /version: 3,[\s\S]*provider: 'opensubtitles'/)
assert.match(credentialStore, /value\.version !== 1 && value\.version !== 2 && value\.version !== 3/)
assert.match(credentialStore, /authMode: 'apiKey'/)
assert.match(credentialStore, /authMode: 'account'/)
assert.match(credentialStore, /probePersistentCredentialStorage[\s\S]*credential-health-check/)

const providers = await read('src/services/subtitle/hashProviders.ts')
assert.match(providers, /this\.id === 'shooter' && !input\.localFilePath && !input\.remoteMediaUrl/)
assert.match(providers, /this\.id === 'xunlei' && !query/)
assert.match(providers, /query: query \?\? null/)
assert.match(providers, /originalTitle: input\.originalTitle \?\? null/)
assert.match(providers, /durationSeconds: input\.duration \?\? null/)
assert.match(providers, /mediaType: input\.mediaType \?\? null/)
assert.match(providers, /remoteUrl: input\.remoteMediaUrl/)
assert.match(providers, /headers: toHeaderPayload/)
assert.match(providers, /subtitle_search_hash_provider/)
assert.doesNotMatch(providers, /downloadRef.*https?:/)

const rust = await read('src-tauri/src/commands/subtitle.rs')
assert.match(rust, /host == "opensubtitles\.com" \|\| host\.ends_with\("\.opensubtitles\.com"\)/)
assert.match(rust, /layout\.cache_dir\.join\("subtitles"\)/)
assert.match(rust, /MAX_DOWNLOAD_RESPONSE_BYTES/)
assert.match(rust, /compute_shooter_hash/)
assert.match(rust, /compute_xunlei_cid/)
assert.match(rust, /https:\/\/api-shoulei-ssl\.xunlei\.com\/oracle\/subtitle/)
assert.match(rust, /append_pair\("name", query\)/)
assert.match(rust, /optional_xunlei_media_cid/)
assert.match(rust, /XUNLEI_OPTIONAL_CID_TIMEOUT_SECONDS/)
assert.match(rust, /CID 精确匹配/)
assert.match(rust, /assess_xunlei_match/)
assert.match(rust, /xunlei_duration_match_score/)
assert.match(rust, /parse_episode_marker/)
assert.match(rust, /ranks_supergirl_2026_movie_and_rejects_tv_episodes/)
assert.match(rust, /ranks_exact_episode_and_rejects_other_episode_numbers/)
assert.doesNotMatch(rust, /sub\.xmp\.sandai\.net/)
assert.match(rust, /url\.host_str\(\) != Some\("www\.shooter\.cn"\)/)
assert.match(rust, /url\.host_str\(\) != Some\("subtitle\.v\.geilijiasu\.com"\)/)
assert.match(rust, /DOWNLOAD_REFERENCE_TTL/)
assert.match(rust, /https:\/\/api\.opensubtitles\.org\/xml-rpc/)
assert.match(rust, /SearchSubtitles/)
assert.match(rust, /DownloadSubtitles/)
assert.match(rust, /GzDecoder/)
assert.match(rust, /request_remote_media_range/)
assert.match(rust, /Range 读取/)
assert.match(rust, /ACCEPT_ENCODING/)
assert.match(rust, /if !same_url_origin\(&url, &next\)[\s\S]*headers\.clear\(\)/)
assert.match(rust, /remote_range_hashes_match_local_file_hashes/)
assert.match(rust, /custom_keyword_is_written_to_opensubtitles_search_request/)
assert.match(rust, /anonymous_status/)
assert.doesNotMatch(rust, /println!|dbg!|log::.*password/)

console.log(JSON.stringify({
  embyRequiresExplicitOriginChoice: true,
  nonEmbyUsesLocalSearchDirectly: true,
  credentialsUseSecureStore: true,
  downloadsAreConstrainedToSubtitleCache: true,
  openSubtitlesSupportsExclusiveApiKeyAndAccountModes: true,
  openSubtitlesAccountModeUsesXmlRpcSession: true,
  modernAccount401FallsBackToAnonymousXmlRpc: true,
  shooterSupportsLocalAndRemoteHashes: true,
  xunleiUsesNameSearchWithOptionalCidRanking: true,
  xunleiNameSearchDoesNotRequireRangeAccess: true,
  xunleiRanksByMediaIdentityYearAndDuration: true,
  xunleiRejectsMovieEpisodeResults: true,
  xunleiRejectsWrongSeasonEpisodeResults: true,
  remoteHashHeadersStayInsideRust: true,
  hashProviderDownloadsUseOpaqueReferences: true,
  missingOpenSubtitlesKeyDoesNotBlockHashProviders: true,
  subtitleDelayUsesMpvSubDelay: true,
  subtitleSearchOffersThreeKeywordModes: true,
  customKeywordOmitsCurrentMediaConstraints: true,
  savingOpenSubtitlesCredentialsEnablesProvider: true,
  disabledKeywordProviderIsReportedClearly: true,
}, null, 2))
