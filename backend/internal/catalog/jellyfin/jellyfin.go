// Package jellyfin Jellyfin 目录源适配（10.11 及以上，不探测版本）。
//
// 文件划分：
//   - jellyfin.go：catalog.Source 的实现，按"媒体库 → 剧 → 季和集"的固定顺序产出；
//   - client.go：HTTP 客户端与 PascalCase 的响应类型；
//   - mapping.go：字段映射、季号取集的 ParentIndexNumber、多集展开、电影合成。
package jellyfin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
)

type Source struct {
	client    *client
	libraries []string
}

var _ catalog.Source = (*Source)(nil)

// New 只保存配置，不连 Jellyfin：Jellyfin 没开时 danfuse 也要能启动。
func New(cfg config.Jellyfin) *Source {
	return &Source{
		client:    &client{baseURL: cfg.URL, apiKey: cfg.APIKey, listTimeout: cfg.ListTimeout, posterTimeout: cfg.PosterTimeout},
		libraries: cfg.Libraries,
	}
}

// List 见 catalog.Source。
//  1. GET /Library/VirtualFolders，按名字找配置的媒体库；找不到或类型不对的放进 Warnings，全部不可用则失败；
//  2. 媒体库按名称排序，逐个列出剧和电影（带上 ProviderIds），各自按 Id 排序，得到 Total；
//  3. Items 逐部取季和集、下载海报，见 items。
func (s *Source) List(ctx context.Context) (catalog.Listing, error) {
	folders, err := s.client.virtualFolders(ctx)
	if err != nil {
		return catalog.Listing{}, failed("列出媒体库失败", err)
	}
	libraries, skipped := pickLibraries(s.libraries, folders)
	if len(libraries) == 0 {
		return catalog.Listing{}, &catalog.Error{Message: "配置的媒体库都不可用：" + strings.Join(skipped, "；")}
	}
	warnings := make([]string, len(skipped))
	for i, reason := range skipped {
		warnings[i] = reason + "，已跳过"
	}

	var listed []item
	for _, lib := range libraries {
		items, err := s.client.listItems(ctx, lib.ItemID, typeSeries+","+typeMovie, "OriginalTitle", "ProviderIds")
		if err != nil {
			return catalog.Listing{}, failed(fmt.Sprintf("列出媒体库「%s」的剧和电影失败", lib.Name), err)
		}
		slices.SortFunc(items, byID)
		listed = append(listed, items...)
	}
	return catalog.Listing{Total: len(listed), Warnings: warnings, Items: s.items(ctx, listed)}, nil
}

// pickLibraries 按名字找到配置的媒体库（去重并按名称排序）；找不到的、类型不是剧集或电影的跳过，返回跳过的原因。
func pickLibraries(names []string, folders []virtualFolder) (libraries []virtualFolder, skipped []string) {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	for _, name := range names {
		i := slices.IndexFunc(folders, func(f virtualFolder) bool { return f.Name == name })
		switch {
		case i < 0:
			skipped = append(skipped, fmt.Sprintf("找不到媒体库「%s」", name))
		case folders[i].CollectionType != "tvshows" && folders[i].CollectionType != "movies":
			skipped = append(skipped, fmt.Sprintf("媒体库「%s」的类型不是剧集或电影", name))
		default:
			libraries = append(libraries, folders[i])
		}
	}
	return libraries, skipped
}

// items Listing.Items 的实现：列出的剧和电影已经带着名称、年份、外部 id 和图片 tag，不用再请求一次；
// 电影自带时长，不用取季和集。请求都在调用方要下一项时才发：剧取季和集，有 Primary 图的再下载海报。
// 海报下载失败只放进这部剧的 PosterErr，不结束迭代；整部跳过的剧不下载海报。
// 没有刮削的剧和电影整部跳过，不发请求，见 scraped。
func (s *Source) items(ctx context.Context, listed []item) iter.Seq2[catalog.Item, error] {
	return func(yield func(catalog.Item, error) bool) {
		for _, it := range listed {
			var result catalog.Item
			switch {
			case !scraped(it):
				result = catalog.Item{Name: displayName(it), Warnings: []string{"没有刮削元数据，整部跳过"}}
			case it.Type == typeMovie:
				result = mapMovie(it)
			default:
				children, err := s.client.listItems(ctx, it.ID, typeSeason+","+typeEpisode)
				if err != nil {
					yield(catalog.Item{}, failed(fmt.Sprintf("取「%s」的季和集失败", displayName(it)), err))
					return
				}
				result = mapSeries(it, children)
			}
			if result.Series != nil && it.ImageTags["Primary"] != "" {
				result.Series.Poster, result.Series.PosterErr = s.client.primaryImage(ctx, it.ID)
			}
			if !yield(result, nil) {
				return
			}
		}
	}
}

// scraped 剧或电影是否刮削过：ProviderIds 里有任意一个外部数据库的 id。
// 没刮削的剧标题、年份来自文件夹名，刮削后会变，没有 TMDB id 的剧按标题和年份对应，同步就会多出一部剧，所以不同步。
// 文件夹名里带的 id（如 [tmdbid-123]）也算，Jellyfin 通常会随后按它刮削。
func scraped(it item) bool {
	for _, id := range it.ProviderIDs {
		if strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

// failed 给 client 返回的错误加上在做什么，例如"列出媒体库失败：无法连接 Jellyfin"；底层原因不变。
func failed(what string, err error) *catalog.Error {
	if e, ok := errors.AsType[*catalog.Error](err); ok {
		return &catalog.Error{Message: what + "：" + e.Message, Err: e.Err}
	}
	return &catalog.Error{Message: what, Err: err}
}

// byID 按 Jellyfin Id 排序，处理顺序因此固定：同一个自然键出现多次时，后写的覆盖先写的，每次结果相同。
func byID(a, b item) int { return cmp.Compare(a.ID, b.ID) }
