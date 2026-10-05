// Package provider 聚合层与 Provider 之间的约定，以及聚合层本身。
// 这里的类型是系统内部的结构，不是弹弹play 的 JSON：协议参数的转换与格式化归弹弹 API（dandan 包）。
// 本地 Provider 要查库，实现在 service.LocalProvider。
package provider

import (
	"context"
	"slices"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
)

// LocalIDLimit 本地的季、集 ID 恒小于它；以后的上游 Provider 用大于等于它的虚拟 ID。
const LocalIDLimit int64 = 10_000_000_000_000

// Provider 回答搜索与取弹幕的一方。本地目录和以后的每个上游平台各是一个 Provider。
type Provider interface {
	// Search 按自己的相关度排好序返回，最多 q.MaxSeasons 季；后面还有时 HasMore 为 true。
	// 不返回能跨 Provider 比较的分数。
	Search(ctx context.Context, q SearchQuery) (SearchResult, error)
	// Season 按 ID 取一季，内容与搜索结果里的同一季相同。季不存在时 found 为 false，不是错误。
	Season(ctx context.Context, id int64) (season Season, found bool, err error)
	// Comments 一集的全部弹幕，已校正、跨源去重、按时间升序。集不存在或没有弹幕时返回空，不是错误。
	Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error)
}

// SearchQuery 搜索条件。往里加字段不会破坏现有调用方，所以不为未实现的协议参数预留字段。
type SearchQuery struct {
	// Keyword 用户输入的原文（或 match 的文件名），不清洗、不分词，由各 Provider 自己处理；
	// 其中写明了的季号、集号按 catalog.ParseName 认出，只返回这一季、这一集。
	Keyword    string
	MaxSeasons int
	Episode    *int // 集号，优先于 Keyword 里写明的集号（协议参数比关键词里推断的更明确）
}

// SearchResult 搜索结果。按集号过滤时（SearchQuery.Episode 或关键词里写明的集号），只返回有这一集的季，
// 每季的 Episodes 恰好是这一集。
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
	ID           int64    // 本地 ID，或以后的虚拟 ID
	Name         string   // 本地按 catalog.SeasonName 拼
	Titles       []string // 所属剧的标题，有原名时加上原名；识别时用来判断文件名里的标题是不是这部剧
	Kind         SeasonKind
	Year         *int
	Number       *int      // 季号，以后的上游可能没有
	Episodes     []Episode // 按集号升序
	EpisodeCount int       // 这一季的总集数；按集号过滤时 Episodes 只有那一集，总集数不变
}

type Episode struct {
	ID     int64
	Number int
	Title  string // 可以为空
}

// Aggregator 聚合层，实现同一个接口，另外按名称识别一集（Match）。现在只有本地 Provider：搜索只问本地；
// 取季、取弹幕按 ID 号段路由，本地以外的号段还没有 Provider，返回空。多个 Provider 的合并规则在接入上游 Provider 时再落实。
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

// MatchResult 识别的结果。Candidates 按可能性排序，每个是一季，Episodes 恰好是认出的那一集；
// Exact 为 true 时恰好一个候选，而且名称里的标题就是这部剧的标题或原名，可以直接采用。
type MatchResult struct {
	Candidates []Season
	Exact      bool
}

// Match 按名称（match 的文件名）识别一集。名称里要写明集号（catalog.ParseName），否则没有候选；
// 其余与搜索相同：写明的季号只要这一季，有这一集的季都是候选，按搜索的排序，最多 maxCandidates 个。
// 候选唯一不算确定：名称里的标题可能只是剧名中的一段，标题与剧名或原名的词完全相同（fulltext.SameWords）才是 Exact。
func (a *Aggregator) Match(ctx context.Context, name string, maxCandidates int) (MatchResult, error) {
	parsed := catalog.ParseName(name)
	if parsed.Episode == nil {
		return MatchResult{}, nil
	}
	result, err := a.Search(ctx, SearchQuery{Keyword: name, MaxSeasons: maxCandidates})
	if err != nil {
		return MatchResult{}, err
	}
	exact := len(result.Seasons) == 1 && slices.ContainsFunc(result.Seasons[0].Titles, func(t string) bool {
		return fulltext.SameWords(t, parsed.Title)
	})
	return MatchResult{Candidates: result.Seasons, Exact: exact}, nil
}

func (a *Aggregator) Season(ctx context.Context, id int64) (Season, bool, error) {
	if !isLocal(id) {
		return Season{}, false, nil
	}
	return a.local.Season(ctx, id)
}

func (a *Aggregator) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	if !isLocal(episodeID) {
		return nil, nil
	}
	return a.local.Comments(ctx, episodeID)
}

// isLocal ID 是否在本地号段内。
func isLocal(id int64) bool {
	return id > 0 && id < LocalIDLimit
}
