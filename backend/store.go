package main

// JSON 文件持久化存储（带读写锁）
import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"
)

type Task struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // container | compose
	Name      string `json:"name"` // container_name 或 project_name
	Action    string `json:"action"`
	Cron      string `json:"cron_expression"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

type APIKey struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

type StoreData struct {
	PasswordSalt        string            `json:"password_salt"`
	PasswordHash        string            `json:"password_hash"`
	AgreedForever       bool              `json:"agreed_forever"`
	Aliases             map[string]any    `json:"aliases"`
	UserPrefs           map[string]any    `json:"user_prefs"`
	APIKeys             []APIKey          `json:"api_keys"`
	Templates           []map[string]any  `json:"templates"`
	Tasks               []Task            `json:"tasks"`
	AutoUpdate          map[string]bool   `json:"auto_update_containers"`
	UpdateIntervalDays  int               `json:"update_interval_days"`
	UpdateIntervalHours int               `json:"update_interval_hours"`
	LastCheck           string            `json:"last_check"`
	UpdateStatus        map[string]string `json:"update_status"`
	HasUpdate           map[string]bool   `json:"has_update"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	Data StoreData
}

func NewStore(path string) *Store {
	s := &Store{path: path}
	s.Data.Aliases = map[string]any{}
	s.Data.UserPrefs = map[string]any{}
	s.Data.AutoUpdate = map[string]bool{}
	s.Data.UpdateStatus = map[string]string{}
	s.Data.HasUpdate = map[string]bool{}
	s.Data.UpdateIntervalDays = 7
	s.Load()
	if s.Data.PasswordSalt == "" {
		s.Data.PasswordSalt = randHex(16)
		s.SetPassword("admin")
		// 自研面板：默认同意首次声明，避免展示 diancup 原版声明弹窗
		s.Data.AgreedForever = true
		s.Save()
	}
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) Load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	json.Unmarshal(b, &s.Data)
}

func (s *Store) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(s.Data, "", "  ")
	tmp := s.path + ".tmp"
	os.WriteFile(tmp, b, 0644)
	os.Rename(tmp, s.path)
}

func (s *Store) SetPassword(pw string) {
	sum := sha256.Sum256([]byte(s.Data.PasswordSalt + pw))
	s.Data.PasswordHash = hex.EncodeToString(sum[:])
}

func (s *Store) CheckPassword(pw string) bool {
	sum := sha256.Sum256([]byte(s.Data.PasswordSalt + pw))
	return hex.EncodeToString(sum[:]) == s.Data.PasswordHash
}

func (s *Store) IsDefaultPassword() bool {
	return s.CheckPassword("admin")
}

func (s *Store) HasAPIKey(k string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range s.Data.APIKeys {
		if key.Enabled && key.Key == k {
			return true
		}
	}
	return false
}

func (s *Store) UsingDefaultPassword() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsDefaultPassword()
}

func (s *Store) Now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
