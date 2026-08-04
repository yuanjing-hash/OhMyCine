export const APP_SCROLL_TO_TOP_EVENT = 'ohmycine:scroll-to-top'

export function requestAppScrollTop() {
  window.dispatchEvent(new Event(APP_SCROLL_TO_TOP_EVENT))
}
