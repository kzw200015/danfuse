package source

import (
	"fmt"
)

// CollectionRef 合集在平台内的引用，是适配器自己定义的 JSON，存入 season_bindings.ref（jsonb），
// 参与唯一约束 (season_id, adapter, ref)。合集是平台上按顺序排列的一组弹幕源（例如 B 站番剧的一季、投稿合集、多 P 投稿）。
// 与弹幕源的 Ref 分开，编译期不会混用；适配器之外的代码不解析它。
type CollectionRef []byte

// CollectionCandidate 链接识别出的一个候选合集。
type CollectionCandidate struct {
	Kind string // 适配器自己定义的种类标识，例如 B 站的番剧、投稿合集、多 P 投稿；创建季绑定时用它选候选
	Ref  CollectionRef
}

// Collection 列出的合集。
type Collection struct {
	Title    string
	Finished bool // 平台上已完结；取不到时为否
	// NumberedByRule 条目的序号由季绑定的集号规则从标签认出（见 NumberItems），适配器不填 Number、Unmatched；
	// 为否时序号由适配器给出（例如番剧的集号）
	NumberedByRule bool
	Items          []CollectionItem
}

// CollectionItem 合集里的一个条目，即一个弹幕源。
type CollectionItem struct {
	Ref        Ref      // 弹幕源 ref，与单集绑定同一套格式，手动绑过的同一个弹幕源能被认出来
	Number     int      // 合集序号，Unmatched 为空时才有意义
	Unmatched  string   // 非空表示对不上，内容是原因，例如"集号「SP」不是整数"
	Label      string   // 展示标签，也是集号规则认集号的名称；条目有两级标题时为"上级 / 下级"（LabelSeparator），越靠后越具体
	LabelParts []string // 原始标题层级，单层稿件或两层稿件/分 P；不从展示标签拆分
	ReasonCode string
	Trace      *EpisodeTrace
}

// NotInteger 认出的集号 s 不是整数时条目对不上的原因。
func NotInteger(s string) string {
	return fmt.Sprintf("集号「%s」不是整数", s)
}

// LabelSeparator 展示标签里两级标题的分隔，如"稿件标题 / 分 P 标题"。旧版默认规则认紧跟它、在结尾的数字。
const LabelSeparator = " / "
