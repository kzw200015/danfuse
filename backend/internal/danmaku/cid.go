package danmaku

import (
	"crypto/sha256"
	"encoding/binary"
)

// CID 弹弹play comment 的 cid：对"平台 ‖ 0x00 ‖ 原始 ID 的 8 字节大端"取 SHA-256，取前 8 字节、
// 保留低 53 位（cid 在 JavaScript 里按 number 读），结果为 0 时取 1。原始 ID 最大约 2.2e18，不能直接当作 cid。
// 输出时现算、不存储：同一条弹幕不论来自哪个绑定、哪种存储模式，cid 都相同。
// 算法一经发布不能再改；一集 10 万条弹幕时撞号的概率约为百万分之 0.5，撞了不处理。
func CID(p Platform, sourceID int64) int64 {
	buf := make([]byte, 0, len(p)+1+8)
	buf = append(buf, p...)
	buf = append(buf, 0)
	buf = binary.BigEndian.AppendUint64(buf, uint64(sourceID))
	sum := sha256.Sum256(buf)
	if cid := int64(binary.BigEndian.Uint64(sum[:8]) & (1<<53 - 1)); cid != 0 {
		return cid
	}
	return 1
}
