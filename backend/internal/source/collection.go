package source

import (
	"context"
	"errors"
)

// CollectionRef 合集在平台内的引用，是适配器自己定义的 JSON，存入 season_bindings.ref（jsonb），
// 参与唯一约束 (season_id, adapter, ref)。与弹幕源的 Ref 分开，编译期不会混用；适配器之外的代码不解析它。
type CollectionRef []byte

// Collector 可选能力：合集，即平台上按顺序排列的一组弹幕源（例如 B 站番剧的一季、投稿合集、多 P 投稿）。
// 和 Linker 一样用类型断言判断，没有合集的适配器（例如本地弹幕文件）不实现它。
type Collector interface {
	// ParseCollectionLink 识别季面板贴的链接，返回一个或多个候选合集，按适配器认为合适的顺序排列。
	// 同一个合集不论链接怎么写，候选的 ref 都相同。不是本平台的链接返回 ErrUnrecognized；
	// 是本平台的但不能作为合集绑定的，返回 Kind 为 InvalidLink 的 *Error，提示由适配器写。可能联网。
	ParseCollectionLink(ctx context.Context, link string) ([]Candidate, error)
	// ListCollection 按合集 ref 列出合集的标题、是否完结（取不到时为否）和全部条目，条目按合集里的顺序排列。
	// 合集不存在时返回 Kind 为 NotFound 的 *Error。重复序号不在这里判定，见 MarkDuplicateNumbers。
	ListCollection(ctx context.Context, ref CollectionRef) (Collection, error)
	// DescribeCollection 由合集 ref 生成展示用的链接和标签，纯计算，不联网。
	DescribeCollection(ref CollectionRef) (Display, error)
}

// Candidate 链接识别出的一个候选合集。
type Candidate struct {
	Kind string // 适配器自己定义的种类标识，例如 B 站的番剧、投稿合集、多 P 投稿；创建季绑定时用它选候选
	Ref  CollectionRef
}

// Collection 列出的合集。
type Collection struct {
	Title    string
	Finished bool // 平台上已完结；取不到时为否
	Items    []CollectionItem
}

// CollectionItem 合集里的一个条目，即一个弹幕源。
type CollectionItem struct {
	Ref       Ref    // 弹幕源 ref，与单集绑定同一套格式，手动绑过的同一个弹幕源能被认出来
	Number    int    // 合集序号，Unmatched 为空时才有意义
	Unmatched string // 非空表示对不上，内容是原因，例如"集号「SP」不是整数"
	Label     string // 展示标签
	Note      string // 可选的提示，例如"共 5 个分 P，只用 P1"
}

// ParseCollectionLink 依次交给实现了 Collector 的适配器，返回第一个认识这个链接的适配器和它给出的候选。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseCollectionLink(ctx context.Context, link string) (Adapter, []Candidate, error) {
	for _, a := range r.adapters {
		collector, ok := a.(Collector)
		if !ok {
			continue
		}
		candidates, err := collector.ParseCollectionLink(ctx, link)
		switch {
		case errors.Is(err, ErrUnrecognized):
			continue
		case err != nil:
			return nil, nil, err
		}
		return a, candidates, nil
	}
	return nil, nil, &Error{Kind: InvalidLink, Message: "无法识别的链接"}
}
