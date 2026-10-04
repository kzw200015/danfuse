import { request } from './request'

export interface Health {
  status: string
}

export function getHealth() {
  return request<Health>({ url: '/health', method: 'GET' })
}
