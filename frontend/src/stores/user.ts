import { create } from 'zustand'

import { createUser, listUsers, type CreateUserPayload, type User } from '@/api/user'

interface UserState {
  users: User[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  fetchUsers: (targetPage?: number) => Promise<void>
  addUser: (payload: CreateUserPayload) => Promise<User>
}

export const useUserStore = create<UserState>()((set, get) => ({
  users: [],
  total: 0,
  page: 1,
  pageSize: 10,
  loading: false,

  async fetchUsers(targetPage = get().page) {
    set({ loading: true })
    try {
      const res = await listUsers({ page: targetPage, pageSize: get().pageSize })
      set({ users: res.list, total: res.total, page: res.page })
    } finally {
      set({ loading: false })
    }
  },

  async addUser(payload) {
    const user = await createUser(payload)
    await get().fetchUsers(1)
    return user
  },
}))

export const selectTotalPages = (state: UserState) =>
  Math.max(1, Math.ceil(state.total / state.pageSize))
