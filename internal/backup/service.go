// Package backup copies all platform data (the whole database) to local
// backup files and a WebDAV server, on a schedule or on demand, and
// restores it from either.
package backup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"vpsbill/internal/clock"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

var (
	ErrInvalid     = errors.New("invalid backup request")
	ErrBusy        = errors.New("另一项备份或还原正在进行，请稍后再试")
	ErrNotFound    = errors.New("备份文件不存在")
	ErrKeyMismatch = errors.New("key mismatch")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Hooks let a restore stop the background workers first and restart the
// process afterwards, so nothing keeps running on data it replaced.
type Hooks struct {
	StopWork func()
	Exit     func()
}

// Activity is the backup or restore in progress.
type Activity struct {
	Kind      string    `json:"kind"`
	Stage     string    `json:"stage"`
	File      string    `json:"file,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

type Service struct {
	db             *pgxpool.Pool
	databaseURL    string
	dir            string
	box            *security.SecretBox
	logger         *slog.Logger
	version        string
	keyFingerprint string
	hooks          Hooks
	now            func() time.Time

	busy      sync.Mutex
	activity  atomic.Pointer[Activity]
	restoring atomic.Bool
}

func New(db *pgxpool.Pool, databaseURL, dir string, box *security.SecretBox, encryptionKey, version string, logger *slog.Logger) *Service {
	s := &Service{
		db: db, databaseURL: databaseURL, dir: dir, box: box, logger: logger, version: version,
		keyFingerprint: KeyFingerprint(encryptionKey), now: time.Now,
		hooks: Hooks{StopWork: func() {}, Exit: func() { os.Exit(0) }},
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.Warn("create backup directory", "dir", dir, "error", err)
	}
	// Leftovers of a backup cut short by a restart.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".tmp-") {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	return s
}

func (s *Service) SetHooks(hooks Hooks) { s.hooks = hooks }

// KeyFingerprint identifies an encryption key without revealing it.
func KeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte("vpsbill-backup-key:" + key))
	return hex.EncodeToString(sum[:6])
}

// Activity is the operation in progress, or nil.
func (s *Service) Activity() *Activity { return s.activity.Load() }

// Restoring reports a restore in progress; the API then only answers
// status requests.
func (s *Service) Restoring() bool { return s.restoring.Load() }

func (s *Service) setActivity(kind, stage, file string, started time.Time) {
	s.activity.Store(&Activity{Kind: kind, Stage: stage, File: file, StartedAt: started})
}

// ---- Settings ----

// Settings is the schedule, retention and WebDAV target.
type Settings struct {
	Schedule      string
	DailyAt       string
	IntervalHours int
	KeepLocal     int
	KeepRemote    int
	WebDAV        WebDAV
	ScheduledAt   *time.Time
}

// SettingsView is what the admin page shows; the WebDAV password only as
// set or not.
type SettingsView struct {
	Schedule          string     `json:"schedule"`
	DailyAt           string     `json:"daily_at"`
	IntervalHours     int        `json:"interval_hours"`
	KeepLocal         int        `json:"keep_local"`
	KeepRemote        int        `json:"keep_remote"`
	WebDAVURL         string     `json:"webdav_url"`
	WebDAVUsername    string     `json:"webdav_username"`
	WebDAVPasswordSet bool       `json:"webdav_password_set"`
	WebDAVDirectory   string     `json:"webdav_directory"`
	WebDAVInsecure    bool       `json:"webdav_insecure"`
	NextRunAt         *time.Time `json:"next_run_at,omitempty"`
}

// SettingsInput changes the settings; an empty password keeps the stored
// one (clearing the URL drops it).
type SettingsInput struct {
	Schedule        string `json:"schedule"`
	DailyAt         string `json:"daily_at"`
	IntervalHours   int    `json:"interval_hours"`
	KeepLocal       int    `json:"keep_local"`
	KeepRemote      int    `json:"keep_remote"`
	WebDAVURL       string `json:"webdav_url"`
	WebDAVUsername  string `json:"webdav_username"`
	WebDAVPassword  string `json:"webdav_password"`
	WebDAVDirectory string `json:"webdav_directory"`
	WebDAVInsecure  bool   `json:"webdav_insecure"`
}

var dailyPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	var sealed []byte
	err := s.db.QueryRow(ctx, `SELECT schedule,daily_at,interval_hours,keep_local,keep_remote,webdav_url,webdav_username,webdav_password_encrypted,webdav_directory,webdav_insecure,scheduled_at FROM backup_config WHERE singleton`).
		Scan(&settings.Schedule, &settings.DailyAt, &settings.IntervalHours, &settings.KeepLocal, &settings.KeepRemote,
			&settings.WebDAV.URL, &settings.WebDAV.Username, &sealed, &settings.WebDAV.Directory, &settings.WebDAV.Insecure, &settings.ScheduledAt)
	if err != nil {
		return Settings{}, err
	}
	if len(sealed) > 0 {
		if settings.WebDAV.Password, err = s.box.Open(sealed); err != nil {
			return Settings{}, fmt.Errorf("decrypt WebDAV password: %w", err)
		}
	}
	return settings, nil
}

func (s *Service) View(ctx context.Context) (SettingsView, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{
		Schedule: settings.Schedule, DailyAt: settings.DailyAt, IntervalHours: settings.IntervalHours,
		KeepLocal: settings.KeepLocal, KeepRemote: settings.KeepRemote,
		WebDAVURL: settings.WebDAV.URL, WebDAVUsername: settings.WebDAV.Username, WebDAVPasswordSet: settings.WebDAV.Password != "",
		WebDAVDirectory: settings.WebDAV.Directory, WebDAVInsecure: settings.WebDAV.Insecure,
		NextRunAt: nextRun(settings, s.now()),
	}, nil
}

// webdavFrom is the WebDAV target an input describes, keeping the stored
// password when none is given.
func (s *Service) webdavFrom(ctx context.Context, in SettingsInput) (WebDAV, error) {
	target := WebDAV{
		URL: strings.TrimSpace(in.WebDAVURL), Username: strings.TrimSpace(in.WebDAVUsername), Password: in.WebDAVPassword,
		Directory: strings.Trim(strings.TrimSpace(in.WebDAVDirectory), "/"), Insecure: in.WebDAVInsecure,
	}
	if target.URL != "" && !strings.HasPrefix(target.URL, "http://") && !strings.HasPrefix(target.URL, "https://") {
		return WebDAV{}, invalid("WebDAV 地址需以 http:// 或 https:// 开头")
	}
	if strings.Contains(target.Directory, "..") {
		return WebDAV{}, invalid("备份目录不能包含 ..")
	}
	if target.Password == "" && target.URL != "" {
		current, err := s.Settings(ctx)
		if err != nil {
			return WebDAV{}, err
		}
		target.Password = current.WebDAV.Password
	}
	return target, nil
}

func (s *Service) Save(ctx context.Context, in SettingsInput) (SettingsView, error) {
	switch in.Schedule {
	case "off", "daily", "interval":
	default:
		return SettingsView{}, invalid("请选择备份计划")
	}
	if !dailyPattern.MatchString(in.DailyAt) {
		return SettingsView{}, invalid("每日备份时间格式为 HH:MM，例如 03:30")
	}
	if in.IntervalHours < 1 || in.IntervalHours > 168 {
		return SettingsView{}, invalid("备份间隔为 1–168 小时")
	}
	if in.KeepLocal < 1 || in.KeepLocal > 365 || in.KeepRemote < 1 || in.KeepRemote > 365 {
		return SettingsView{}, invalid("保留份数为 1–365")
	}
	target, err := s.webdavFrom(ctx, in)
	if err != nil {
		return SettingsView{}, err
	}
	var sealed []byte
	if target.URL != "" && target.Password != "" {
		if sealed, err = s.box.Seal(target.Password); err != nil {
			return SettingsView{}, err
		}
	}
	if target.URL == "" {
		target = WebDAV{Directory: target.Directory}
	}
	if target.Directory == "" {
		target.Directory = "vpsbill"
	}
	// A changed plan starts counting now, so saving never fires a backup
	// for a slot that has already passed.
	_, err = s.db.Exec(ctx, `
		UPDATE backup_config SET
		    scheduled_at = CASE WHEN schedule<>$1 OR daily_at<>$2 OR interval_hours<>$3 THEN now() ELSE scheduled_at END,
		    schedule=$1, daily_at=$2, interval_hours=$3, keep_local=$4, keep_remote=$5,
		    webdav_url=$6, webdav_username=$7, webdav_password_encrypted=$8, webdav_directory=$9, webdav_insecure=$10, updated_at=now()
		WHERE singleton`,
		in.Schedule, in.DailyAt, in.IntervalHours, in.KeepLocal, in.KeepRemote,
		target.URL, target.Username, sealed, target.Directory, target.Insecure)
	if err != nil {
		return SettingsView{}, err
	}
	return s.View(ctx)
}

// TestWebDAV tries the target in the input (not yet saved) end to end.
func (s *Service) TestWebDAV(ctx context.Context, in SettingsInput) (string, error) {
	target, err := s.webdavFrom(ctx, in)
	if err != nil {
		return "", err
	}
	if target.Directory == "" {
		target.Directory = "vpsbill"
	}
	return target.Test(ctx)
}

// ---- Files ----

// LocalFile is a backup in the local directory.
type LocalFile struct {
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	Trigger   string    `json:"trigger"`
	Version   string    `json:"app_version"`
	// KeyMatches is false when the backup's secrets were sealed with
	// another ENCRYPTION_KEY.
	KeyMatches bool   `json:"key_matches"`
	Error      string `json:"error,omitempty"`
}

func (s *Service) ListLocal() ([]LocalFile, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	files := []LocalFile{}
	for _, entry := range entries {
		if entry.IsDir() || !ValidName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		file := LocalFile{Name: entry.Name(), SizeBytes: info.Size(), CreatedAt: info.ModTime()}
		if manifest, err := readManifest(filepath.Join(s.dir, entry.Name())); err != nil {
			file.Error = err.Error()
		} else {
			file.CreatedAt, file.Trigger, file.Version = manifest.CreatedAt, manifest.Trigger, manifest.AppVersion
			file.KeyMatches = manifest.KeyFingerprint == s.keyFingerprint
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return files, nil
}

// LocalPath is the path of a local backup, for download.
func (s *Service) LocalPath(name string) (string, error) {
	if !ValidName(name) {
		return "", ErrNotFound
	}
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); err != nil {
		return "", ErrNotFound
	}
	return path, nil
}

func (s *Service) DeleteLocal(name string) error {
	path, err := s.LocalPath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *Service) ListRemote(ctx context.Context) ([]RemoteFile, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.WebDAV.Configured() {
		return nil, invalid("还没有配置 WebDAV")
	}
	return settings.WebDAV.List(ctx)
}

// Persistent reports whether the backup directory is on a mounted volume;
// otherwise recreating the container loses the local backups.
func (s *Service) Persistent() bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return true
	}
	defer file.Close()
	dir := filepath.Clean(s.dir)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || fields[4] == "/" {
			continue
		}
		if dir == fields[4] || strings.HasPrefix(dir, fields[4]+"/") {
			return true
		}
	}
	return false
}

// SaveUpload stores an uploaded backup file after checking it whole.
func (s *Service) SaveUpload(body io.Reader) (LocalFile, error) {
	temp, err := os.CreateTemp(s.dir, ".tmp-upload-*")
	if err != nil {
		return LocalFile{}, err
	}
	defer os.Remove(temp.Name())
	if _, err := io.Copy(temp, body); err != nil {
		temp.Close()
		return LocalFile{}, fmt.Errorf("上传中断：%w", err)
	}
	if err := temp.Close(); err != nil {
		return LocalFile{}, err
	}
	manifest, err := extractDump(temp.Name(), "")
	if err != nil {
		return LocalFile{}, invalid("%s", err.Error())
	}
	name := s.freeName(s.now().In(clock.Zone), "upload")
	if err := os.Rename(temp.Name(), filepath.Join(s.dir, name)); err != nil {
		return LocalFile{}, err
	}
	info, _ := os.Stat(filepath.Join(s.dir, name))
	file := LocalFile{Name: name, CreatedAt: manifest.CreatedAt, Trigger: manifest.Trigger, Version: manifest.AppVersion, KeyMatches: manifest.KeyFingerprint == s.keyFingerprint}
	if info != nil {
		file.SizeBytes = info.Size()
	}
	return file, nil
}

// ---- History ----

// Run is one backup, restore or WebDAV download.
type Run struct {
	ID           int64      `json:"id"`
	Kind         string     `json:"kind"`
	Trigger      string     `json:"trigger"`
	FileName     string     `json:"file_name"`
	SizeBytes    int64      `json:"size_bytes"`
	Status       string     `json:"status"`
	WebDAVStatus string     `json:"webdav_status"`
	Message      string     `json:"message"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func (s *Service) Runs(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.db.Query(ctx, `SELECT id,kind,trigger,file_name,size_bytes,status,webdav_status,message,started_at,finished_at FROM backup_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.Kind, &run.Trigger, &run.FileName, &run.SizeBytes, &run.Status, &run.WebDAVStatus, &run.Message, &run.StartedAt, &run.FinishedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Service) startRun(ctx context.Context, kind, trigger, file, actorID string) int64 {
	var id int64
	if err := s.db.QueryRow(ctx, `INSERT INTO backup_runs(kind,trigger,file_name,actor_id) VALUES($1,$2,$3,nullif($4,'')::uuid) RETURNING id`, kind, trigger, file, actorID).Scan(&id); err != nil {
		s.logger.Warn("record backup run", "error", err)
	}
	return id
}

func (s *Service) finishRun(ctx context.Context, id int64, status, webdavStatus, file string, size int64, message string) {
	if id == 0 {
		return
	}
	if _, err := s.db.Exec(ctx, `UPDATE backup_runs SET status=$2,webdav_status=$3,file_name=CASE WHEN $4='' THEN file_name ELSE $4 END,size_bytes=$5,message=left($6,1000),finished_at=now() WHERE id=$1`,
		id, status, webdavStatus, file, size, message); err != nil {
		s.logger.Warn("record backup result", "error", err)
	}
}

// ---- Backup ----

// Start makes a backup in the background: a local file, then a copy on
// WebDAV when one is set.
func (s *Service) Start(trigger, actorID string) error {
	if !s.busy.TryLock() {
		return ErrBusy
	}
	go func() {
		defer s.busy.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		_, _ = s.backup(ctx, trigger, actorID, true)
	}()
	return nil
}

// backup runs with s.busy held.
func (s *Service) backup(ctx context.Context, trigger, actorID string, upload bool) (string, error) {
	started := s.now()
	s.setActivity("backup", "正在导出数据库", "", started)
	defer s.activity.Store(nil)
	runID := s.startRun(ctx, "backup", trigger, "", actorID)
	name, size, err := s.createLocal(ctx, trigger)
	if err != nil {
		s.logger.Error("backup failed", "trigger", trigger, "error", err)
		s.finishRun(ctx, runID, "failed", "", name, 0, err.Error())
		return "", err
	}
	s.pruneLocal()
	status, webdavStatus, message := "succeeded", "", "已保存到本地"
	if upload {
		settings, err := s.Settings(ctx)
		switch {
		case err != nil:
			status, webdavStatus, message = "partial", "failed", "已保存到本地；读取 WebDAV 设置失败："+err.Error()
		case !settings.WebDAV.Configured():
			webdavStatus, message = "skipped", "已保存到本地（未配置 WebDAV）"
		default:
			s.setActivity("backup", "正在上传到 WebDAV", name, started)
			if err := settings.WebDAV.Upload(ctx, name, filepath.Join(s.dir, name)); err != nil {
				status, webdavStatus, message = "partial", "failed", "已保存到本地；上传 WebDAV 失败："+err.Error()
			} else {
				webdavStatus, message = "uploaded", "已保存到本地并上传到 WebDAV"
				s.pruneRemote(ctx, settings)
			}
		}
	}
	s.finishRun(ctx, runID, status, webdavStatus, name, size, message)
	s.logger.Info("backup finished", "file", name, "bytes", size, "status", status, "webdav", webdavStatus)
	return name, nil
}

func (s *Service) createLocal(ctx context.Context, trigger string) (string, int64, error) {
	now := s.now().In(clock.Zone)
	name := s.freeName(now, trigger)
	dumpPath := filepath.Join(s.dir, ".tmp-"+name+".dump")
	tarPath := filepath.Join(s.dir, ".tmp-"+name)
	defer os.Remove(dumpPath)
	defer os.Remove(tarPath)
	var versionNum int
	var serverVersion, schema string
	if err := s.db.QueryRow(ctx, `SELECT current_setting('server_version_num')::int, current_setting('server_version'), coalesce((SELECT max(version) FROM schema_migrations),'')`).Scan(&versionNum, &serverVersion, &schema); err != nil {
		return "", 0, fmt.Errorf("读取数据库版本失败：%w", err)
	}
	if err := pgDump(ctx, s.databaseURL, versionNum/10000, dumpPath); err != nil {
		return "", 0, err
	}
	size, sum, err := hashFile(dumpPath)
	if err != nil {
		return "", 0, err
	}
	manifest := Manifest{
		Format: formatVersion, CreatedAt: now, Trigger: trigger, AppVersion: s.version, SchemaVersion: schema,
		PostgresVersion: serverVersion, KeyFingerprint: s.keyFingerprint, DumpSize: size, DumpSHA256: sum,
	}
	if err := writeArchive(tarPath, manifest, dumpPath); err != nil {
		return "", 0, fmt.Errorf("写入备份文件失败：%w", err)
	}
	if err := os.Rename(tarPath, filepath.Join(s.dir, name)); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(filepath.Join(s.dir, name))
	if err != nil {
		return "", 0, err
	}
	return name, info.Size(), nil
}

// freeName names a new backup, a second later while the name is taken
// (two uploads, or a backup right after another, in the same second).
func (s *Service) freeName(at time.Time, trigger string) string {
	for {
		name := fileName(at, trigger)
		if _, err := os.Stat(filepath.Join(s.dir, name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
		at = at.Add(time.Second)
	}
}

// keep lists what retention keeps: the newest rotating backups.
func keep(names []string, count int) (drop []string) {
	var rotated []string
	for _, name := range names {
		if rotating(name) {
			rotated = append(rotated, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(rotated)))
	if len(rotated) > count {
		return rotated[count:]
	}
	return nil
}

func (s *Service) pruneLocal() {
	settings, err := s.Settings(context.Background())
	if err != nil {
		return
	}
	files, err := s.ListLocal()
	if err != nil {
		return
	}
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	for _, name := range keep(names, settings.KeepLocal) {
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil {
			s.logger.Warn("remove old backup", "file", name, "error", err)
		}
	}
}

func (s *Service) pruneRemote(ctx context.Context, settings Settings) {
	files, err := settings.WebDAV.List(ctx)
	if err != nil {
		s.logger.Warn("list WebDAV backups", "error", err)
		return
	}
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	for _, name := range keep(names, settings.KeepRemote) {
		if err := settings.WebDAV.Delete(ctx, name); err != nil {
			s.logger.Warn("remove old WebDAV backup", "file", name, "error", err)
		}
	}
}

// ---- WebDAV download ----

// Fetch downloads a backup from WebDAV into the local directory in the
// background, checking it whole, so it can then be restored.
func (s *Service) Fetch(name, actorID string) error {
	if !ValidName(name) {
		return ErrNotFound
	}
	if !s.busy.TryLock() {
		return ErrBusy
	}
	go func() {
		defer s.busy.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		s.setActivity("fetch", "正在从 WebDAV 下载", name, s.now())
		defer s.activity.Store(nil)
		runID := s.startRun(ctx, "fetch", "manual", name, actorID)
		size, err := s.fetch(ctx, name)
		if err != nil {
			s.finishRun(ctx, runID, "failed", "", name, 0, err.Error())
			return
		}
		s.finishRun(ctx, runID, "succeeded", "", name, size, "已下载到本地，可以还原")
	}()
	return nil
}

func (s *Service) fetch(ctx context.Context, name string) (int64, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if !settings.WebDAV.Configured() {
		return 0, errors.New("还没有配置 WebDAV")
	}
	temp := filepath.Join(s.dir, ".tmp-fetch-"+name)
	defer os.Remove(temp)
	if err := settings.WebDAV.Download(ctx, name, temp); err != nil {
		return 0, err
	}
	if _, err := extractDump(temp, ""); err != nil {
		return 0, err
	}
	if err := os.Rename(temp, filepath.Join(s.dir, name)); err != nil {
		return 0, err
	}
	info, err := os.Stat(filepath.Join(s.dir, name))
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ---- Restore ----

// Restore checks a local backup, then in the background backs up the
// current data, stops the workers and replaces every table with the
// backup's in one transaction. The process then exits so the container
// restarts on the restored data.
func (s *Service) Restore(ctx context.Context, name string, allowKeyMismatch bool, actorID string) (Manifest, error) {
	path, err := s.LocalPath(name)
	if err != nil {
		return Manifest{}, err
	}
	if !s.busy.TryLock() {
		return Manifest{}, ErrBusy
	}
	manifest, err := extractDump(path, "")
	if err == nil && manifest.SchemaVersion > postgres.LatestMigration() {
		err = invalid("这份备份来自更新的版本（数据库结构 %s），请先把系统升级到同一版本或更新版本再还原", manifest.SchemaVersion)
	}
	if err == nil && manifest.KeyFingerprint != s.keyFingerprint && !allowKeyMismatch {
		err = ErrKeyMismatch
	}
	if err != nil {
		s.busy.Unlock()
		if errors.Is(err, ErrKeyMismatch) || errors.Is(err, ErrInvalid) {
			return manifest, err
		}
		return manifest, invalid("%s", err.Error())
	}
	go s.restore(path, name, actorID)
	return manifest, nil
}

func (s *Service) restore(path, name, actorID string) {
	defer s.busy.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	started := s.now()
	runID := s.startRun(ctx, "restore", "manual", name, actorID)
	s.setActivity("restore", "正在备份当前数据", name, started)
	if _, err := s.backup(ctx, "pre-restore", actorID, false); err != nil {
		s.finishRun(ctx, runID, "failed", "", name, 0, "还原前备份当前数据失败，未做任何改动："+err.Error())
		s.activity.Store(nil)
		return
	}
	s.setActivity("restore", "正在解包备份", name, started)
	dumpPath := filepath.Join(s.dir, ".tmp-restore.dump")
	defer os.Remove(dumpPath)
	if _, err := extractDump(path, dumpPath); err != nil {
		s.finishRun(ctx, runID, "failed", "", name, 0, "解包失败，未做任何改动："+err.Error())
		s.activity.Store(nil)
		return
	}

	// From here on the process restarts whatever happens.
	s.restoring.Store(true)
	s.setActivity("restore", "正在还原数据库", name, started)
	s.hooks.StopWork()
	var versionNum int
	_ = s.db.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum)
	// Other sessions (other API instances, stray queries) would hold locks
	// the restore needs; the pools reconnect by themselves.
	_, _ = s.db.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`)
	err := pgRestore(ctx, s.databaseURL, versionNum/10000, dumpPath)
	status, message := "succeeded", "还原完成，服务已自动重启；请重新登录"
	if err != nil {
		status, message = "failed", "还原失败，数据库已回滚到还原前的状态："+err.Error()
		s.logger.Error("restore failed", "file", name, "error", err)
	} else {
		s.logger.Info("restore finished", "file", name)
	}
	// The pool's connections were cut; record the result on a new one.
	if conn, connErr := pgx.Connect(ctx, s.databaseURL); connErr == nil {
		_, _ = conn.Exec(ctx, `UPDATE backup_runs SET status=$2,message=$3,finished_at=now() WHERE id=$1`, runID, status, message)
		conn.Close(ctx)
	}
	s.setActivity("restore", "即将重启服务", name, started)
	time.Sleep(3 * time.Second)
	s.hooks.Exit()
}

// ---- Schedule ----

// RunScheduler starts the scheduled backups; with several API instances
// only the one that claims a slot runs it.
func (s *Service) RunScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimed, err := s.claim(ctx)
			if err != nil {
				s.logger.Warn("backup schedule", "error", err)
				continue
			}
			if claimed {
				if err := s.Start("scheduled", ""); err != nil {
					s.logger.Warn("scheduled backup skipped", "error", err)
				}
			}
		}
	}
}

func (s *Service) claim(ctx context.Context) (bool, error) {
	settings, err := s.Settings(ctx)
	if err != nil || settings.Schedule == "off" {
		return false, err
	}
	var command string
	var args []any
	switch settings.Schedule {
	case "daily":
		command = `UPDATE backup_config SET scheduled_at=$1 WHERE singleton AND schedule='daily' AND daily_at=$2 AND (scheduled_at IS NULL OR scheduled_at < $1)`
		args = []any{dailySlot(s.now(), settings.DailyAt), settings.DailyAt}
	case "interval":
		command = `UPDATE backup_config SET scheduled_at=now() WHERE singleton AND schedule='interval' AND (scheduled_at IS NULL OR scheduled_at <= now() - make_interval(hours => interval_hours))`
	}
	result, err := s.db.Exec(ctx, command, args...)
	return err == nil && result.RowsAffected() == 1, err
}

// dailySlot is the latest time at or before now that a daily backup at
// HH:MM (UTC+8) was due.
func dailySlot(now time.Time, at string) time.Time {
	var hour, minute int
	_, _ = fmt.Sscanf(at, "%d:%d", &hour, &minute)
	local := now.In(clock.Zone)
	slot := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, clock.Zone)
	if now.Before(slot) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// nextRun is when the schedule fires next, nil when it is off.
func nextRun(settings Settings, now time.Time) *time.Time {
	var next time.Time
	switch settings.Schedule {
	case "daily":
		slot := dailySlot(now, settings.DailyAt)
		next = slot.AddDate(0, 0, 1)
		if settings.ScheduledAt == nil || settings.ScheduledAt.Before(slot) {
			next = now
		}
	case "interval":
		next = now
		if settings.ScheduledAt != nil {
			next = settings.ScheduledAt.Add(time.Duration(settings.IntervalHours) * time.Hour)
		}
	default:
		return nil
	}
	if next.Before(now) {
		next = now
	}
	return &next
}
