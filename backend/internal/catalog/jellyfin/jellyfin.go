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
		client:    &client{baseURL: cfg.URL, apiKey: cfg.APIKey},
		libraries: cfg.Libraries,
	}
}

// List 见 catalog.Source。
//  1. GET /Library/VirtualFolders，按名字找配置的媒体库；找不到或类型不对的放进 Warnings，全部不可用则失败；
//  2. 媒体库按名称排序，逐个列出剧和电影，各自按 Id 排序，得到 Total；
//  3. Items 逐部取季和集、下载海报，见 items。
func (s *Source) List(ctx context.Context) (catalog.Listing, error) {
	folders, err := s.client.virtualFolders(ctx)
	if err != nil {
		return catalog.Listing{}, fmt.Errorf("列出媒体库失败：%w", err)
	}
	libraries, skipped := pickLibraries(s.libraries, folders)
	if len(libraries) == 0 {
		return catalog.Listing{}, fmt.Errorf("配置的媒体库都不可用：%s", strings.Join(skipped, "；"))
	}
	warnings := make([]string, len(skipped))
	for i, reason := range skipped {
		warnings[i] = reason + "，已跳过"
	}

	var listed []item
	for _, lib := range libraries {
		items, err := s.client.items(ctx, lib.ItemID, typeSeries+","+typeMovie, "OriginalTitle")
		if err != nil {
			return catalog.Listing{}, fmt.Errorf("列出媒体库「%s」的剧和电影失败：%w", lib.Name, err)
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

// items Listing.Items 的实现：列出的剧和电影已经带着名称、年份和图片 tag，不用再请求一次；
// 电影自带时长，不用取季和集。请求都在调用方要下一项时才发：剧取季和集，有 Primary 图的再下载海报。
// 海报下载失败只放进这部剧的 PosterErr，不结束迭代；整部跳过的剧不下载海报。
func (s *Source) items(ctx context.Context, listed []item) iter.Seq2[catalog.Item, error] {
	return func(yield func(catalog.Item, error) bool) {
		for _, it := range listed {
			var result catalog.Item
			if it.Type == typeMovie {
				result = mapMovie(it)
			} else {
				children, err := s.client.items(ctx, it.ID, typeSeason+","+typeEpisode)
				if err != nil {
					yield(catalog.Item{}, fmt.Errorf("取「%s」的季和集失败：%w", displayName(it), err))
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

// byID 按 Jellyfin Id 排序，处理顺序因此固定：同一个自然键出现多次时，后写的覆盖先写的，每次结果相同。
func byID(a, b item) int { return cmp.Compare(a.ID, b.ID) }
