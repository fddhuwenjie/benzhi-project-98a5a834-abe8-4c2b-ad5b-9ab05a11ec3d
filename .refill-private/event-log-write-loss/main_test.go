package event_log_write_loss_test

import (
	"heritage-care/internal/domain"
	"heritage-care/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentCommitRejectsEventLogFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "events.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := storage.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.AddArtifact(domain.ArtifactRecord{ArtifactID: "artifact-log", Title: "日志测试", Material: "paper"})
	if err == nil {
		t.Errorf("events.log不可追加时提交仍报告成功")
	}
	reloaded, reloadErr := storage.New(dir)
	if reloadErr != nil {
		t.Fatal(reloadErr)
	}
	if _, exists := reloaded.Artifact("artifact-log"); exists {
		t.Errorf("失败提交已经泄漏到重启后的snapshot.json")
	}
}
