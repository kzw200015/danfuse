package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var errNotFolder = apierr.ErrBadRequest.WithMessage("这不是文件夹的季绑定")

// 文件夹的季绑定：条目和条目的文件存在库里，四种写入（创建、保存表单、换文件夹、立即补建）都在一个事务里当场完成：
// 锁季、锁季绑定（FOR UPDATE，与另一次写入排队，不拿租约），写表单，派生，删掉要删的槽位，再按条目建出文件绑定。
// 有上传的文件时，先在事务之外分成条目、逐份解析，有一份失败就整次 422，什么都不保存。

// FolderForm 文件夹的表单里条目以外的部分：集号对应、集号规则。
type FolderForm struct {
	Mapping source.Mapping
	Rule    source.EpisodeRule
}

// PickedFolder 预览时选的文件夹：文件夹名，与各条目的名称和文件哈希（代替文件本身）。
type PickedFolder struct {
	Folder  string
	Entries []EntryHashes
}

// EntryHashes 文件夹里的一个条目：名称（不带文件夹名）与各份文件内容的 SHA-256。
type EntryHashes struct {
	Label   string
	SHA256s [][sha256.Size]byte
}

// CreateFolder 用上传的文件夹创建文件夹的季绑定：标题为文件夹名，保存全部条目（对不上的也保存）和条目的文件，
// 对到集的条目当场建出文件绑定，返回详情。files 的 Name 是以文件夹名开头的相对路径。
// 季不存在为 404，保存前被删除为 404"这一季已被删除"；文件夹的结构不对为 400，有一份文件解析失败为 422。
func (s *Service) CreateFolder(ctx context.Context, seasonID int64, files []bindingsvc.UploadedFile, f FolderForm) (Detail, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Detail{}, err
	}
	upload, err := readFolder(files)
	if err != nil {
		return Detail{}, err
	}
	var (
		id  int64
		res folderResult
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockSeason(ctx, q, seasonID, errSeasonDeleted); err != nil {
			return err
		}
		sb, err := q.InsertFolderSeasonBinding(ctx, dao.InsertFolderSeasonBindingParams{
			SeasonID:        seasonID,
			Title:           upload.name,
			MappingFrom:     int32(f.Mapping.From),
			MappingTo:       int32(f.Mapping.To),
			EpisodePatterns: f.Rule.Patterns(),
		})
		if err != nil {
			return fmt.Errorf("insert folder season binding of season %d: %w", seasonID, err)
		}
		id = sb.ID
		res, err = s.applyFolder(ctx, tx, sb, &upload, true)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	s.logFolder(ctx, "folder season binding created", id, res)
	return s.Get(ctx, id)
}

// SaveFolder 保存文件夹的季绑定的表单，当场完成，返回详情：files 为空时不换文件夹，只改集号规则、集号对应；
// 不为空时换文件夹，标题改为新的文件夹名，名称和文件哈希都没变的条目沿用（建出的绑定和偏移不动），其余的删掉重建。
// 派生之后删掉没有条目的槽位，替换、新建槽位。每次保存都走这一套，不比较表单改没改。
// 不存在时 404（保存前季或季绑定被删除也是），合集的季绑定 400；文件夹的结构不对为 400，有一份文件解析失败为 422。
func (s *Service) SaveFolder(ctx context.Context, id int64, files []bindingsvc.UploadedFile, f FolderForm) (Detail, error) {
	sb, err := s.getFolder(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	var upload *uploadedFolder
	if len(files) > 0 {
		u, err := readFolder(files)
		if err != nil {
			return Detail{}, err
		}
		upload = &u
	}
	var res folderResult
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := lockSeasonBinding(ctx, q, sb.SeasonID, id)
		if err != nil {
			return err
		}
		title := locked.Title
		if upload != nil {
			title = upload.name
		}
		saved, err := q.UpdateFolderSeasonBinding(ctx, dao.UpdateFolderSeasonBindingParams{
			ID:              id,
			Title:           title,
			MappingFrom:     int32(f.Mapping.From),
			MappingTo:       int32(f.Mapping.To),
			EpisodePatterns: f.Rule.Patterns(),
		})
		if err != nil {
			return fmt.Errorf("update folder season binding %d: %w", id, err)
		}
		res, err = s.applyFolder(ctx, tx, saved, upload, true)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	s.logFolder(ctx, "folder season binding saved", id, res)
	return s.Get(ctx, id)
}

// backfillFolder 文件夹的立即补建：在一个事务里按保存的表单派生，替换、新建槽位，不删除没有条目的槽位（补建从不删除，见 docs/architecture/season-binding.md 的"写入"）。
func (s *Service) backfillFolder(ctx context.Context, sb dao.SeasonBinding) error {
	var res folderResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		locked, err := lockSeasonBinding(ctx, s.q.WithTx(tx), sb.SeasonID, sb.ID)
		if err != nil {
			return err
		}
		res, err = s.applyFolder(ctx, tx, locked, nil, false)
		return err
	})
	if err != nil {
		return err
	}
	s.logFolder(ctx, "folder season binding backfilled", sb.ID, res)
	return nil
}

// PreviewNewFolder 预览新建文件夹的季绑定：标题为这次的文件夹名，条目都是新的。不保存任何东西；季不存在为 404。
func (s *Service) PreviewNewFolder(ctx context.Context, seasonID int64, p PickedFolder, f FolderForm) (Preview, error) {
	if err := s.checkSeason(ctx, seasonID); err != nil {
		return Preview{}, err
	}
	entries := make([]formEntry, len(p.Entries))
	for i, e := range p.Entries {
		entries[i] = formEntry{label: e.Label}
	}
	episodes, err := episodeSlots(ctx, s.q, seasonID, nil, "")
	if err != nil {
		return Preview{}, err
	}
	return folderPreview(p.Folder, Derive(folderEntries(p.Folder, entries, f.Rule), f.Mapping, episodes), false), nil
}

// PreviewFolderEdit 预览保存文件夹的季绑定：p 为 nil 时不换文件夹，按保存的条目和标题算；否则按新的文件夹名，
// 新条目按名称和文件哈希对上保存的条目。removed 是保存后删除的槽位。不保存任何东西；不存在时 404，合集的季绑定 400。
func (s *Service) PreviewFolderEdit(ctx context.Context, id int64, p *PickedFolder, f FolderForm) (Preview, error) {
	sb, err := s.getFolder(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	title := sb.Title
	entries, err := savedEntries(ctx, s.q, id)
	if err != nil {
		return Preview{}, err
	}
	if p != nil {
		title = p.Folder
		if entries, err = matchEntries(ctx, s.q, id, entries, p.Entries); err != nil {
			return Preview{}, err
		}
	}
	episodes, err := episodeSlots(ctx, s.q, sb.SeasonID, &id, "")
	if err != nil {
		return Preview{}, err
	}
	return folderPreview(title, Derive(folderEntries(title, entries, f.Rule), f.Mapping, episodes), true), nil
}

// folderPreview 文件夹的预览只有一组，没有合集的字段；文件夹的条目都按集号规则编号。current 为编辑时，标出这一组是保存的。
func folderPreview(title string, r Result, current bool) Preview {
	c := newCandidate(r)
	c.Title, c.NumberedByRule, c.Current = title, true, current
	return Preview{Candidates: []Candidate{c}}
}

// getFolder 读文件夹的季绑定：不存在时 404，合集的季绑定 400（种类不符）。
func (s *Service) getFolder(ctx context.Context, id int64) (dao.SeasonBinding, error) {
	return s.getKind(ctx, id, KindFolder, errNotFolder)
}

// folderResult 一次写入建出、替换、删除的槽位数，记进日志。
type folderResult struct {
	created, replaced, removed int
}

func (s *Service) logFolder(ctx context.Context, msg string, id int64, r folderResult) {
	s.logger.LogAttrs(ctx, slog.LevelInfo, msg, slog.Int64("season_binding_id", id),
		slog.Int("created", r.created), slog.Int("replaced", r.replaced), slog.Int("removed", r.removed))
}

// applyFolder 在写入事务里按季绑定 sb 的表单（已写好、已锁住）落实：
//  1. 条目：upload 不为 nil（创建、换文件夹）时为这次上传的，按名称和哈希对上保存的条目；否则为保存的条目。
//  2. 派生。
//  3. 先删槽位（DeleteSlotsInTx 先锁集）：被替换的，以及 deleteEmpty 时没有条目的。没对上的条目建出的绑定都在这两类里，
//     所以随后删掉这些条目时没有绑定跟着级联删除。
//  4. 写条目：删掉没对上的，沿用的改位置，新的连同文件插入。
//  5. 替换、新建：从条目的文件解析，用 CreateFileSlotInTx 写入；集已被删除时跳过。
func (s *Service) applyFolder(ctx context.Context, tx pgx.Tx, sb dao.SeasonBinding, upload *uploadedFolder, deleteEmpty bool) (folderResult, error) {
	q := s.q.WithTx(tx)
	rule, err := formRule(sb)
	if err != nil {
		return folderResult{}, err
	}
	saved, err := savedEntries(ctx, q, sb.ID)
	if err != nil {
		return folderResult{}, err
	}
	entries := saved
	if upload != nil {
		if entries, err = matchEntries(ctx, q, sb.ID, saved, upload.hashes()); err != nil {
			return folderResult{}, err
		}
		for i := range entries {
			entries[i].upload = &upload.entries[i]
		}
	}
	episodes, err := episodeSlots(ctx, q, sb.SeasonID, &sb.ID, "")
	if err != nil {
		return folderResult{}, err
	}
	r := Derive(folderEntries(sb.Title, entries, rule), formMapping(sb), episodes)

	var (
		res   folderResult
		slots []int64 // 要删的槽位所在的集
	)
	for _, it := range r.Items {
		if it.Status == StatusReplace {
			slots = append(slots, it.EpisodeID)
		}
	}
	if deleteEmpty {
		slots = append(slots, r.emptyEpisodeIDs()...)
		res.removed = len(r.Empty)
	}
	if len(slots) > 0 {
		if err := s.bindings.DeleteSlotsInTx(ctx, tx, sb.ID, slots); err != nil {
			return folderResult{}, err
		}
	}
	if upload != nil {
		if err := writeEntries(ctx, q, sb.ID, saved, entries); err != nil {
			return folderResult{}, err
		}
	}

	stored, err := storedEntryFiles(ctx, q, entries, r)
	if err != nil {
		return folderResult{}, err
	}
	for i, it := range r.Items {
		if it.Status != StatusCreate && it.Status != StatusReplace {
			continue
		}
		items, err := entries[i].danmaku(stored[entries[i].id])
		if err != nil {
			return folderResult{}, err
		}
		err = s.bindings.CreateFileSlotInTx(ctx, tx, bindingsvc.FileSlot{
			SeasonBindingID: sb.ID, EpisodeID: it.EpisodeID, EntryID: entries[i].id, Title: entries[i].label, Danmaku: items,
		})
		switch {
		case errors.Is(err, bindingsvc.ErrEpisodeGone):
			continue
		case err != nil:
			return folderResult{}, err
		case it.Status == StatusReplace:
			res.replaced++
		default:
			res.created++
		}
	}
	return res, nil
}

// formEntry 表单里的一个条目。保存的条目有 ID；换文件夹时没对上保存的条目的 ID 为 0，写条目时才插入。
type formEntry struct {
	id     int64
	label  string         // 条目名称，不带文件夹名
	upload *uploadedEntry // 这次上传的条目；不换文件夹时为 nil
}

// source 条目作为弹幕源的身份（Entry.Source）：保存的条目为 entrySource(条目 ID)；换文件夹时没对上保存的条目的，
// 为临时身份 "new:<名称>"，不会与任何槽位相同。
func (e formEntry) source() string {
	if e.id == 0 {
		return "new:" + e.label
	}
	return entrySource(e.id)
}

// danmaku 条目的弹幕：各份文件经 binding.ParseFile 解析、合起来（重复的由写入时按原始 ID 去重）。
// 这次上传的已经解析过；保存的解析 stored（storedEntryFiles 读出的这个条目的文件），解析不了时 422。
func (e formEntry) danmaku(stored []dao.ListSeasonBindingEntryFilesRow) ([]danmaku.Danmaku, error) {
	var items []danmaku.Danmaku
	if e.upload != nil {
		for _, f := range e.upload.files {
			items = append(items, f.Danmaku...)
		}
		return items, nil
	}
	for _, f := range stored {
		parsed, err := bindingsvc.ParseFile(f.Name, f.Content)
		if err != nil {
			return nil, err
		}
		items = append(items, parsed...)
	}
	return items, nil
}

// storedEntryFiles 要新建、替换的槽位里，条目不是这次上传的（保存的条目），一条查询读出它们的文件，按条目 ID 分组、按加入的顺序。
func storedEntryFiles(ctx context.Context, q *dao.Queries, entries []formEntry, r Result) (map[int64][]dao.ListSeasonBindingEntryFilesRow, error) {
	var ids []int64
	for i, it := range r.Items {
		if (it.Status == StatusCreate || it.Status == StatusReplace) && entries[i].upload == nil {
			ids = append(ids, entries[i].id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.ListSeasonBindingEntryFiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list files of entries %v: %w", ids, err)
	}
	files := make(map[int64][]dao.ListSeasonBindingEntryFilesRow, len(ids))
	for _, f := range rows {
		files[f.EntryID] = append(files[f.EntryID], f)
	}
	return files, nil
}

// savedEntries 季绑定保存的条目，按位置排列。
func savedEntries(ctx context.Context, q *dao.Queries, id int64) ([]formEntry, error) {
	rows, err := q.ListSeasonBindingEntries(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list entries of season binding %d: %w", id, err)
	}
	entries := make([]formEntry, len(rows))
	for i, r := range rows {
		entries[i] = formEntry{id: r.ID, label: r.Label}
	}
	return entries, nil
}

// matchEntries 换文件夹时新的条目按"名称 + 文件哈希的集合"对上季绑定 id 保存的条目 saved（savedEntries 读出的）：
// 对上的沿用条目 ID，没对上的 ID 为 0。文件夹名不参与，文件夹改名后照样对得上。
func matchEntries(ctx context.Context, q *dao.Queries, id int64, saved []formEntry, picked []EntryHashes) ([]formEntry, error) {
	rows, err := q.ListSeasonBindingEntryHashes(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list file hashes of season binding %d: %w", id, err)
	}
	hashes := make(map[int64][]string, len(saved)) // 条目 ID → 文件哈希
	for _, r := range rows {
		hashes[r.EntryID] = append(hashes[r.EntryID], string(r.Sha256))
	}
	byKey := make(map[entryKey]int64, len(saved))
	for _, e := range saved {
		byKey[newEntryKey(e.label, hashes[e.id])] = e.id
	}
	entries := make([]formEntry, len(picked))
	for i, p := range picked {
		sums := make([]string, len(p.SHA256s))
		for j, sum := range p.SHA256s {
			sums[j] = string(sum[:])
		}
		entries[i] = formEntry{id: byKey[newEntryKey(p.Label, sums)], label: p.Label}
	}
	return entries, nil
}

// entryKey 条目的"名称 + 文件哈希的集合"。
type entryKey struct {
	label  string
	hashes string // 排序、去重后的文件哈希（原始字节，各 32 字节）首尾相接，与文件的顺序和重复无关
}

func newEntryKey(label string, hashes []string) entryKey {
	return entryKey{label: label, hashes: strings.Join(slices.Compact(slices.Sorted(slices.Values(hashes))), "")}
}

// writeEntries 换文件夹（创建）时写条目：删掉 saved 里没对上的（它们建出的绑定已经删掉），沿用的改为新的位置，
// 新的连同文件插入，ID 填回 entries；每一步一条语句写完。位置从 1 开始，按这次上传的顺序。
func writeEntries(ctx context.Context, q *dao.Queries, id int64, saved, entries []formEntry) error {
	kept := make(map[int64]bool, len(entries))
	for _, e := range entries {
		kept[e.id] = true
	}
	stale := []int64{}
	for _, e := range saved {
		if !kept[e.id] {
			stale = append(stale, e.id)
		}
	}
	if err := q.DeleteSeasonBindingEntries(ctx, stale); err != nil {
		return fmt.Errorf("delete entries of season binding %d: %w", id, err)
	}
	moved := dao.SetSeasonBindingEntryPositionsParams{Ids: []int64{}, Positions: []int32{}}
	added := dao.InsertSeasonBindingEntriesParams{SeasonBindingID: id, Labels: []string{}, Positions: []int32{}}
	for i, e := range entries {
		position := int32(i + 1)
		if e.id != 0 {
			moved.Ids = append(moved.Ids, e.id)
			moved.Positions = append(moved.Positions, position)
		} else {
			added.Labels = append(added.Labels, e.label)
			added.Positions = append(added.Positions, position)
		}
	}
	if err := q.SetSeasonBindingEntryPositions(ctx, moved); err != nil {
		return fmt.Errorf("set positions of entries of season binding %d: %w", id, err)
	}
	rows, err := q.InsertSeasonBindingEntries(ctx, added)
	if err != nil {
		return fmt.Errorf("insert entries of season binding %d: %w", id, err)
	}
	inserted := make(map[string]int64, len(rows)) // 条目名称 → ID
	for _, r := range rows {
		inserted[r.Label] = r.ID
	}
	files := dao.InsertSeasonBindingEntryFilesParams{}
	for i := range entries {
		e := &entries[i]
		if e.id != 0 {
			continue
		}
		e.id = inserted[e.label]
		for _, f := range e.upload.files {
			files.EntryIds = append(files.EntryIds, e.id)
			files.Names = append(files.Names, path.Base(f.Name))
			files.Sha256s = append(files.Sha256s, f.Sum[:])
			files.Sizes = append(files.Sizes, int32(len(f.Data)))
			files.Contents = append(files.Contents, f.Data)
		}
	}
	if err := q.InsertSeasonBindingEntryFiles(ctx, files); err != nil {
		return fmt.Errorf("insert entry files of season binding %d: %w", id, err)
	}
	return nil
}

// uploadedFolder 上传的文件夹：文件夹名，与按路径分好、逐份解析过的条目（按第一次出现的顺序）。
type uploadedFolder struct {
	name    string
	entries []uploadedEntry
}

// uploadedEntry 上传的文件夹里的一个条目，文件都已解析。
type uploadedEntry struct {
	label string
	files []bindingsvc.ParsedFile
}

// hashes 各条目的名称和文件哈希，按名称和哈希对上保存的条目用。
func (u uploadedFolder) hashes() []EntryHashes {
	out := make([]EntryHashes, len(u.entries))
	for i, e := range u.entries {
		out[i] = EntryHashes{Label: e.label, SHA256s: make([][sha256.Size]byte, len(e.files))}
		for j, f := range e.files {
			out[i].SHA256s[j] = f.Sum
		}
	}
	return out
}

// readFolder 把上传的文件按路径分成条目（结构不对时 400），再逐份解析（有一份失败就 422，提示带着它的相对路径）。
func readFolder(files []bindingsvc.UploadedFile) (uploadedFolder, error) {
	name, groups, err := groupFolder(files)
	if err != nil {
		return uploadedFolder{}, err
	}
	u := uploadedFolder{name: name, entries: make([]uploadedEntry, len(groups))}
	for i, g := range groups {
		parsed, err := bindingsvc.ParseFiles(g.files)
		if err != nil {
			return uploadedFolder{}, err
		}
		u.entries[i] = uploadedEntry{label: g.label, files: parsed}
	}
	return u, nil
}

// folderGroup groupFolder 分出的一个条目：一个子目录，或顶层的一份文件。
type folderGroup struct {
	label string // 子目录名，或去掉扩展名的文件名
	dir   bool   // 子目录合成的条目
	files []bindingsvc.UploadedFile
}

// groupFolder 按文件名（相对路径）把文件分成条目，前后端用同一套规则：每个路径是"文件夹/x.xml"或"文件夹/子目录/x.xml"
// （最多两层），只认 .xml（不分大小写），文件夹名都相同；子目录里的文件合成一个条目，名称为子目录名，
// 顶层的文件各自一个条目，名称为去掉扩展名的文件名；条目名称不能重复。不满足时 400。返回文件夹名和按第一次出现的顺序分好的条目。
func groupFolder(files []bindingsvc.UploadedFile) (string, []folderGroup, error) {
	var (
		name   string
		groups []folderGroup
	)
	index := make(map[string]int) // 条目名称 → 下标
	for i, f := range files {
		p := f.Name
		parts := strings.Split(p, "/")
		if len(parts) > 3 {
			return "", nil, apierr.ErrBadRequest.WithMessage("目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：" + p)
		}
		if len(parts) < 2 || slices.Contains(parts, "") {
			return "", nil, apierr.ErrBadRequest.WithMessage("文件路径不合法：" + p)
		}
		if !strings.HasSuffix(strings.ToLower(p), ".xml") {
			return "", nil, apierr.ErrBadRequest.WithMessage("只能上传 XML 弹幕文件：" + p)
		}
		if i == 0 {
			name = parts[0]
		}
		if parts[0] != name {
			return "", nil, apierr.ErrBadRequest.WithMessage("文件要在同一个文件夹里：" + p)
		}
		dir := len(parts) == 3
		label := parts[1]
		if !dir {
			// 与前端一样去掉最后一个点及其后面的部分；文件名只有扩展名时保留原样
			if dot := strings.LastIndexByte(label, '.'); dot > 0 {
				label = label[:dot]
			}
		}
		g, ok := index[label]
		switch {
		case !ok:
			g = len(groups)
			index[label] = g
			groups = append(groups, folderGroup{label: label, dir: dir})
		case !dir || !groups[g].dir:
			return "", nil, apierr.ErrBadRequest.WithMessage("条目名称重复：" + label)
		}
		groups[g].files = append(groups[g].files, f)
	}
	if len(groups) == 0 {
		return "", nil, apierr.ErrBadRequest.WithMessage("请选择要上传的文件")
	}
	return name, groups, nil
}
