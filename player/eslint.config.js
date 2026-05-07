import antfu from '@antfu/eslint-config'

export default antfu({
  vue: true,
  typescript: true,
  ignores: ['dist/**', 'src-tauri/**', 'node_modules/**'],
})
