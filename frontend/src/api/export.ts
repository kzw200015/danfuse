/**
 * 导出的下载地址，交给 <a href download> 由浏览器直接下载。导出接口是统一响应约定的例外：成功时直接返回文件，
 * 不经过 request()，页面不用先把整个文件读进内存；出错时浏览器显示下载失败。
 */
export function bindingExportUrl(id: number) {
  return `/api/bindings/${id}/export`
}
