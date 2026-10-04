import { useState, type SubmitEvent } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { ApiError } from '@/api/request'
import { createUser, listUsers, type CreateUserPayload } from '@/api/user'
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
import { cn } from '@/lib/utils'

const PAGE_SIZE = 10

const emptyForm: CreateUserPayload = { name: '', email: '' }

function errorMessage(err: unknown) {
  return err instanceof ApiError ? err.message : '未知错误'
}

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}

export default function UsersView() {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [form, setForm] = useState(emptyForm)

  const { data, error, isPending, isFetching } = useQuery({
    queryKey: ['users', { page, pageSize: PAGE_SIZE }],
    queryFn: () => listUsers({ page, pageSize: PAGE_SIZE }),
    // 翻页时保留上一页数据，避免表格闪空
    placeholderData: keepPreviousData,
  })
  const users = data?.list ?? []
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const create = useMutation({
    mutationFn: createUser,
    onSuccess: (user) => {
      toast.success(`已创建用户 ${user.name}`)
      setForm(emptyForm)
      setPage(1)
      // 返回 Promise，列表刷新完成前按钮保持禁用
      return queryClient.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    create.mutate(form)
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
            <Button type="submit" disabled={create.isPending}>
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
                  <TableCell
                    colSpan={4}
                    className={cn('h-24 text-center', error && 'text-destructive')}
                  >
                    {error ? errorMessage(error) : isPending ? '加载中...' : '暂无数据'}
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
              disabled={isFetching || page <= 1}
              onClick={() => setPage(page - 1)}
            >
              上一页
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={isFetching || page >= totalPages}
              onClick={() => setPage(page + 1)}
            >
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
