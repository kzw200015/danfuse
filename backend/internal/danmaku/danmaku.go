// Package danmaku 弹幕的内部格式（源适配器拉取的产出，也是绑定落库的内容），
// 以及读取一集弹幕时与弹幕是否落库无关的处理：屏蔽、校正、跨源去重、cid。
// 纯计算，不访问数据库和网络，不依赖任何业务包。
package danmaku

// Platform 弹幕所在的平台；空串表示没有平台（用弹幕文件建的绑定）。
// 取值一经发布不能再改：它参与 cid 计算与跨源去重。
type Platform string

const (
	PlatformNone     Platform = ""
	PlatformBilibili Platform = "bilibili"
)

// Mode 弹幕模式，沿用 B 站的编号。平台的其他模式由适配器合并或丢弃，不会出现在这里。
type Mode uint8

const (
	ModeScroll  Mode = 1 // 滚动
	ModeBottom  Mode = 4 // 底部
	ModeTop     Mode = 5 // 顶部
	ModeReverse Mode = 6 // 逆向滚动
)

// Danmaku 一条弹幕的内部格式。
type Danmaku struct {
	TimeMs   int32 // 相对弹幕源视频开头的毫秒数
	Mode     Mode
	Color    uint32 // RGB888，0～0xFFFFFF
	Text     string
	SourceID int64 // 平台原始弹幕 ID，非 0；在同一平台（或所有无平台的弹幕源）内唯一
}

// Item 一集合并后输出的一条弹幕：TimeMs 已校正到本地视频的时间轴，带上所在的平台。
type Item struct {
	Danmaku
	Platform Platform
}
