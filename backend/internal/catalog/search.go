package catalog

import (
	"strconv"

	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

// SearchVector 一季的搜索列（tsvector 文本）：A 档为剧名和季号，B 档为原名，C 档为目录源给的季标题。
// 季号只以数字出现（剧集的第 1 季起，特别篇和电影没有），给"剧名2"这种没有标注的写法用：光看数字分不出是标题的一部分
// 还是季号，交给全文搜索，数字在标题里、季号里都能命中。写明了的季号（"第2季"、"S02"、"特别篇"）由 naming.Parse 拆出，
// 按季号精确过滤，不经过搜索列。季号放在最后，不挪动前面各词的位置：同一部剧的各季按剧名、原名搜索时得分相同，
// 先后由季号决定（ts_rank 会看命中的词之间的距离）。
// 同步写入季时用它计算；改动它的组成时，要新增一个 goose Go 迁移，重算所有季的搜索列。
func SearchVector(t naming.SeriesType, seriesTitle, originalTitle string, number int, seasonTitle string) string {
	seasonNumber := ""
	if t == naming.TypeTV && number > 0 {
		seasonNumber = strconv.Itoa(number)
	}
	return fulltext.Vector(
		fulltext.Field{Weight: fulltext.WeightA, Text: seriesTitle},
		fulltext.Field{Weight: fulltext.WeightB, Text: originalTitle},
		fulltext.Field{Weight: fulltext.WeightC, Text: seasonTitle},
		fulltext.Field{Weight: fulltext.WeightA, Text: seasonNumber},
	)
}
