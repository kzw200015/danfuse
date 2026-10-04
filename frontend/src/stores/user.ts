import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

import { createUser, listUsers, type CreateUserPayload, type User } from '@/api/user'

export const useUserStore = defineStore('user', () => {
  const users = ref<User[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(10)
  const loading = ref(false)

  const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))

  async function fetchUsers(targetPage = page.value) {
    loading.value = true
    try {
      const res = await listUsers({ page: targetPage, pageSize: pageSize.value })
      users.value = res.list
      total.value = res.total
      page.value = res.page
    } finally {
      loading.value = false
    }
  }

  async function addUser(payload: CreateUserPayload) {
    const user = await createUser(payload)
    await fetchUsers(1)
    return user
  }

  return { users, total, page, pageSize, loading, totalPages, fetchUsers, addUser }
})
