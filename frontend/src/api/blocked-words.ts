import { request } from './request'

/** keyword 关键词：归一化后包含即命中；regex 正则：RE2 语法，对原始正文匹配 */
export type BlockedWordKind = 'keyword' | 'regex'

/** 屏蔽词：正文命中的弹幕不由弹弹 API 输出，对应后端 blockword.BlockedWord */
export interface BlockedWord {
  id: number
  kind: BlockedWordKind
  /** 去掉首尾空白的原文 */
  pattern: string
  createdAt: string
}

/** 全部屏蔽词，新加的在前 */
export function listBlockedWords() {
  return request<BlockedWord[]>({ url: '/blocked-words' })
}

/** 新增一条屏蔽词；内容不合法时 400，已有相同的时 409 */
export function createBlockedWord(kind: BlockedWordKind, pattern: string) {
  return request<BlockedWord>({ url: '/blocked-words', method: 'POST', data: { kind, pattern } })
}

export function deleteBlockedWord(id: number) {
  return request<null>({ url: `/blocked-words/${id}`, method: 'DELETE' })
}
