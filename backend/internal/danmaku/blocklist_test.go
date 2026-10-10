package danmaku

import (
	"slices"
	"strings"
	"testing"
)

func TestParseBlockedWord(t *testing.T) {
	tests := []struct {
		name    string
		kind    BlockedWordKind
		pattern string
		want    BlockedWord
		wantErr string
	}{
		{"关键词去掉首尾空白", BlockedKeyword, "  前排 \n", BlockedWord{BlockedKeyword, "前排"}, ""},
		{"正则去掉首尾空白", BlockedRegex, ` ^\d+$ `, BlockedWord{BlockedRegex, `^\d+$`}, ""},
		{"100 个字符", BlockedKeyword, strings.Repeat("草", 100), BlockedWord{BlockedKeyword, strings.Repeat("草", 100)}, ""},
		{"超过 100 个字符", BlockedKeyword, strings.Repeat("草", 101), BlockedWord{}, "屏蔽词不能超过 100 个字符"},
		{"空的", BlockedKeyword, "", BlockedWord{}, "屏蔽词不能为空"},
		{"只有空白", BlockedRegex, " \t　", BlockedWord{}, "屏蔽词不能为空"},
		{"不合法的正则", BlockedRegex, "(前排", BlockedWord{}, "不是合法的正则：missing closing )"},
		{"不合法的正则可以是合法的关键词", BlockedKeyword, "(前排", BlockedWord{BlockedKeyword, "(前排"}, ""},
		{"未知的类型", "user", "前排", BlockedWord{}, "屏蔽词的类型只能是关键词或正则"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBlockedWord(tt.kind, tt.pattern)
			if errMsg := errString(err); got != tt.want || errMsg != tt.wantErr {
				t.Errorf("ParseBlockedWord(%q, %q) = %+v, %q; want %+v, %q", tt.kind, tt.pattern, got, errMsg, tt.want, tt.wantErr)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestBlockedWordKey(t *testing.T) {
	tests := []struct {
		word BlockedWord
		want string
	}{
		{BlockedWord{BlockedKeyword, "Ａ W S L"}, "awsl"},
		{BlockedWord{BlockedRegex, "Ａ W S L"}, "Ａ W S L"},
	}
	for _, tt := range tests {
		if got := tt.word.Key(); got != tt.want {
			t.Errorf("%+v.Key() = %q, want %q", tt.word, got, tt.want)
		}
	}
}

// TestMergeBlocklist 正文命中屏蔽词的弹幕不输出：每条正文各放进一个绑定，看合并后剩下哪些。
func TestMergeBlocklist(t *testing.T) {
	tests := []struct {
		name  string
		words []BlockedWord
		texts []string
		want  []string
	}{
		{"没有屏蔽词", nil, []string{"前排", "草"}, []string{"前排", "草"}},
		{
			"关键词按归一化后的正文包含即命中",
			[]BlockedWord{{BlockedKeyword, "Awsl"}},
			[]string{"我AWSL了", "Ａ W S L", "aws l!", "aws"},
			[]string{"aws"},
		},
		{
			"正则对原始正文匹配，区分大小写",
			[]BlockedWord{{BlockedRegex, `^前排`}, {BlockedRegex, "awsl"}, {BlockedRegex, "前 排"}},
			[]string{"前排", "我前排", "AWSL", "awsl", "前排了", "前 排"},
			[]string{"我前排", "AWSL"},
		},
		{
			"(?i) 只作用于它所在的那条正则",
			[]BlockedWord{{BlockedRegex, "(?i)abc"}, {BlockedRegex, "xyz"}},
			[]string{"ABC", "XYZ", "xyz"},
			[]string{"XYZ"},
		},
		{
			"关键词与正则同时生效",
			[]BlockedWord{{BlockedKeyword, "剧透"}, {BlockedRegex, `^\d+$`}},
			[]string{"前方剧透", "233", "2333啊", "好看"},
			[]string{"2333啊", "好看"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocklist, err := NewBlocklist(tt.words)
			if err != nil {
				t.Fatal(err)
			}
			tracks := make([]Track, len(tt.texts))
			for i, text := range tt.texts {
				// 各条相隔 10 秒，不会被按文本去重
				tracks[i] = track(int64(i+1), PlatformBilibili, 0, dm(int64(i+1), int32(i)*10000, text))
			}
			var got []string
			for _, it := range Merge(tracks, blocklist) {
				got = append(got, it.Text)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Merge() 剩下 %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMergeBlocksBeforeDedup 被屏蔽的弹幕不参与跨源去重：另一个绑定里归一化后相同、只差在空白上的那条照常输出。
func TestMergeBlocksBeforeDedup(t *testing.T) {
	blocklist, err := NewBlocklist([]BlockedWord{{BlockedRegex, "^草 $"}})
	if err != nil {
		t.Fatal(err)
	}
	got := Merge([]Track{
		track(1, PlatformBilibili, 0, dm(1, 10000, "草 ")),
		track(2, PlatformBilibili, 0, dm(2, 11000, "草")),
	}, blocklist)
	want := []Item{out(PlatformBilibili, dm(2, 11000, "草"), 11000)}
	if !slices.Equal(got, want) {
		t.Errorf("Merge() = %+v\nwant %+v", got, want)
	}
}
