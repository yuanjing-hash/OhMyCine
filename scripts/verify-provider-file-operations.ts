import { strict as assert } from 'node:assert'
import { readFile } from 'node:fs/promises'

const nativeDelete = await readFile(new URL('../src-tauri/src/commands/provider_file.rs', import.meta.url), 'utf8')
const nativeDownloads = await readFile(new URL('../src-tauri/src/commands/downloads.rs', import.meta.url), 'utf8')
const cd2 = await readFile(new URL('../src-tauri/src/commands/clouddrive2.rs', import.meta.url), 'utf8')
const pan123 = await readFile(new URL('../src-tauri/src/commands/pan123.rs', import.meta.url), 'utf8')
const quark = await readFile(new URL('../src-tauri/src/commands/quark.rs', import.meta.url), 'utf8')
const mediaDelete = await readFile(new URL('../src/services/mediaDelete.ts', import.meta.url), 'utf8')
const downloadAdapter = await readFile(new URL('../src/services/mediaActions/downloadAdapter.ts', import.meta.url), 'utf8')

assert.match(nativeDelete, /credential::read_credential_value/)
assert.match(nativeDelete, /resolve_source_download/)
assert.match(nativeDelete, /"emby" \| "jellyfin"/)
assert.match(nativeDelete, /x-emby-token/)
assert.match(nativeDelete, /Method::DELETE/)
assert.match(nativeDelete, /"api\/fs\/remove"/)
assert.match(nativeDelete, /"names": \[name\]/)
assert.match(nativeDelete, /redirect\(reqwest::redirect::Policy::none\(\)\)/)
assert.match(nativeDelete, /installed gRPC contract does not expose a verified delete method/)
assert.match(nativeDelete, /Source deletion is restricted to an item below the configured root/)
assert.doesNotMatch(nativeDelete, /log::|println!|dbg!\(/)

assert.match(cd2, /GetDownloadUrlPath/)
assert.match(cd2, /pub\(crate\) async fn resolve_download_stream/)
assert.match(pan123, /pub\(crate\) async fn resolve_download_stream/)
assert.match(pan123, /"\/file\/trash"/)
assert.match(pan123, /private web API changed/)
assert.match(quark, /pub\(crate\) async fn resolve_download_stream/)
assert.match(quark, /"\/file\/delete"/)
assert.match(quark, /private web API changed/)

assert.match(mediaDelete, /'alist', 'webdav', '123', 'quark'/)
assert.match(mediaDelete, /provider_source_file_delete/)
assert.match(downloadAdapter, /'clouddrive2', 'webdav', '123', 'quark', 'emby', 'jellyfin'/)
assert.match(nativeDownloads, /resolve_task_remote/)
assert.match(nativeDownloads, /"clouddrive2" \| "webdav" \| "123" \| "quark" \| "emby" \| "jellyfin"/)

for (const forbidden of ['url TEXT', 'headers TEXT', 'cookie TEXT', 'authorization TEXT', 'signature TEXT'])
  assert.doesNotMatch(nativeDownloads.toLowerCase(), new RegExp(forbidden.toLowerCase()))

console.log('Provider native download/delete regression checks passed.')
