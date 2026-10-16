package binding_test

import (
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestUpdateOffset(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testenv.Logger(t))
	id := createS1(t, svc)
	before := getBinding(t, pool, id)

	got, err := svc.UpdateOffset(t.Context(), id, -2.5)
	if err != nil {
		t.Fatalf("UpdateOffset: %v", err)
	}
	if got.Offset != -2.5 || got.DanmakuCount != 3 {
		t.Errorf("UpdateOffset() = %+v, want 偏移 -2.5、3 条弹幕", got)
	}
	// 改偏移不改 content_version
	if after := getBinding(t, pool, id); after.Offset != -2.5 || after.ContentVersion != before.ContentVersion {
		t.Errorf("偏移 %v、content_version %d，want -2.5、%d", after.Offset, after.ContentVersion, before.ContentVersion)
	}

	_, err = svc.UpdateOffset(t.Context(), 2, 1)
	testenv.AssertAppError(t, err, http.StatusNotFound, "绑定不存在")
}

// TestDeleteBinding 删除绑定，它的弹幕随之删除；同一个弹幕源在别的集上的绑定不受影响。
func TestDeleteBinding(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testenv.Logger(t))
	id := createS1(t, svc)
	other, err := svc.Create(t.Context(), 2, "fake/s1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(t.Context(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE id = $1`, id); n != 0 {
		t.Error("绑定还在")
	}
	if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM danmaku WHERE binding_id = $1`, id); n != 0 {
		t.Errorf("还留着它的 %d 条弹幕", n)
	}
	if ids := sourceIDs(t, pool, other.ID); len(ids) != 3 {
		t.Errorf("别的集上的绑定有 %d 条弹幕，want 3", len(ids))
	}

	testenv.AssertAppError(t, svc.Delete(t.Context(), id), http.StatusNotFound, "绑定不存在")
}
