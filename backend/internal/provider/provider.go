// Package provider 聚合层与 Provider 之间的约定，以及聚合层本身。
// 这里的类型是系统内部的结构，不是弹弹play 的 JSON：协议参数的转换与格式化归弹弹 API（dandan 包）。
// 本地 Provider 要查库，实现在 service.LocalProvider。
package provider

import (
	"context"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// LocalIDLimit 本地的季、集 ID 恒小于它；以后的上游 Provider 用大于等于它的虚拟 ID。
const LocalIDLimit int64 = 10_000_000_000_000

// Provider 回答搜索与取弹幕的一方。本地目录和以后的每个上游平台各是一个 Provider。
type Provider interface {
	// Search 按自己的相关度排好序返回，最多 q.MaxSeasons 季；后面还有时 HasMore 为 true。
	// 不返回能跨 Provider 比较的分数。
	Search(ctx context.Context, q SearchQuery) (SearchResult, error)
	// Comments 一集的全部弹幕，已校正、跨源去重、按时间升序。集不存在或没有弹幕时返回空，不是错误。
	Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error)
}

// SearchQuery 搜索条件。往里加字段不会破坏现有调用方，所以不为未实现的协议参数预留字段。
type SearchQuery struct {
	Keyword    string // 用户输入的原文，不清洗、不分词，由各 Provider 自己处理
	MaxSeasons int
}

type SearchResult struct {
	Seasons []Season
	HasMore bool
}

// SeasonKind 季的类别。
type SeasonKind int

const (
	KindSeries  SeasonKind = iota + 1 // 剧集的第 1 季及以后
	KindSpecial                       // 剧集的第 0 季
	KindMovie
)

type Season struct {
	ID       int64  // 本地 ID，或以后的虚拟 ID
	Name     string // 本地按 catalog.SeasonName 拼
	Kind     SeasonKind
	Year     *int
	Number   *int      // 季号，以后的上游可能没有
	Episodes []Episode // 按集号升序
}

type Episode struct {
	ID     int64
	Number int
	Title  string // 可以为空
}

// Aggregator 聚合层，实现同一个接口。现在只有本地 Provider：搜索只问本地；取弹幕按 ID 号段路由，
// 本地以外的号段还没有 Provider，返回空。多个 Provider 的合并规则在接入上游 Provider 时再落实。
type Aggregator struct {
	local Provider
}

var _ Provider = (*Aggregator)(nil)

func NewAggregator(local Provider) *Aggregator {
	return &Aggregator{local: local}
}

func (a *Aggregator) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	return a.local.Search(ctx, q)
}

func (a *Aggregator) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	if episodeID <= 0 || episodeID >= LocalIDLimit {
		return nil, nil
	}
	return a.local.Comments(ctx, episodeID)
}
