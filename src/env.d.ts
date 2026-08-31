/// <reference types="vite/client" />

declare const __OHMYCINE_BUILTIN_TMDB_READ_ACCESS_TOKEN__: string

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<object, object, any>
  export default component
}
