/**
 * 地址栏里的 ID（目录页的剧、同步页的 ?run=）：规范写法的正整数，否则为 undefined。
 * "01""1e1"都不算，与季、集 ID 按字符串比较的结果一致。
 */
export function parseId(param: string) {
  return /^[1-9]\d*$/.test(param) ? Number(param) : undefined
}
