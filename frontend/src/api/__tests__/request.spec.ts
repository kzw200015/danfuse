import { afterEach, describe, expect, it } from 'vitest'
import { AxiosError, type AxiosResponse } from 'axios'

import { CODE_FAIL } from '@/api/errcode'
import { ApiError, http, isApiStatus, request } from '@/api/request'

const originalAdapter = http.defaults.adapter

/** 用自定义 adapter 模拟后端响应，不发出真实请求；4xx、5xx 按 axios 的默认行为抛出 */
function mockResponse(status: number, data: unknown) {
  http.defaults.adapter = async (config) => {
    const response: AxiosResponse = { data, status, statusText: '', headers: {}, config }
    if (status >= 400) {
      throw new AxiosError(
        `Request failed with status code ${status}`,
        AxiosError.ERR_BAD_RESPONSE,
        config,
        null,
        response,
      )
    }
    return response
  }
}

function mockNetworkError() {
  http.defaults.adapter = async (config) => {
    throw new AxiosError('Network Error', AxiosError.ERR_NETWORK, config)
  }
}

/** 断言请求失败并返回 ApiError 的关键字段 */
async function failure(promise: Promise<unknown>) {
  const err = await promise.catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  const { code, status, message } = err as ApiError
  return { code, status, message }
}

afterEach(() => {
  http.defaults.adapter = originalAdapter
})

describe('request', () => {
  it('成功时解包 data', async () => {
    mockResponse(200, { code: 0, message: 'ok', data: { id: 1, status: 'running' } })

    await expect(request({ url: '/sync-runs/latest' })).resolves.toEqual({
      id: 1,
      status: 'running',
    })
  })

  it.each([
    {
      name: '统一结构的错误响应：保留业务码、HTTP 状态码与提示',
      mock: () => mockResponse(409, { code: CODE_FAIL, message: '同步正在进行', data: null }),
      want: { code: CODE_FAIL, status: 409, message: '同步正在进行' },
    },
    {
      name: '2xx 但业务码不为 0',
      mock: () => mockResponse(200, { code: 1001, message: '业务失败', data: null }),
      want: { code: 1001, status: 200, message: '业务失败' },
    },
    {
      name: '2xx 但响应不是统一结构：响应格式错误',
      mock: () => mockResponse(200, '<!doctype html>'),
      want: { code: CODE_FAIL, status: 200, message: '响应格式错误' },
    },
    {
      name: '错误响应不是统一结构：通用失败，保留 HTTP 状态码',
      mock: () => mockResponse(502, '<html>Bad Gateway</html>'),
      want: { code: CODE_FAIL, status: 502, message: 'Request failed with status code 502' },
    },
    {
      name: '网络错误：通用失败，status 为 0',
      mock: mockNetworkError,
      want: { code: CODE_FAIL, status: 0, message: 'Network Error' },
    },
  ])('$name → ApiError', async ({ mock, want }) => {
    mock()

    expect(await failure(request({ url: '/series' }))).toEqual(want)
  })
})

describe('isApiStatus', () => {
  it('只认指定状态码的 ApiError', () => {
    const notFound = new ApiError('剧不存在', CODE_FAIL, 404)
    expect(isApiStatus(notFound, 404)).toBe(true)
    expect(isApiStatus(notFound, 409)).toBe(false)
    expect(isApiStatus(new Error('剧不存在'), 404)).toBe(false)
    expect(isApiStatus(null, 404)).toBe(false)
  })
})
