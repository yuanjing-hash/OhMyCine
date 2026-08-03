export function isNativeAndroidRuntime(): boolean {
  return /Android/i.test(globalThis.navigator?.userAgent ?? '')
}
