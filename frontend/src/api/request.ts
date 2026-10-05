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
