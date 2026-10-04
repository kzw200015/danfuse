<script setup lang="ts">
import 'vue-sonner/style.css'

import { RouterLink, RouterView } from 'vue-router'

import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'

const navItems = [
  { to: '/', label: '首页' },
  { to: '/users', label: '用户' },
]
</script>

<template>
  <div class="min-h-svh bg-background text-foreground">
    <header class="border-b">
      <div class="mx-auto flex h-14 max-w-5xl items-center gap-6 px-4">
        <span class="font-semibold">Danfuse</span>
        <nav class="flex items-center gap-1">
          <Button v-for="item in navItems" :key="item.to" variant="ghost" size="sm" as-child>
            <RouterLink :to="item.to" active-class="bg-muted">{{ item.label }}</RouterLink>
          </Button>
        </nav>
      </div>
    </header>

    <main class="mx-auto max-w-5xl px-4 py-8">
      <RouterView />
    </main>

    <Toaster rich-colors />
  </div>
</template>
