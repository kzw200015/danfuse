package danmaku

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// textDedupWindowMs 跨源按文本去重的时间窗口：校正后相差不超过它的算作同一时刻。
const textDedupWindowMs = 5000

// Track 一个绑定的弹幕连同它的校正参数，是 Merge 的输入。
type Track struct {
	BindingID int64 // 越小越早创建；去重时保留 id 小的
	Platform  Platform
	Offset    float64   // 秒，正数表示弹幕延后
	Scale     float64   // 时间缩放系数，纠正线性漂移
	Items     []Danmaku // 时间相对弹幕源视频，未校正
}

// Merge 读取一集弹幕时的全部处理，与弹幕是否落库无关：
//  1. 校正：t × Scale + Offset，四舍五入到毫秒。为负的丢弃，超出本集时长的保留。
//  2. 按 ID 去重：不同绑定之间平台与原始 ID 都相同的，保留 BindingID 小的那条。
//     兜底同一集的两个绑定实际指向同一个弹幕源，例如番剧的 ep 与同一稿件的 av。
//  3. 按文本去重：按校正后时间排序，归一化后正文相同、相差不超过 5 秒、来自不同绑定的，保留时间早的，
//     时间相同时保留 BindingID 小的。只和已保留的比较，被去掉的不会让后面的再算作重复。
//     同一绑定内部不去重：多人刷同一句话是真实的弹幕密度。
//
// 结果按校正后时间升序。
func Merge(tracks []Track) []Item {
	type idKey struct {
		platform Platform
		sourceID int64
	}
	var entries []entry
	owners := make(map[idKey]int64) // (平台, 原始 ID) → 先拿到它的绑定
	for _, tr := range slices.SortedFunc(slices.Values(tracks), func(a, b Track) int { return cmp.Compare(a.BindingID, b.BindingID) }) {
		for _, d := range tr.Items {
			t := math.Round(float64(d.TimeMs)*tr.Scale + tr.Offset*1000)
			if t < 0 || t > math.MaxInt32 { // 超出 int32 的只会来自异常的 scale
				continue
			}
			key := idKey{tr.Platform, d.SourceID}
			if owner, ok := owners[key]; ok && owner != tr.BindingID {
				continue
			}
			owners[key] = tr.BindingID
			d.TimeMs = int32(t)
			entries = append(entries, entry{Item{Danmaku: d, Platform: tr.Platform}, tr.BindingID})
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.TimeMs, b.TimeMs), cmp.Compare(a.bindingID, b.bindingID), cmp.Compare(a.SourceID, b.SourceID))
	})

	result := make([]Item, 0, len(entries))
	kept := make(map[string][]entry) // 归一化后的正文 → 保留下来的这句话，按时间升序
	for _, e := range entries {
		text := normalize(e.Text)
		if duplicated(kept[text], e) {
			continue
		}
		kept[text] = append(kept[text], e)
		result = append(result, e.Item)
	}
	return result
}

// entry 合并过程中的一条弹幕，记着它来自哪个绑定。
type entry struct {
	Item
	bindingID int64
}

// duplicated 从最近的往前看窗口内保留下来的同一句话（kept 按时间升序，都不晚于 e），有来自其他绑定的就算重复。
func duplicated(kept []entry, e entry) bool {
	for i := len(kept) - 1; i >= 0 && e.TimeMs-kept[i].TimeMs <= textDedupWindowMs; i-- {
		if kept[i].bindingID != e.bindingID {
			return true
		}
	}
	return false
}

// normalize 按文本去重时比较的形式：NFKC，去掉所有空白，英文字母转小写。
// 不做简繁统一，也不压缩重复字符。
func normalize(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return -1
		case 'A' <= r && r <= 'Z':
			return r + 'a' - 'A'
		}
		return r
	}, norm.NFKC.String(text))
}
