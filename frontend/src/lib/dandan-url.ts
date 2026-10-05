/**
 * 填进播放器（jellyfin-danmaku 插件等）的弹弹 API 地址：弹弹 API 挂在 /dandanplay[/<token>]/api/v2，
 * 客户端会在填写的地址后面拼 /api/v2，所以地址到 token 为止，末尾不带 /。
 */
export function dandanUrl(origin: string, token: string | null) {
  return token ? `${origin}/dandanplay/${token}` : `${origin}/dandanplay`
}
