package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"qingzhou/internal/store"
)

type fakeObjectStore struct {
	mu          sync.Mutex
	putStarted  chan struct{}
	putContinue chan struct{}
	putErr      error
	objects     map[string][]byte
	deleted     []string
}

func (f *fakeObjectStore) PutFile(_ context.Context, key, filename, _ string) (int64, error) {
	if f.putStarted != nil {
		select {
		case f.putStarted <- struct{}{}:
		default:
		}
	}
	if f.putContinue != nil {
		<-f.putContinue
	}
	if f.putErr != nil {
		return 0, f.putErr
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	f.objects[key] = data
	return int64(len(data)), nil
}

func (f *fakeObjectStore) HeadBucket(context.Context) error { return nil }

func (f *fakeObjectStore) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeObjectStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "https://example.test/download", nil
}

func newBackupTestManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "qingzhou.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		st.Close()
		t.Fatalf("migrate store: %v", err)
	}
	st.SetSecretKey([]byte("backup-test-key"))
	return New(st), st
}

func validConfig() Config {
	return Config{
		Endpoint:        "https://account.r2.cloudflarestorage.com",
		Region:          "auto",
		Bucket:          "backups",
		AccessKeyID:     "access-key",
		SecretAccessKey: "secret-key",
		Prefix:          "qingzhou",
	}
}

func saveValidConfig(t *testing.T, manager *Manager) {
	t.Helper()
	if _, err := manager.SaveConfig(validConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func waitForRecord(t *testing.T, manager *Manager, id string) Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, err := manager.List()
		if err != nil {
			t.Fatalf("list records: %v", err)
		}
		for _, record := range records {
			if record.ID == id && record.Status != "pending" {
				return record
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("backup %s did not finish", id)
	return Record{}
}

func TestConfigIsEncryptedAndLoadConfigIsRedacted(t *testing.T) {
	manager, st := newBackupTestManager(t)
	defer st.Close()

	saved, err := manager.SaveConfig(validConfig())
	if err != nil {
		t.Fatalf("save config: %v", err)
	}
	if saved.SecretAccessKey != "" {
		t.Fatal("SaveConfig returned the secret")
	}
	loaded, configured, err := manager.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !configured || loaded.SecretAccessKey != "" {
		t.Fatalf("loaded config = %+v, configured = %v", loaded, configured)
	}
	var raw string
	if err := st.DB().QueryRow(`SELECT value FROM settings WHERE key='backup_s3_config'`).Scan(&raw); err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	if raw == "" || raw == "secret-key" || !contains(raw, "enc:v1:") {
		t.Fatalf("secret was not encrypted in raw setting: %q", raw)
	}
}

func TestSaveScheduleValidatesCron(t *testing.T) {
	manager, st := newBackupTestManager(t)
	defer st.Close()
	if _, err := manager.SaveSchedule(context.Background(), Schedule{CronExpr: "not cron"}); err == nil {
		t.Fatal("invalid cron was accepted")
	}
	if _, err := manager.SaveSchedule(context.Background(), Schedule{CronExpr: "0 3 * * *", RetainDays: -1}); err == nil {
		t.Fatal("negative retention was accepted")
	}
}

func TestStartBackupUploadsSnapshotAndRecordsDigest(t *testing.T) {
	manager, st := newBackupTestManager(t)
	defer st.Close()
	saveValidConfig(t, manager)
	fake := &fakeObjectStore{}
	manager.factory = func(context.Context, Config) (objectStore, error) { return fake, nil }

	record, err := manager.StartBackup(context.Background(), "manual")
	if err != nil {
		t.Fatalf("start backup: %v", err)
	}
	finished := waitForRecord(t, manager, record.ID)
	if finished.Status != "completed" || finished.SizeBytes == 0 || len(finished.SHA256) != 64 {
		t.Fatalf("finished record = %+v", finished)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.objects) != 1 || fake.objects[finished.ObjectKey] == nil {
		t.Fatalf("uploaded objects = %v", fake.objects)
	}
}

func TestStartBackupRecordsUploadFailureAndRejectsConcurrentRun(t *testing.T) {
	manager, st := newBackupTestManager(t)
	defer st.Close()
	saveValidConfig(t, manager)
	fake := &fakeObjectStore{putStarted: make(chan struct{}, 1), putContinue: make(chan struct{}), putErr: errors.New("upload down")}
	manager.factory = func(context.Context, Config) (objectStore, error) { return fake, nil }

	first, err := manager.StartBackup(context.Background(), "manual")
	if err != nil {
		t.Fatalf("start first backup: %v", err)
	}
	<-fake.putStarted
	if _, err := manager.StartBackup(context.Background(), "manual"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("concurrent backup error = %v, want ErrInProgress", err)
	}
	close(fake.putContinue)
	finished := waitForRecord(t, manager, first.ID)
	if finished.Status != "failed" || finished.Error == "" {
		t.Fatalf("failed record = %+v", finished)
	}
}

func TestRetentionDeletesOldObjects(t *testing.T) {
	manager, st := newBackupTestManager(t)
	defer st.Close()
	saveValidConfig(t, manager)
	if _, err := manager.SaveSchedule(context.Background(), Schedule{CronExpr: defaultCron, RetainCount: 1}); err != nil {
		t.Fatalf("save schedule: %v", err)
	}
	fake := &fakeObjectStore{}
	manager.factory = func(context.Context, Config) (objectStore, error) { return fake, nil }
	for i := 0; i < 2; i++ {
		record, err := manager.StartBackup(context.Background(), "manual")
		if err != nil {
			t.Fatalf("start backup %d: %v", i, err)
		}
		waitForRecord(t, manager, record.ID)
		if i == 0 {
			time.Sleep(time.Second)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.deleted) != 1 {
		t.Fatalf("deleted keys = %v, want one", fake.deleted)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
