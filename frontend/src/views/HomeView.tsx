import { useQuery } from '@tanstack/react-query'

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

function describeError(err: Error) {
  return err instanceof ApiError ? `${err.message}（code: ${err.code}）` : String(err)
}

export default function HomeView() {
  const { isPending, error, isFetching, refetch } = useQuery({
    queryKey: ['health'],
    queryFn: getHealth,
  })

  return (
    <Card className="max-w-md">
      <CardHeader>
        <CardTitle>服务状态</CardTitle>
        <CardDescription>调用 GET /api/health 检查后端与数据库</CardDescription>
      </CardHeader>
      <CardContent>
        {isPending ? (
          <p className="text-sm text-muted-foreground">检查中...</p>
        ) : error ? (
          <p className="text-sm text-destructive">{describeError(error)}</p>
        ) : (
          <p className="text-sm text-emerald-600">后端与数据库连接正常</p>
        )}
      </CardContent>
      <CardFooter>
        <Button variant="outline" disabled={isFetching} onClick={() => refetch()}>
          重新检查
        </Button>
      </CardFooter>
    </Card>
  )
}
