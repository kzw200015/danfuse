package source

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Registry 已注册的源适配器。
type Registry struct {
	adapters []Adapter
}

// NewRegistry 按给定顺序注册，ParseLink 也按这个顺序询问。
func NewRegistry(adapters ...Adapter) *Registry {
	return &Registry{adapters: adapters}
}

// Get 按 bindings.adapter 取适配器；没有注册这个 ID 时返回错误，调用方按服务器内部错误处理。
func (r *Registry) Get(id string) (Adapter, error) {
	i := slices.IndexFunc(r.adapters, func(a Adapter) bool { return a.ID() == id })
	if i < 0 {
		return nil, fmt.Errorf("source: unknown adapter %q", id)
	}
	return r.adapters[i], nil
}

// ParseLink 依次交给各个适配器，返回第一个认识这个链接的适配器和规范化的 ref。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseLink(ctx context.Context, link string) (Adapter, Ref, error) {
	return firstRecognized(r.adapters, func(a Adapter) (Ref, error) { return a.ParseLink(ctx, link) })
}

// ParseCollectionLink 依次交给各个适配器，返回第一个认识这个链接的适配器和它给出的候选。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseCollectionLink(ctx context.Context, link string) (Adapter, []CollectionCandidate, error) {
	return firstRecognized(r.adapters, func(a Adapter) ([]CollectionCandidate, error) { return a.ParseCollectionLink(ctx, link) })
}

// firstRecognized 依次用 parse 询问各个适配器，返回第一个认识链接的适配器和它的结果，规则见 Registry.ParseLink。
func firstRecognized[T any](adapters []Adapter, parse func(Adapter) (T, error)) (Adapter, T, error) {
	var zero T
	for _, a := range adapters {
		v, err := parse(a)
		switch {
		case errors.Is(err, ErrUnrecognized):
			continue
		case err != nil:
			return nil, zero, err
		}
		return a, v, nil
	}
	return nil, zero, &Error{Kind: InvalidLink, Message: "无法识别的链接"}
}
