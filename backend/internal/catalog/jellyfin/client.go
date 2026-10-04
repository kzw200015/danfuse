package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
)

// 单个请求的超时。
const (
	listTimeout   = 2 * time.Minute  // 列出条目
	posterTimeout = 30 * time.Second // 下载一张海报
)

// posterMaxWidth 海报按这个宽度缩小后下载，够管理界面显示。
const posterMaxWidth = "400"

// client Jellyfin 的 HTTP 客户端，超时都由 ctx 控制。鉴权固定用 `Authorization: MediaBrowser Token="<api_key>"` 请求头，
// 不用查询参数，这样错误信息里的 URL 不会带上 key。api_key 不写日志。
type client struct {
	baseURL string // 已去掉末尾的 /
	apiKey  string
}

// virtualFolder /Library/VirtualFolders 的一项，即一个媒体库。
type virtualFolder struct {
	Name           string `json:"Name"`
	ItemID         string `json:"ItemId"`
	CollectionType string `json:"CollectionType"` // tvshows | movies | ...；不指定类型的媒体库没有这个字段
}

// item /Items 返回的条目，只取用到的字段。Jellyfin 省略空字段，可能缺省的编号都是指针。
type item struct {
	ID                string            `json:"Id"`   // 32 位小写十六进制
	Type              string            `json:"Type"` // Series | Movie | Season | Episode
	Name              string            `json:"Name"`
	OriginalTitle     string            `json:"OriginalTitle"`
	ProductionYear    *int              `json:"ProductionYear"`
	IndexNumber       *int              `json:"IndexNumber"`       // 季条目为季号，集条目为集号
	IndexNumberEnd    *int              `json:"IndexNumberEnd"`    // 只在多集文件上出现
	ParentIndexNumber *int              `json:"ParentIndexNumber"` // 集所在的季号
	RunTimeTicks      *int64            `json:"RunTimeTicks"`      // 1 秒 = 10^7 tick
	SeriesID          string            `json:"SeriesId"`          // 季和集实际所在的剧
	SeasonName        string            `json:"SeasonName"`        // 集所在季的名称，只用于警告
	ImageTags         map[string]string `json:"ImageTags"`         // 图片类型 → tag；有 Primary 的剧和电影才下载海报
}

const (
	typeSeries  = "Series"
	typeMovie   = "Movie"
	typeSeason  = "Season"
	typeEpisode = "Episode"
)

func (c *client) virtualFolders(ctx context.Context) ([]virtualFolder, error) {
	var folders []virtualFolder
	if err := c.getJSON(ctx, "/Library/VirtualFolders", nil, &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// items GET /Items，ParentId 下递归列出 types 类型的条目。
//   - 不传 userId：12.x 带上它时会把按剧名合并的剧去重，同名不同年的剧整部丢失；
//   - 不传 Limit、不分页：偏移分页在库内容变化时会重复或漏项。
func (c *client) items(ctx context.Context, parentID, types string, fields ...string) ([]item, error) {
	query := url.Values{
		"ParentId":         {parentID},
		"IncludeItemTypes": {types},
		"Recursive":        {"true"},
		"IsMissing":        {"false"},
	}
	for _, f := range fields {
		query.Add("Fields", f)
	}
	var resp struct {
		Items []item `json:"Items"`
	}
	if err := c.getJSON(ctx, "/Items", query, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// primaryImage 下载条目的主海报（缩到 posterMaxWidth 宽），content-type 取响应头。
// 不发 Accept：Jellyfin 按 Accept 选输出格式，声明接受 image/webp 时一律转成 WebP；不声明时 JPEG、PNG 保持原格式。
//
// content-type 不合格时算下载失败：
//   - 不是 image/*：合理性检查，例如反向代理返回的登录页不能当作海报；
//   - 是 SVG：安全考虑，图片接口原样输出存下的 content-type，从 danfuse 同源输出的 SVG 可以执行脚本。
func (c *client) primaryImage(ctx context.Context, itemID string) (*catalog.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, posterTimeout)
	defer cancel()

	path := "/Items/" + itemID + "/Images/Primary"
	resp, err := c.get(ctx, path, url.Values{"maxWidth": {posterMaxWidth}}, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	contentType := resp.Header.Get("Content-Type")
	switch mediaType, _, _ := mime.ParseMediaType(contentType); {
	case !strings.HasPrefix(mediaType, "image/"):
		return nil, fmt.Errorf("GET %s: 响应不是图片（Content-Type: %s）", path, contentType)
	case mediaType == "image/svg+xml":
		return nil, fmt.Errorf("GET %s: 不接受 SVG 格式的海报", path)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read response: %w", path, err)
	}
	return &catalog.Image{ContentType: contentType, Data: data}, nil
}

// getJSON GET 一个 JSON 接口，把响应解码进 out。
func (c *client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	resp, err := c.get(ctx, path, query, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("GET %s: decode response: %w", path, err)
	}
	return nil
}

// get 发一个 GET，accept 为空时不发 Accept 请求头；状态码不是 200 时返回错误，否则由调用方关闭响应体。
func (c *client) get(ctx context.Context, path string, query url.Values, accept string) (*http.Response, error) {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+c.apiKey+`"`)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// 失败原因会显示在同步页上：只留路径和底层原因，不带完整的 URL 与查询串
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return resp, nil
}
