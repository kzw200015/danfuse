import { request, type Page } from './request'

export interface User {
  id: number
  name: string
  email: string
  createdAt: string
  updatedAt: string
}

export interface CreateUserPayload {
  name: string
  email: string
}

export interface ListUsersParams {
  page?: number
  pageSize?: number
}

export function listUsers(params: ListUsersParams = {}) {
  return request<Page<User>>({ url: '/users', method: 'GET', params })
}

export function getUser(id: number) {
  return request<User>({ url: `/users/${id}`, method: 'GET' })
}

export function createUser(data: CreateUserPayload) {
  return request<User>({ url: '/users', method: 'POST', data })
}
