import axios, { type AxiosRequestConfig } from 'axios'

import { CODE_FAIL, CODE_OK } from './errcode'

/** 统一响应结构，对应后端 internal/pkg/response.Response */
export interface ApiResponse<T = unknown> {
  /** 业务码，见 ./errcode.ts */
  code: number
  message: string
  data: T
}

/** 接口错误。code 为业务码（见 ./errcode.ts），status 为 HTTP 状态码（无响应时为 0） */
export class ApiError extends Error {
  readonly code: number
  readonly status: number

  constructor(message: string, code: number, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

function isApiResponse(body: unknown): body is ApiResponse {
  return typeof body === 'object' && body !== null && typeof (body as ApiResponse).code === 'number'
}

/** status 为给定 HTTP 状态码的 ApiError */
export function isApiStatus(e: unknown, status: number): e is ApiError {
  return e instanceof ApiError && e.status === status
}

/**
 * 后端当场请求平台的接口（创建绑定、重新拉取、预览和创建季绑定）最长约 25 秒（服务端的写超时是 30 秒）；
 * 默认的 15 秒请求超时不够，放宽到 35 秒，让服务端先给出结果。上传弹幕文件（默认最多 50 MB，后端配置 danmaku_file.max_upload_mb）也用它。
 */
export const slowRequestTimeout = 35_000

export const http = axios.create({
  baseURL: '/api',
  timeout: 15_000,
})

http.interceptors.response.use(
  (resp) => {
    if (!isApiResponse(resp.data)) {
      throw new ApiError('响应格式错误', CODE_FAIL, resp.status)
    }
    if (resp.data.code !== CODE_OK) {
      throw new ApiError(resp.data.message, resp.data.code, resp.status)
    }
    return resp
  },
  (error: unknown) => {
    if (!axios.isAxiosError(error)) {
      throw error
    }
    const status = error.response?.status ?? 0
    const body: unknown = error.response?.data
    if (isApiResponse(body)) {
      throw new ApiError(body.message, body.code, status)
    }
    throw new ApiError(error.message || '网络错误', CODE_FAIL, status)
  },
)

/** 发起请求并解包统一响应，直接返回 data；失败时抛出 ApiError */
export async function request<T>(config: AxiosRequestConfig): Promise<T> {
  const resp = await http.request<ApiResponse<T>>(config)
  return resp.data.data
}
