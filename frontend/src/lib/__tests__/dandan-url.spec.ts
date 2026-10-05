import { describe, expect, it } from 'vitest'

import { dandanUrl } from '@/lib/dandan-url'

describe('dandanUrl', () => {
  it.each([
    ['http://192.168.1.10:8080', null, 'http://192.168.1.10:8080/dandanplay'],
    ['https://danmaku.example.com', 's3cret', 'https://danmaku.example.com/dandanplay/s3cret'],
  ])('%s，token %s → %s', (origin, token, want) => {
    expect(dandanUrl(origin, token)).toBe(want)
  })
})
