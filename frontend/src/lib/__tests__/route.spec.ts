import { describe, expect, it } from 'vitest'

import { parseId } from '../route'

describe('parseId', () => {
  it.each([
    ['1', 1],
    ['42', 42],
    ['0', undefined],
    ['-1', undefined],
    ['1.5', undefined],
    ['01', undefined],
    ['1e1', undefined],
    ['abc', undefined],
    ['', undefined],
  ])('%j', (param, want) => {
    expect(parseId(param)).toBe(want)
  })
})
