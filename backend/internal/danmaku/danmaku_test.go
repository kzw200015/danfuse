package danmaku

import (
	"slices"
	"testing"
)

// track 一个绑定的弹幕，不缩放（scale 为 1）。
func track(bindingID int64, p Platform, offset float64, items ...Danmaku) Track {
	return Track{BindingID: bindingID, Platform: p, Offset: offset, Scale: 1, Items: items}
}

// dm 一条滚动、黑色的弹幕。
func dm(sourceID int64, timeMs int32, text string) Danmaku {
	return Danmaku{TimeMs: timeMs, Mode: ModeScroll, Text: text, SourceID: sourceID}
}

// out 合并后输出的一条弹幕：d 的时间换成校正后的 timeMs。
func out(p Platform, d Danmaku, timeMs int32) Item {
	d.TimeMs = timeMs
	return Item{Danmaku: d, Platform: p}
}

func TestMerge(t *testing.T) {
	const bili = PlatformBilibili
	styled := Danmaku{TimeMs: 2000, Mode: ModeReverse, Color: 0xE70012, Text: "逆向", SourceID: 9}

	tests := []struct {
		name   string
		tracks []Track
		want   []Item
	}{
		{"没有绑定", nil, nil},
		{"绑定没有弹幕", []Track{track(1, bili, 0)}, nil},
		{
			"offset 以秒为单位，正数表示弹幕延后；模式、颜色、正文、原始 ID 原样保留",
			[]Track{track(1, bili, 1.5, styled)},
			[]Item{out(bili, styled, 3500)},
		},
		{
			"t × scale + offset，四舍五入到毫秒",
			[]Track{{BindingID: 1, Platform: bili, Offset: 0.25, Scale: 0.5, Items: []Danmaku{
				dm(1, 1001, "a"), // 500.5 + 250 → 751
				dm(2, 3000, "b"), // 1500 + 250
			}}, {BindingID: 2, Platform: bili, Scale: 1.04, Items: []Danmaku{
				dm(3, 10012, "c"), // 10412.48 → 10412
				dm(4, 10013, "d"), // 10413.52 → 10414
			}}},
			[]Item{out(bili, dm(1, 1001, "a"), 751), out(bili, dm(2, 3000, "b"), 1750), out(bili, dm(3, 10012, "c"), 10412), out(bili, dm(4, 10013, "d"), 10414)},
		},
		{
			"校正后为负的丢弃，为 0 的保留",
			[]Track{track(1, bili, -1.5, dm(1, 1000, "a"), dm(2, 1499, "b"), dm(3, 1500, "c"), dm(4, 1501, "d"))},
			[]Item{out(bili, dm(3, 1500, "c"), 0), out(bili, dm(4, 1501, "d"), 1)},
		},
		{
			"按校正后时间升序，与输入顺序无关",
			[]Track{
				track(1, bili, 0, dm(1, 3000, "a"), dm(2, 1000, "b")),
				track(2, bili, 1.5, dm(3, 2000, "c"), dm(4, 0, "d")),
			},
			[]Item{out(bili, dm(2, 1000, "b"), 1000), out(bili, dm(4, 0, "d"), 1500), out(bili, dm(1, 3000, "a"), 3000), out(bili, dm(3, 2000, "c"), 3500)},
		},
		{
			// 绑定按创建先后取舍，与传入的顺序无关；两条相差 10 秒，不是按文本去掉的
			"不同绑定之间平台与原始 ID 都相同的，保留绑定 id 较小的那条",
			[]Track{
				track(2, bili, 10, dm(7, 1000, "前排"), dm(8, 2000, "b")),
				track(1, bili, 0, dm(7, 1000, "前排")),
			},
			[]Item{out(bili, dm(7, 1000, "前排"), 1000), out(bili, dm(8, 2000, "b"), 12000)},
		},
		{
			"平台不同时原始 ID 相同不算重复",
			[]Track{track(1, bili, 0, dm(7, 1000, "a")), track(2, PlatformNone, 0, dm(7, 1000, "b"))},
			[]Item{out(bili, dm(7, 1000, "a"), 1000), out(PlatformNone, dm(7, 1000, "b"), 1000)},
		},
		{
			// 绑定 1 的那条校正后为负被丢掉，不再占着这个原始 ID
			"按 ID 去重在丢弃负时间之后",
			[]Track{track(1, bili, -5, dm(7, 1000, "a")), track(2, bili, 0, dm(7, 1000, "a"))},
			[]Item{out(bili, dm(7, 1000, "a"), 1000)},
		},
		{
			"相差 5 秒以内（含 5 秒）的跨绑定相同正文，保留较早的",
			[]Track{track(1, bili, 0, dm(1, 10000, "草")), track(2, bili, 0, dm(2, 15000, "草"))},
			[]Item{out(bili, dm(1, 10000, "草"), 10000)},
		},
		{
			"相差超过 5 秒的都保留",
			[]Track{track(1, bili, 0, dm(1, 10000, "草")), track(2, bili, 0, dm(2, 15001, "草"))},
			[]Item{out(bili, dm(1, 10000, "草"), 10000), out(bili, dm(2, 15001, "草"), 15001)},
		},
		{
			"较早的那条来自 id 较大的绑定时，保留它",
			[]Track{track(1, bili, 0, dm(1, 12000, "草")), track(2, bili, 0, dm(2, 10000, "草"))},
			[]Item{out(bili, dm(2, 10000, "草"), 10000)},
		},
		{
			"时间相同时保留绑定 id 较小的",
			[]Track{track(2, bili, 0, dm(2, 10000, "草")), track(1, bili, 0, dm(1, 10000, "草"))},
			[]Item{out(bili, dm(1, 10000, "草"), 10000)},
		},
		{
			"按文本去重不看平台",
			[]Track{track(1, bili, 0, dm(1, 10000, "草")), track(2, PlatformNone, 0, dm(2, 11000, "草"))},
			[]Item{out(bili, dm(1, 10000, "草"), 10000)},
		},
		{
			"按校正后的时间比较",
			[]Track{track(1, bili, 0, dm(1, 10000, "草")), track(2, bili, -8, dm(2, 20000, "草"))},
			[]Item{out(bili, dm(1, 10000, "草"), 10000)},
		},
		{
			"同一绑定内的相同正文全部保留",
			[]Track{track(1, bili, 0, dm(1, 0, "哈哈"), dm(2, 1000, "哈哈"), dm(3, 1000, "哈哈")), track(2, bili, 0, dm(4, 500, "哈哈"))},
			[]Item{out(bili, dm(1, 0, "哈哈"), 0), out(bili, dm(2, 1000, "哈哈"), 1000), out(bili, dm(3, 1000, "哈哈"), 1000)},
		},
		{
			// 绑定 2 在 4 秒的那条已被去掉，不再让绑定 1 在 8 秒的那条算作重复
			"只和保留下来的比较",
			[]Track{track(1, bili, 0, dm(1, 0, "草"), dm(3, 8000, "草")), track(2, bili, 0, dm(2, 4000, "草"))},
			[]Item{out(bili, dm(1, 0, "草"), 0), out(bili, dm(3, 8000, "草"), 8000)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Merge(tt.tracks, Blocklist{}); !slices.Equal(got, tt.want) {
				t.Errorf("Merge() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestMergeNormalizesText 按文本去重前的归一化：两个绑定各一条，相差 1 秒，正文归一化后相同才算重复。
func TestMergeNormalizesText(t *testing.T) {
	tests := []struct {
		name     string
		earlier  string
		later    string
		wantDups bool
	}{
		{"完全相同", "前排", "前排", true},
		{"全角与半角", "ＡＢＣ１２３！", "abc123!", true},
		{"去掉所有空白", " 前 排\t\n　", "前排", true},
		{"英文字母不分大小写", "AWSL", "awsl", true},
		{"NFKC 兼容字符", "㍿ ①", "株式会社1", true},
		{"不做简繁统一", "这样", "這樣", false},
		{"不压缩重复字符", "哈哈", "哈哈哈", false},
		{"标点不去掉", "前排", "前排！", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge([]Track{
				track(1, PlatformBilibili, 0, dm(1, 10000, tt.earlier)),
				track(2, PlatformBilibili, 0, dm(2, 11000, tt.later)),
			}, Blocklist{})
			if dups := len(got) == 1; dups != tt.wantDups {
				t.Errorf("%q 与 %q：Merge() = %+v，want 算作重复 = %v", tt.earlier, tt.later, got, tt.wantDups)
			}
		})
	}
}

func TestCID(t *testing.T) {
	// 算法一经发布不能再改：固定几个已知值，改了实现这里就会失败
	tests := []struct {
		platform Platform
		sourceID int64
		want     int64
	}{
		{PlatformBilibili, 1, 5639575707988448},
		{PlatformBilibili, 1983745621937266688, 1881586332506557},
		{PlatformNone, 1, 494350949496718},
		{PlatformNone, 1983745621937266688, 6662812546087605},
	}
	for _, tt := range tests {
		if got := CID(tt.platform, tt.sourceID); got != tt.want {
			t.Errorf("CID(%q, %d) = %d, want %d", tt.platform, tt.sourceID, got, tt.want)
		}
	}

	// 弹弹play 的 cid 在 JavaScript 里按 number 读，不能超过 2^53−1；也不能为 0
	const maxSafeInteger = 1<<53 - 1
	for _, p := range []Platform{PlatformBilibili, PlatformNone} {
		for id := range int64(10000) {
			for _, sourceID := range []int64{id + 1, 1<<62 + id} {
				if got := CID(p, sourceID); got <= 0 || got > maxSafeInteger {
					t.Fatalf("CID(%q, %d) = %d, want 1～2^53−1", p, sourceID, got)
				}
			}
		}
	}
}
