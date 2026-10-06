package source

import (
	"context"
	"errors"
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

// ParseCollectionLink 依次交给各个适配器，返回第一个认识这个链接的适配器和它给出的候选。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseCollectionLink(ctx context.Context, link string) (Adapter, []CollectionCandidate, error) {
	for _, a := range r.adapters {
		candidates, err := a.ParseCollectionLink(ctx, link)
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
