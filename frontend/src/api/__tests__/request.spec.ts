import { afterEach, describe, expect, it } from 'vitest'
import { AxiosError, type AxiosResponse } from 'axios'

import { CODE_FAIL } from '@/api/errcode'
import { ApiError, http, request } from '@/api/request'

const originalAdapter = http.defaults.adapter

/** 用自定义 adapter 模拟后端响应，不发出真实请求 */
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
    mockResponse(200, { code: 0, message: 'ok', data: { id: 1 } })

    await expect(request({ url: '/users/1' })).resolves.toEqual({ id: 1 })
  })

  it('业务错误转换为 ApiError，保留业务码与 HTTP 状态码', async () => {
    mockResponse(409, { code: 10002, message: '邮箱已被使用', data: null })

    expect(await failure(request({ url: '/users' }))).toEqual({
      code: 10002,
      status: 409,
      message: '邮箱已被使用',
    })
  })

  it('响应不是统一结构时归为通用失败', async () => {
    mockResponse(502, '<html>Bad Gateway</html>')

    expect(await failure(request({ url: '/users' }))).toMatchObject({
      code: CODE_FAIL,
      status: 502,
    })
  })

  it('网络错误归为通用失败，status 为 0', async () => {
    mockNetworkError()

    expect(await failure(request({ url: '/users' }))).toEqual({
      code: CODE_FAIL,
      status: 0,
      message: 'Network Error',
    })
  })
})
