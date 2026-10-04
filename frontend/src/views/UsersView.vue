<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { toast } from 'vue-sonner'

import { ApiError } from '@/api/request'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useUserStore } from '@/stores/user'

const userStore = useUserStore()
const { users, total, page, totalPages, loading } = storeToRefs(userStore)

const form = reactive({ name: '', email: '' })
const submitting = ref(false)

function errorMessage(err: unknown) {
  return err instanceof ApiError ? err.message : '未知错误'
}

async function load(targetPage?: number) {
  try {
    await userStore.fetchUsers(targetPage)
  } catch (err) {
    toast.error(errorMessage(err))
  }
}

async function submit() {
  submitting.value = true
  try {
    const user = await userStore.addUser({ ...form })
    toast.success(`已创建用户 ${user.name}`)
    form.name = ''
    form.email = ''
  } catch (err) {
    toast.error(errorMessage(err))
  } finally {
    submitting.value = false
  }
}

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}

onMounted(() => load(1))
</script>

<template>
  <div class="grid gap-6">
    <Card>
      <CardHeader>
        <CardTitle>新建用户</CardTitle>
        <CardDescription>POST /api/users，失败提示取自统一响应的 message</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end" @submit.prevent="submit">
          <div class="grid gap-2">
            <Label for="name">姓名</Label>
            <Input id="name" v-model="form.name" placeholder="Alice" />
          </div>
          <div class="grid gap-2">
            <Label for="email">邮箱</Label>
            <Input id="email" v-model="form.email" placeholder="alice@example.com" />
          </div>
          <Button type="submit" :disabled="submitting">创建</Button>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>用户列表</CardTitle>
        <CardDescription>共 {{ total }} 条</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead class="w-20">ID</TableHead>
              <TableHead>姓名</TableHead>
              <TableHead>邮箱</TableHead>
              <TableHead>创建时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="user in users" :key="user.id">
              <TableCell>{{ user.id }}</TableCell>
              <TableCell>{{ user.name }}</TableCell>
              <TableCell>{{ user.email }}</TableCell>
              <TableCell>{{ formatTime(user.createdAt) }}</TableCell>
            </TableRow>
            <TableEmpty v-if="users.length === 0" :colspan="4">
              {{ loading ? '加载中...' : '暂无数据' }}
            </TableEmpty>
          </TableBody>
        </Table>

        <div class="flex items-center justify-end gap-2 text-sm">
          <span class="text-muted-foreground">第 {{ page }} / {{ totalPages }} 页</span>
          <Button
            variant="outline"
            size="sm"
            :disabled="loading || page <= 1"
            @click="load(page - 1)"
          >
            上一页
          </Button>
          <Button
            variant="outline"
            size="sm"
            :disabled="loading || page >= totalPages"
            @click="load(page + 1)"
          >
            下一页
          </Button>
        </div>
      </CardContent>
    </Card>
  </div>
</template>
