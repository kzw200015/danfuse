/**
 * 填进 jellyfin-danmaku 插件的弹幕服务器地址：弹弹 API 挂在 /dandanplay[/<token>]/api/v2，
 * 插件会在填写的地址后面拼 /api/v2，所以地址到 token 为止，末尾不带 /。
 */
export function pluginUrl(origin: string, token: string | null) {
  return token ? `${origin}/dandanplay/${token}` : `${origin}/dandanplay`
}
