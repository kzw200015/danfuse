package service_test

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
