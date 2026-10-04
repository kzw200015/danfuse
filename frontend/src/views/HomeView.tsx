import { useEffect, useState } from 'react'

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
import { cn } from '@/lib/utils'

interface CheckResult {
  status: 'ok' | 'error'
  message: string
}

async function checkHealth(): Promise<CheckResult> {
  try {
    await getHealth()
    return { status: 'ok', message: '后端与数据库连接正常' }
  } catch (err) {
    return {
      status: 'error',
      message: err instanceof ApiError ? `${err.message}（code: ${err.code}）` : String(err),
    }
  }
}

export default function HomeView() {
  // 挂载后立即发起首次检查，因此初始即为 loading
  const [loading, setLoading] = useState(true)
  const [result, setResult] = useState<CheckResult | null>(null)

  useEffect(() => {
    // 组件卸载后丢弃过期结果
    let ignore = false
    void checkHealth().then((res) => {
      if (!ignore) {
        setResult(res)
        setLoading(false)
      }
    })
    return () => {
      ignore = true
    }
  }, [])

  async function recheck() {
    setLoading(true)
    setResult(await checkHealth())
    setLoading(false)
  }

  return (
    <Card className="max-w-md">
      <CardHeader>
        <CardTitle>服务状态</CardTitle>
        <CardDescription>调用 GET /api/health 检查后端与数据库</CardDescription>
      </CardHeader>
      <CardContent>
        {result === null ? (
          <p className="text-sm text-muted-foreground">检查中...</p>
        ) : (
          <p
            className={cn(
              'text-sm',
              result.status === 'ok' ? 'text-emerald-600' : 'text-destructive',
            )}
          >
            {result.message}
          </p>
        )}
      </CardContent>
      <CardFooter>
        <Button variant="outline" disabled={loading} onClick={recheck}>
          重新检查
        </Button>
      </CardFooter>
    </Card>
  )
}
