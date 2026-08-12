import { describe, expect, it } from 'vitest'

import { normalizeLineEndings } from './line-endings.mjs'

describe('generated permission newline comparison', () => {
  it('treats CRLF and LF output as the same content', () => {
    expect(normalizeLineEndings('first\r\nsecond\r\n')).toBe('first\nsecond\n')
  })

  it('does not normalize actual content drift', () => {
    expect(normalizeLineEndings('first\r\nchanged\r\n')).not.toBe('first\nsecond\n')
  })
})
