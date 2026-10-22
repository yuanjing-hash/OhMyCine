// Uses the existing Server Web UI test toolchain without adding Player dependencies.
// Set OHMYCINE_WEBUI_TEST_RUNTIME to its webui directory for isolated checkouts.
import { resolve } from 'node:path'
import { existsSync } from 'node:fs'
import { fileURLToPath, pathToFileURL } from 'node:url'

const at = path => fileURLToPath(new URL(path, import.meta.url))
const runtime = resolve(process.env.OHMYCINE_WEBUI_TEST_RUNTIME || at('../../OhMyCine-Server/webui'))
const fromRuntime = path => resolve(runtime, 'node_modules', path)
if (!existsSync(fromRuntime('vitest/dist/config.js')) || !existsSync(fromRuntime('@vitejs/plugin-vue/dist/index.mjs'))) {
  throw new Error('Player mounted checks require the installed Server Web UI test runtime. Set OHMYCINE_WEBUI_TEST_RUNTIME to its absolute webui directory, then run that directory\'s node_modules/vitest/vitest.mjs with --config scripts/verify-server-discovery-detail.config.mjs. No Player test dependency is installed by this script.')
}
const [{ defineConfig }, { default: vue }] = await Promise.all([
  import(pathToFileURL(fromRuntime('vitest/dist/config.js')).href),
  import(pathToFileURL(fromRuntime('@vitejs/plugin-vue/dist/index.mjs')).href),
])

export default defineConfig({
  root: at('../'),
  plugins: [vue()],
  resolve: { alias: {
    '@': at('../src'),
    'vue-router': at('../node_modules/vue-router/dist/vue-router.mjs'),
    'vue': fromRuntime('vue/dist/vue.esm-bundler.js'),
    '@vue/test-utils': fromRuntime('@vue/test-utils/dist/vue-test-utils.esm-bundler.mjs'),
    'vitest': fromRuntime('vitest/dist/index.js'),
  }, dedupe: ['vue'] },
  test: { environment: 'happy-dom', include: ['scripts/verify-server-discovery-detail.mounted.test.ts'] },
})
