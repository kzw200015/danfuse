// Package catalog 目录的规则，以及目录源适配与同步核心之间的接口。
// 只有类型和纯计算，不访问数据库：写库的同步核心在 service.SyncService，
// 各目录源的适配在子包里（jellyfin），由 app 按配置的 kind 选择。
//
// 划分原则：目录本身的规则放在核心；取决于目录源怎么表示数据的，放在适配。
package catalog

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/fulltext"
)

// SeriesType 剧的类型，与 series.type 列的取值一致。
type SeriesType string

const (
	TypeTV    SeriesType = "tv"
	TypeMovie SeriesType = "movie"
)

// Source 目录源适配。
type Source interface {
	// List 列出配置的所有媒体库里要同步的剧，只列清单，不取季和集。
	// 配置的媒体库都不可用、或请求失败时返回 error，这次同步失败。
	List(ctx context.Context) (Listing, error)
}

// Listing 一次 List 的结果。
type Listing struct {
	Total    int      // 剧（含电影）的总数，用于同步进度
	Warnings []string // 列清单时跳过的媒体库（找不到、类型不对）
	// Items 按固定顺序逐部取季和集，每部列出的剧恰好产出一项（被跳过的也算），所以进度一定能走满。
	//   - 只在调用方要下一项时才发请求，所以每部剧的网络请求都在调用方的事务之外；ctx 沿用 List 的；
	//   - 调用方 break 时适配器立即停止，不再请求；
	//   - 请求失败时产出 (Item{}, err) 后结束，这次同步失败。
	Items iter.Seq2[Item, error]
}

// Item 一部剧的取回结果。
type Item struct {
	Name     string   // 剧在目录源里的名称，核心用它给这部剧的警告加上剧名；Series 为 nil 时也有
	Series   *Series  // nil 表示整部跳过（没有任何有效的集），原因在 Warnings 里
	Warnings []string // 这部剧里跳过的项（无效的集、范围异常的多集文件）
}

// Series 适配交给核心的一部剧，与表结构一一对应。电影由适配合成为第 1 季第 1 集。
// 同一个自然键可以出现多次（例如同一集的两个版本），核心按顺序 upsert，后写的覆盖先写的。
//
// 海报有三种状态，决定核心怎么处理，属于数据，不放进 Item.Warnings：
//   - Poster 不为 nil：有图，与现有海报的 sha256 不同时换图；
//   - Poster 与 PosterErr 都为 nil：目录源里没有图，清空这部剧的海报；
//   - PosterErr 不为 nil：下载失败，保留现有海报并记警告。
type Series struct {
	Type          SeriesType
	Title         string
	OriginalTitle string // 空串存为 null
	Year          *int
	Poster        *Image
	PosterErr     error
	Seasons       []Season
}

type Season struct {
	Number   int
	Title    string // 目录源给的季标题，空串存为 null
	Episodes []Episode
}

type Episode struct {
	Number   int
	Title    string // 空串存为 null
	Duration *int   // 秒，取不到时为 nil
}

// Image 一张图片：原始字节，以及目录源给的 content-type。
type Image struct {
	ContentType string
	Data        []byte
}

// Validate 核心的防御性校验：类型有效、标题非空、至少一季、每季至少一集、编号 ≥ 0、
// 电影恰好只有第 1 季第 1 集。不合格的整部剧跳过，返回的 error 文本作为警告记入同步记录。
func (s Series) Validate() error {
	if s.Type != TypeTV && s.Type != TypeMovie {
		return fmt.Errorf("类型 %q 无效", s.Type)
	}
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("标题为空")
	}
	if len(s.Seasons) == 0 {
		return errors.New("没有任何季")
	}
	for _, season := range s.Seasons {
		if season.Number < 0 {
			return fmt.Errorf("季号 %d 为负数", season.Number)
		}
		if len(season.Episodes) == 0 {
			return fmt.Errorf("第 %d 季没有任何集", season.Number)
		}
		for _, ep := range season.Episodes {
			if ep.Number < 0 {
				return fmt.Errorf("第 %d 季的集号 %d 为负数", season.Number, ep.Number)
			}
		}
	}
	if s.Type == TypeMovie {
		if len(s.Seasons) != 1 || s.Seasons[0].Number != 1 ||
			len(s.Seasons[0].Episodes) != 1 || s.Seasons[0].Episodes[0].Number != 1 {
			return errors.New("电影只能有第 1 季第 1 集")
		}
	}
	return nil
}

// SeasonLabel 季号标签：剧集的每一季都有，第 0 季为"特别篇"，其余为"第N季"（包括第 1 季）；电影没有。
// 用来拼季的名称；ParseName 认得这两种写法，按季号精确过滤。
func SeasonLabel(t SeriesType, number int) string {
	switch {
	case t == TypeMovie:
		return ""
	case number == 0:
		return "特别篇"
	default:
		return fmt.Sprintf("第%d季", number)
	}
}

// SeasonName 季对外的名称（弹弹play 的 animeTitle）：第 1 季和电影为"剧名"，第 N 季为"剧名 第N季"，
// 第 0 季为"剧名 特别篇"，都不带年份。ParseName 能从它拆回剧名和季号，拿它搜索能找回这一季。
func SeasonName(t SeriesType, seriesTitle string, number int) string {
	if t == TypeMovie || number == 1 {
		return seriesTitle
	}
	return seriesTitle + " " + SeasonLabel(t, number)
}

// SearchVector 一季的搜索列（tsvector 文本）：A 档为剧名和季号，B 档为原名，C 档为目录源给的季标题。
// 季号只以数字出现（剧集的第 1 季起，特别篇和电影没有），给"剧名2"这种没有标注的写法用：光看数字分不出是标题的一部分
// 还是季号，交给全文搜索，数字在标题里、季号里都能命中。写明了的季号（"第2季"、"S02"、"特别篇"）由 ParseName 拆出，
// 按季号精确过滤，不经过搜索列。季号放在最后，不挪动前面各词的位置：同一部剧的各季按剧名、原名搜索时得分相同，
// 先后由季号决定（ts_rank 会看命中的词之间的距离）。
// 同步写入季时用它计算；改动它的组成时，要新增一个 goose Go 迁移，重算所有季的搜索列。
func SearchVector(t SeriesType, seriesTitle, originalTitle string, number int, seasonTitle string) string {
	seasonNumber := ""
	if t == TypeTV && number > 0 {
		seasonNumber = strconv.Itoa(number)
	}
	return fulltext.Vector(
		fulltext.Field{Weight: fulltext.WeightA, Text: seriesTitle},
		fulltext.Field{Weight: fulltext.WeightB, Text: originalTitle},
		fulltext.Field{Weight: fulltext.WeightC, Text: seasonTitle},
		fulltext.Field{Weight: fulltext.WeightA, Text: seasonNumber},
	)
}
