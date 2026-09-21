package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"qingzhou/internal/store"
)

const (
	configSetting   = "backup_s3_config"
	scheduleSetting = "backup_schedule"
	recordsSetting  = "backup_records"
	defaultCron     = "0 3 * * *"
	maxRecords      = 100
)

var (
	ErrNotConfigured = errors.New("远端备份尚未配置")
	ErrInProgress    = errors.New("已有备份正在执行")
)

type Config struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Prefix          string `json:"prefix"`
	ForcePathStyle  bool   `json:"force_path_style"`
}

type Schedule struct {
	Enabled     bool   `json:"enabled"`
	CronExpr    string `json:"cron_expr"`
	RetainDays  int    `json:"retain_days"`
	RetainCount int    `json:"retain_count"`
}

type Record struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	FileName    string `json:"file_name"`
	ObjectKey   string `json:"object_key"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	TriggeredBy string `json:"triggered_by"`
	Error       string `json:"error,omitempty"`
	StartedAt   int64  `json:"started_at"`
	FinishedAt  int64  `json:"finished_at,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
	Format      string `json:"format,omitempty"`
}

type Manager struct {
	st      *store.Store
	factory objectStoreFactory

	mu        sync.Mutex
	running   bool
	rootCtx   context.Context
	scheduler *cron.Cron
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

func New(st *store.Store) *Manager { return &Manager{st: st, factory: newS3Store} }

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.rootCtx = ctx
	m.stopOnce = sync.Once{}
	m.mu.Unlock()
	m.applySchedule(ctx)
	go func() {
		<-ctx.Done()
		m.Stop()
	}()
}

func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		scheduler := m.scheduler
		m.scheduler = nil
		m.mu.Unlock()
		if scheduler != nil {
			scheduler.Stop()
		}
		m.wg.Wait()
	})
}

func (c Config) Validate() error {
	endpoint := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("Endpoint 必须是完整的 http:// 或 https:// 地址")
	}
	if strings.HasSuffix(strings.ToLower(parsed.Host), ".r2.cloudflarestorage.com") && strings.Trim(parsed.Path, "/") != "" {
		return fmt.Errorf("Cloudflare R2 Endpoint 只填写账户地址，Bucket 请单独填写")
	}
	if strings.TrimSpace(c.Bucket) == "" {
		return fmt.Errorf("Bucket 不能为空")
	}
	if strings.TrimSpace(c.AccessKeyID) == "" || strings.TrimSpace(c.SecretAccessKey) == "" {
		return fmt.Errorf("Access Key ID 和 Secret Access Key 不能为空")
	}
	return nil
}

func (c Config) configured() bool {
	return strings.TrimSpace(c.Endpoint) != "" && strings.TrimSpace(c.Bucket) != "" &&
		strings.TrimSpace(c.AccessKeyID) != "" && strings.TrimSpace(c.SecretAccessKey) != ""
}

func (m *Manager) LoadConfig() (Config, bool, error) {
	raw, err := m.st.GetSetting(configSetting)
	if err != nil {
		return Config{}, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return Config{}, false, nil
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, false, fmt.Errorf("远端备份配置损坏: %w", err)
	}
	configured := cfg.configured()
	cfg.SecretAccessKey = ""
	return cfg, configured, nil
}

func (m *Manager) loadFullConfig() (Config, error) {
	raw, err := m.st.GetSetting(configSetting)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(raw) == "" {
		return Config{}, ErrNotConfigured
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("远端备份配置损坏: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (m *Manager) SaveConfig(cfg Config) (Config, error) {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.Prefix = strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	if cfg.SecretAccessKey == "" {
		raw, err := m.st.GetSetting(configSetting)
		if err != nil {
			return Config{}, err
		}
		if raw != "" {
			var old Config
			if err := json.Unmarshal([]byte(raw), &old); err != nil {
				return Config{}, fmt.Errorf("读取已有备份配置失败: %w", err)
			}
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return Config{}, err
	}
	if err := m.st.SetSetting(configSetting, string(b)); err != nil {
		return Config{}, err
	}
	cfg.SecretAccessKey = ""
	return cfg, nil
}

func (m *Manager) TestConnection(ctx context.Context, cfg Config) error {
	if cfg.SecretAccessKey == "" {
		full, err := m.loadFullConfig()
		if err != nil {
			return err
		}
		cfg.SecretAccessKey = full.SecretAccessKey
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	objectStore, err := m.factory(ctx, cfg)
	if err != nil {
		return err
	}
	return objectStore.HeadBucket(ctx)
}

func (m *Manager) LoadSchedule() (Schedule, error) {
	raw, err := m.st.GetSetting(scheduleSetting)
	if err != nil {
		return Schedule{}, err
	}
	cfg := Schedule{CronExpr: defaultCron, RetainDays: 14, RetainCount: 10}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return Schedule{}, fmt.Errorf("定时备份配置损坏: %w", err)
		}
	}
	if cfg.CronExpr == "" {
		cfg.CronExpr = defaultCron
	}
	return cfg, nil
}

func (m *Manager) SaveSchedule(ctx context.Context, cfg Schedule) (Schedule, error) {
	if cfg.CronExpr == "" {
		cfg.CronExpr = defaultCron
	}
	if cfg.RetainDays < 0 || cfg.RetainCount < 0 {
		return Schedule{}, fmt.Errorf("保留天数和保留份数不能为负数")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(cfg.CronExpr); err != nil {
		return Schedule{}, fmt.Errorf("cron 表达式无效: %w", err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return Schedule{}, err
	}
	if err := m.st.SetSetting(scheduleSetting, string(b)); err != nil {
		return Schedule{}, err
	}
	m.applySchedule(m.rootContext(ctx))
	return cfg, nil
}

func (m *Manager) rootContext(fallback context.Context) context.Context {
	m.mu.Lock()
	ctx := m.rootCtx
	m.mu.Unlock()
	if ctx == nil {
		return fallback
	}
	return ctx
}

func (m *Manager) applySchedule(ctx context.Context) {
	cfg, err := m.LoadSchedule()
	if err != nil {
		log.Printf("remote backup schedule: %v", err)
		return
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(cfg.CronExpr); err != nil {
		log.Printf("remote backup schedule: %v", err)
		return
	}
	var next *cron.Cron
	if cfg.Enabled {
		next = cron.New()
		if _, err := next.AddFunc(cfg.CronExpr, func() {
			if _, err := m.StartBackup(ctx, "scheduled"); err != nil && !errors.Is(err, ErrInProgress) {
				log.Printf("remote backup scheduled run: %v", err)
			}
		}); err != nil {
			log.Printf("remote backup schedule: %v", err)
			return
		}
		next.Start()
	}
	m.mu.Lock()
	old := m.scheduler
	m.scheduler = next
	m.mu.Unlock()
	if old != nil {
		old.Stop()
	}
}

func (m *Manager) List() ([]Record, error) {
	records, err := m.loadRecords()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt > records[j].StartedAt })
	return records, nil
}

func (m *Manager) loadRecords() ([]Record, error) {
	raw, err := m.st.GetSetting(recordsSetting)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return []Record{}, nil
	}
	var records []Record
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, fmt.Errorf("备份记录损坏: %w", err)
	}
	return records, nil
}

func (m *Manager) saveRecords(records []Record) error {
	if len(records) > maxRecords {
		sort.Slice(records, func(i, j int) bool { return records[i].StartedAt > records[j].StartedAt })
		records = records[:maxRecords]
	}
	b, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return m.st.SetSetting(recordsSetting, string(b))
}

func (m *Manager) updateRecord(record Record) error {
	records, err := m.loadRecords()
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].ID == record.ID {
			records[i] = record
			return m.saveRecords(records)
		}
	}
	return fmt.Errorf("备份记录不存在: %s", record.ID)
}

func (m *Manager) StartBackup(ctx context.Context, triggeredBy string) (Record, error) {
	cfg, err := m.loadFullConfig()
	if err != nil {
		return Record{}, err
	}
	spec, err := loadRecoverySpec()
	if err != nil {
		return Record{}, err
	}
	if spec != nil && !strings.HasPrefix(cfg.Endpoint, "https://") {
		return Record{}, fmt.Errorf("完整灾备包含敏感配置，必须使用 HTTPS 私有对象存储")
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return Record{}, ErrInProgress
	}
	m.running = true
	root := m.rootCtx
	m.mu.Unlock()
	if root == nil {
		root = ctx
	}
	now := time.Now().UTC()
	record := Record{
		ID:          uuid.NewString(),
		Status:      "pending",
		FileName:    "qingzhou-" + now.Format("20060102-150405") + ".db",
		TriggeredBy: triggeredBy,
		StartedAt:   now.Unix(),
	}
	record.Format = "sqlite"
	if spec != nil {
		record.Format = "tar.gz"
		record.FileName = "qingzhou-dr-" + now.Format("20060102-150405") + "-" + record.ID + ".tar.gz"
	}
	record.ObjectKey = joinKey(cfg.Prefix, record.FileName)
	records, err := m.loadRecords()
	if err != nil {
		m.setRunning(false)
		return Record{}, err
	}
	records = append(records, record)
	if err := m.saveRecords(records); err != nil {
		m.setRunning(false)
		return Record{}, err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.setRunning(false)
		m.runBackup(root, cfg, record, spec)
	}()
	return record, nil
}

func (m *Manager) setRunning(value bool) { m.mu.Lock(); m.running = value; m.mu.Unlock() }

func (m *Manager) runBackup(root context.Context, cfg Config, record Record, spec *RecoverySpec) {
	ctx, cancel := context.WithTimeout(root, 30*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp(filepath.Dir(m.st.Path()), ".qingzhou-remote-backup-")
	if err != nil {
		m.failRecord(record, err)
		return
	}
	defer os.RemoveAll(dir)
	snapshot := filepath.Join(dir, "snapshot.db")
	if err := m.st.BackupTo(snapshot); err != nil {
		m.failRecord(record, err)
		return
	}
	uploadPath, contentType := snapshot, "application/vnd.sqlite3"
	if spec != nil {
		uploadPath = filepath.Join(dir, record.FileName)
		contentType = "application/gzip"
		if err := m.createRecoveryArchive(ctx, spec, snapshot, uploadPath); err != nil {
			m.failRecord(record, err)
			return
		}
	}
	sha, size, err := fileDigest(uploadPath)
	if err != nil {
		m.failRecord(record, err)
		return
	}
	objectStore, err := m.factory(ctx, cfg)
	if err != nil {
		m.failRecord(record, err)
		return
	}
	if _, err := objectStore.PutFile(ctx, record.ObjectKey, uploadPath, contentType); err != nil {
		m.failRecord(record, err)
		return
	}
	record.Status = "completed"
	record.SizeBytes = size
	record.SHA256 = sha
	record.FinishedAt = time.Now().Unix()
	if schedule, err := m.LoadSchedule(); err == nil && schedule.RetainDays > 0 {
		record.ExpiresAt = time.Now().Add(time.Duration(schedule.RetainDays) * 24 * time.Hour).Unix()
	}
	if err := m.updateRecord(record); err != nil {
		log.Printf("remote backup: save completed record: %v", err)
	}
	m.cleanup(ctx, objectStore)
}

func (m *Manager) failRecord(record Record, err error) {
	record.Status = "failed"
	record.Error = err.Error()
	record.FinishedAt = time.Now().Unix()
	if saveErr := m.updateRecord(record); saveErr != nil {
		log.Printf("remote backup: save failed record: %v", saveErr)
	}
}

func (m *Manager) cleanup(ctx context.Context, objectStore objectStore) {
	schedule, err := m.LoadSchedule()
	if err != nil {
		return
	}
	records, err := m.loadRecords()
	if err != nil {
		return
	}
	completed := make([]Record, 0, len(records))
	for _, record := range records {
		if record.Status == "completed" {
			completed = append(completed, record)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].StartedAt > completed[j].StartedAt })
	keep := map[string]bool{}
	for i, record := range completed {
		keep[record.ID] = true
		if schedule.RetainCount > 0 && i >= schedule.RetainCount {
			keep[record.ID] = false
		}
		if record.ExpiresAt > 0 && record.ExpiresAt <= time.Now().Unix() {
			keep[record.ID] = false
		}
	}
	remaining := make([]Record, 0, len(records))
	for _, record := range records {
		if record.Status == "completed" && !keep[record.ID] {
			if err := objectStore.Delete(ctx, record.ObjectKey); err != nil {
				log.Printf("remote backup: delete expired object %s: %v", record.ObjectKey, err)
				remaining = append(remaining, record)
			}
			continue
		}
		remaining = append(remaining, record)
	}
	if err := m.saveRecords(remaining); err != nil {
		log.Printf("remote backup: save retention records: %v", err)
	}
}

func (m *Manager) DownloadURL(ctx context.Context, id string) (string, error) {
	cfg, err := m.loadFullConfig()
	if err != nil {
		return "", err
	}
	records, err := m.loadRecords()
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.ID == id {
			if record.Status != "completed" {
				return "", fmt.Errorf("备份尚未完成")
			}
			objectStore, err := m.factory(ctx, cfg)
			if err != nil {
				return "", err
			}
			return objectStore.PresignURL(ctx, record.ObjectKey, time.Hour)
		}
	}
	return "", fmt.Errorf("备份记录不存在")
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	cfg, err := m.loadFullConfig()
	if err != nil {
		return err
	}
	records, err := m.loadRecords()
	if err != nil {
		return err
	}
	objectStore, err := m.factory(ctx, cfg)
	if err != nil {
		return err
	}
	remaining := make([]Record, 0, len(records))
	found := false
	for _, record := range records {
		if record.ID != id {
			remaining = append(remaining, record)
			continue
		}
		found = true
		if record.Status == "completed" {
			if err := objectStore.Delete(ctx, record.ObjectKey); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("备份记录不存在")
	}
	return m.saveRecords(remaining)
}

func joinKey(prefix, filename string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return filename
	}
	return prefix + "/" + filename
}

func fileDigest(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
