/**
 * 图片的地址，交给 <img> 加载。图片接口是统一响应约定的例外：成功时直接返回原始字节，不经过 request()。
 * 同一个 ID 的内容不会变（海报换了会换新 ID），浏览器可以永久缓存。
 */
export function imageUrl(id: number) {
  return `/api/images/${id}`
}
