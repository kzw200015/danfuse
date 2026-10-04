<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { getHealth } from '@/api/health'
import { ApiError } from '@/api/request'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

const loading = ref(false)
const status = ref<'ok' | 'error' | null>(null)
const message = ref('')

async function check() {
  loading.value = true
  try {
    await getHealth()
    status.value = 'ok'
    message.value = '后端与数据库连接正常'
  } catch (err) {
    status.value = 'error'
    message.value = err instanceof ApiError ? `${err.message}（code: ${err.code}）` : String(err)
  } finally {
    loading.value = false
  }
}

onMounted(check)
</script>

<template>
  <Card class="max-w-md">
    <CardHeader>
      <CardTitle>服务状态</CardTitle>
      <CardDescription>调用 GET /api/health 检查后端与数据库</CardDescription>
    </CardHeader>
    <CardContent>
      <p v-if="status === null" class="text-sm text-muted-foreground">检查中...</p>
      <p v-else :class="['text-sm', status === 'ok' ? 'text-emerald-600' : 'text-destructive']">
        {{ message }}
      </p>
    </CardContent>
    <CardFooter>
      <Button variant="outline" :disabled="loading" @click="check">重新检查</Button>
    </CardFooter>
  </Card>
</template>
