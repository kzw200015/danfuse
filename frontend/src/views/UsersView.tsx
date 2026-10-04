import { useEffect, useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'
import { useShallow } from 'zustand/react/shallow'

import { ApiError } from '@/api/request'
import type { CreateUserPayload } from '@/api/user'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { selectTotalPages, useUserStore } from '@/stores/user'

const emptyForm: CreateUserPayload = { name: '', email: '' }

function errorMessage(err: unknown) {
  return err instanceof ApiError ? err.message : '未知错误'
}

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}

export default function UsersView() {
  const { users, total, page, loading, fetchUsers, addUser } = useUserStore(
    useShallow((s) => ({
      users: s.users,
      total: s.total,
      page: s.page,
      loading: s.loading,
      fetchUsers: s.fetchUsers,
      addUser: s.addUser,
    })),
  )
  const totalPages = useUserStore(selectTotalPages)

  const [form, setForm] = useState(emptyForm)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    // 组件卸载后不再提示，避免 StrictMode 下重复执行 effect 时弹出两次
    let ignore = false
    fetchUsers(1).catch((err: unknown) => {
      if (!ignore) {
        toast.error(errorMessage(err))
      }
    })
    return () => {
      ignore = true
    }
  }, [fetchUsers])

  async function load(targetPage: number) {
    try {
      await fetchUsers(targetPage)
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setSubmitting(true)
    try {
      const user = await addUser(form)
      toast.success(`已创建用户 ${user.name}`)
      setForm(emptyForm)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="grid gap-6">
      <Card>
        <CardHeader>
          <CardTitle>新建用户</CardTitle>
          <CardDescription>POST /api/users，失败提示取自统一响应的 message</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end" onSubmit={submit}>
            <div className="grid gap-2">
              <Label htmlFor="name">姓名</Label>
              <Input
                id="name"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="Alice"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="email">邮箱</Label>
              <Input
                id="email"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                placeholder="alice@example.com"
              />
            </div>
            <Button type="submit" disabled={submitting}>
              创建
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>用户列表</CardTitle>
          <CardDescription>共 {total} 条</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-20">ID</TableHead>
                <TableHead>姓名</TableHead>
                <TableHead>邮箱</TableHead>
                <TableHead>创建时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((user) => (
                <TableRow key={user.id}>
                  <TableCell>{user.id}</TableCell>
                  <TableCell>{user.name}</TableCell>
                  <TableCell>{user.email}</TableCell>
                  <TableCell>{formatTime(user.createdAt)}</TableCell>
                </TableRow>
              ))}
              {users.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="h-24 text-center">
                    {loading ? '加载中...' : '暂无数据'}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>

          <div className="flex items-center justify-end gap-2 text-sm">
            <span className="text-muted-foreground">
              第 {page} / {totalPages} 页
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={loading || page <= 1}
              onClick={() => load(page - 1)}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={loading || page >= totalPages}
              onClick={() => load(page + 1)}
            >
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
