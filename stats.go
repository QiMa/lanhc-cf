package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 自托管访问统计：替代 counter.dev（该服务长期返回 400，只有跨域往返没有数据）。
// 事件以 JSONL 追加落盘，进程内维护按天聚合；不写 cookie，不存 IP 和 UA。
const (
	statsMaxBody      = 2 << 10
	statsRetentionDay = 400
	statsMaxSessions  = 20000 // 单日去重 session 上限，防止内存被刷爆
	statsDefaultDays  = 7
	statsMaxDays      = 90
)

type statsEvent struct {
	TS     int64  `json:"ts"`
	Path   string `json:"path"`
	Ref    string `json:"ref,omitempty"`
	Screen string `json:"screen,omitempty"`
	SID    string `json:"sid,omitempty"`
	Lang   string `json:"lang,omitempty"`
}

type statsDay struct {
	Views    int64
	Sessions int
	Paths    map[string]int64
	Refs     map[string]int64
	sids     map[string]struct{}
}

// StatsService is a tiny first-party page-view counter: one append-only log,
// in-memory rollups, and a password-gated summary endpoint.
type StatsService struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	total     int64
	days      map[string]*statsDay
	origins   map[string]bool
	adminPass string
	limitMu   sync.Mutex
	limits    map[string]*statsBucket
	started   time.Time
}

type statsBucket struct {
	count int
	reset time.Time
}

func newStatsService(dir string) (*StatsService, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &StatsService{
		path:      filepath.Join(dir, "stats.jsonl"),
		days:      map[string]*statsDay{},
		origins:   map[string]bool{},
		adminPass: env("CHAT_ADMIN_PASSWORD", ""),
		limits:    map[string]*statsBucket{},
		started:   time.Now(),
	}
	for _, o := range strings.Split(env("STATS_ALLOWED_ORIGINS", env("CHAT_ALLOWED_ORIGINS", "https://lanhc.com https://www.lanhc.com http://localhost:1313")), " ") {
		if o = strings.TrimSpace(o); o != "" {
			s.origins[o] = true
		}
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	s.file = f
	return s, nil
}

// load replays the log into memory and compacts entries older than the retention window.
func (s *StatsService) load() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	cutoff := time.Now().AddDate(0, 0, -statsRetentionDay).Unix()
	kept := make([]string, 0, 4096)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev statsEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.TS < cutoff {
			continue
		}
		s.apply(ev)
		kept = append(kept, line)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(kept) == 0 {
		return nil
	}
	// Rewrite only when the replay actually dropped rows.
	if dropped := countLines(s.path) - len(kept); dropped > 0 {
		tmp := s.path + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, s.path); err != nil {
			return err
		}
		log.Printf("stats: compacted %d expired rows", dropped)
	}
	return nil
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

func (s *StatsService) apply(ev statsEvent) {
	day := time.Unix(ev.TS, 0).In(time.Local).Format("2006-01-02")
	d, ok := s.days[day]
	if !ok {
		d = &statsDay{Paths: map[string]int64{}, Refs: map[string]int64{}, sids: map[string]struct{}{}}
		s.days[day] = d
	}
	d.Views++
	d.Paths[ev.Path]++
	if ev.Ref != "" {
		d.Refs[ev.Ref]++
	}
	if ev.SID != "" && len(d.sids) < statsMaxSessions {
		if _, seen := d.sids[ev.SID]; !seen {
			d.sids[ev.SID] = struct{}{}
			d.Sessions = len(d.sids)
		}
	}
	s.total++
}

func (s *StatsService) Register(mux *http.ServeMux) {
	mux.HandleFunc("/stats/api/collect", s.handleCollect)
	mux.HandleFunc("/stats/api/summary", s.handleSummary)
	mux.HandleFunc("/stats/collect.js", s.serveCollectJS)
	mux.HandleFunc("/stats/", s.serveDashboard)
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/stats/", http.StatusFound)
	})
}

func (s *StatsService) allow(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !s.origins[origin] {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "600")
	return true
}

func (s *StatsService) rateLimited(key string, limit int, window time.Duration) bool {
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	now := time.Now()
	if len(s.limits) > 4096 {
		for k, b := range s.limits {
			if now.After(b.reset) {
				delete(s.limits, k)
			}
		}
	}
	b, ok := s.limits[key]
	if !ok || now.After(b.reset) {
		s.limits[key] = &statsBucket{count: 1, reset: now.Add(window)}
		return false
	}
	b.count++
	return b.count > limit
}

func cleanPath(v string) string {
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	if len(v) > 200 {
		v = v[:200]
	}
	// 只保留站内路径的可见字符，避免把注入内容写进日志
	var b strings.Builder
	for _, r := range v {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '<' || r == '>' || r == '"' || r == '\'' {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

func cleanRef(v string) string {
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 100 {
		host = host[:100]
	}
	return host
}

func cleanToken(v string, max int) string {
	v = strings.TrimSpace(v)
	if len(v) > max {
		v = v[:max]
	}
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *StatsService) handleCollect(w http.ResponseWriter, r *http.Request) {
	if !s.allow(w, r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.rateLimited("collect:"+clientIP(r), 120, time.Minute) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, statsMaxBody))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var in struct {
		Path   string `json:"p"`
		Ref    string `json:"r"`
		Screen string `json:"s"`
		SID    string `json:"sid"`
		Lang   string `json:"l"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	ev := statsEvent{
		TS:     time.Now().Unix(),
		Path:   cleanPath(in.Path),
		Ref:    cleanRef(in.Ref),
		Screen: cleanToken(in.Screen, 16),
		SID:    cleanToken(in.SID, 32),
		Lang:   cleanToken(in.Lang, 12),
	}
	line, err := json.Marshal(ev)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	if _, err := s.file.Write(append(line, '\n')); err != nil {
		s.mu.Unlock()
		log.Println("stats: write failed:", err)
		http.Error(w, "storage failed", http.StatusInternalServerError)
		return
	}
	s.apply(ev)
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *StatsService) authorized(r *http.Request) bool {
	if s.adminPass == "" {
		return false
	}
	token := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimSpace(h[len("Bearer "):])
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.adminPass)) == 1
}

type statsDayView struct {
	Day      string `json:"day"`
	Views    int64  `json:"views"`
	Sessions int    `json:"sessions"`
}

type statsRow struct {
	Key   string `json:"key"`
	Views int64  `json:"views"`
}

func (s *StatsService) handleSummary(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="stats"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	days := statsDefaultDays
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	if days > statsMaxDays {
		days = statsMaxDays
	}

	s.mu.Lock()
	from := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	dayViews := make([]statsDayView, 0, days)
	paths := map[string]int64{}
	refs := map[string]int64{}
	var views int64
	sessions := 0
	for i := days - 1; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		d := s.days[day]
		if d == nil {
			dayViews = append(dayViews, statsDayView{Day: day})
			continue
		}
		views += d.Views
		sessions += d.Sessions
		dayViews = append(dayViews, statsDayView{Day: day, Views: d.Views, Sessions: d.Sessions})
		for k, v := range d.Paths {
			paths[k] += v
		}
		for k, v := range d.Refs {
			refs[k] += v
		}
	}
	total := s.total
	s.mu.Unlock()

	resp := struct {
		Days      []statsDayView `json:"days"`
		Views     int64          `json:"views"`
		Sessions  int            `json:"sessions"`
		Total     int64          `json:"total"`
		From      string         `json:"from"`
		To        string         `json:"to"`
		TopPaths  []statsRow     `json:"top_paths"`
		TopRefs   []statsRow     `json:"top_refs"`
		Generated int64          `json:"generated_at"`
	}{Days: dayViews, Views: views, Sessions: sessions, Total: total, From: from, To: time.Now().Format("2006-01-02"), Generated: time.Now().Unix()}

	resp.TopPaths = rankRows(paths, 15)
	resp.TopRefs = rankRows(refs, 10)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func rankRows(m map[string]int64, limit int) []statsRow {
	rows := make([]statsRow, 0, len(m))
	for k, v := range m {
		rows = append(rows, statsRow{Key: k, Views: v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Views == rows[j].Views {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Views > rows[j].Views
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}
