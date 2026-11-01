package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed chatui
var chatUIFS embed.FS

const (
	adminCookie   = "cf_chat_admin"
	adminTTL      = 12 * time.Hour
	agentOnline   = 2 * time.Minute
	maxTextLength = 2000
)

// ChatService wires the visitor widget, the agent console and the store.
type ChatService struct {
	store       *ChatStore
	smtp        smtpConfig
	origins     map[string]bool
	adminPass   string
	cookieKey   []byte
	lastAgentTS atomic.Int64
	notified    sync.Map // session id -> int64
	limitMu     sync.Mutex
	limits      map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

func newChatService(store *ChatStore, smtp smtpConfig) *ChatService {
	s := &ChatService{
		store:     store,
		smtp:      smtp,
		origins:   map[string]bool{},
		adminPass: env("CHAT_ADMIN_PASSWORD", ""),
		cookieKey: []byte(env("CHAT_COOKIE_KEY", "")),
		limits:    make(map[string]*bucket),
	}
	for _, o := range strings.Split(env("CHAT_ALLOWED_ORIGINS", "https://lanhc.com https://www.lanhc.com http://localhost:1313"), " ") {
		if o = strings.TrimSpace(o); o != "" {
			s.origins[o] = true
		}
	}
	if len(s.cookieKey) == 0 {
		s.cookieKey = []byte(newChatToken())
		log.Println("chat: CHAT_COOKIE_KEY not set, admin sessions expire on restart")
	}
	if s.adminPass == "" {
		log.Println("chat: CHAT_ADMIN_PASSWORD not set, admin console is disabled")
	}
	return s
}

func (s *ChatService) Register(mux *http.ServeMux) {
	sub, err := fs.Sub(chatUIFS, "chatui")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /chat/widget.js", chatAsset(sub, "widget.js", "application/javascript; charset=utf-8"))
	mux.Handle("GET /chat/widget", chatAsset(sub, "widget.html", "text/html; charset=utf-8"))
	mux.Handle("GET /chat/admin", chatAsset(sub, "admin.html", "text/html; charset=utf-8"))

	mux.HandleFunc("/chat/api/session", s.handleSession)
	mux.HandleFunc("/chat/api/poll", s.handlePoll)
	mux.HandleFunc("/chat/api/message", s.handleVisitorMessage)
	mux.HandleFunc("/chat/api/email", s.handleVisitorEmail)

	mux.HandleFunc("/chat/api/admin/login", s.handleAdminLogin)
	mux.HandleFunc("/chat/api/admin/logout", s.handleAdminLogout)
	mux.HandleFunc("/chat/api/admin/sessions", s.withAdmin(s.handleAdminSessions))
	mux.HandleFunc("/chat/api/admin/messages", s.withAdmin(s.handleAdminMessages))
	mux.HandleFunc("/chat/api/admin/reply", s.withAdmin(s.handleAdminReply))
	mux.HandleFunc("/chat/api/admin/read", s.withAdmin(s.handleAdminRead))
	mux.HandleFunc("/chat/api/admin/close", s.withAdmin(s.handleAdminClose))
}

func chatAsset(fsys fs.FS, name, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	})
}

// ---------- helpers ----------

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

func (s *ChatService) allow(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	if s.origins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
}

func (s *ChatService) rateLimited(key string, limit int, window time.Duration) bool {
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
		s.limits[key] = &bucket{count: 1, reset: now.Add(window)}
		return false
	}
	b.count++
	return b.count > limit
}

func (s *ChatService) agentIsOnline() bool {
	last := s.lastAgentTS.Load()
	return last > 0 && time.Since(time.Unix(last, 0)) < agentOnline
}

// ---------- visitor API ----------

type sessionRequest struct {
	Token string `json:"token"`
	Page  string `json:"page"`
}

func (s *ChatService) handleSession(w http.ResponseWriter, r *http.Request) {
	s.allow(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req sessionRequest
	if !readJSON(w, r, &req) {
		return
	}

	if validChatToken(req.Token) {
		if sess, ok := s.store.Session(req.Token); ok {
			s.store.Touch(sess.ID)
			s.respondSession(w, sess, 0)
			return
		}
	}
	if s.rateLimited("session:"+clientIP(r), 20, 10*time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many sessions"})
		return
	}
	page := strings.TrimSpace(req.Page)
	if len(page) > 300 {
		page = page[:300]
	}
	sess := s.store.CreateSession(page, trimLen(r.UserAgent(), 300), clientIP(r))
	s.respondSession(w, *sess, 0)
}

func (s *ChatService) respondSession(w http.ResponseWriter, sess ChatSession, since int64) {
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        sess.ID,
		"closed":       sess.Closed,
		"agent_online": s.agentIsOnline(),
		"server_time":  time.Now().Unix(),
		"messages":     s.store.MessagesSince(sess.ID, since),
	})
}

func (s *ChatService) handlePoll(w http.ResponseWriter, r *http.Request) {
	s.allow(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	token := r.URL.Query().Get("token")
	if !validChatToken(token) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad token"})
		return
	}
	sess, ok := s.store.Session(token)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	s.store.Touch(token)
	writeJSON(w, http.StatusOK, map[string]any{
		"closed":       sess.Closed,
		"agent_online": s.agentIsOnline(),
		"server_time":  time.Now().Unix(),
		"messages":     s.store.MessagesSince(token, atoiOr(r.URL.Query().Get("since"), 0)),
	})
}

type visitorMessage struct {
	Token string `json:"token"`
	Text  string `json:"text"`
}

func (s *ChatService) handleVisitorMessage(w http.ResponseWriter, r *http.Request) {
	s.allow(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req visitorMessage
	if !readJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty message"})
		return
	}
	if len(text) > maxTextLength {
		text = text[:maxTextLength]
	}
	if !validChatToken(req.Token) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad token"})
		return
	}
	if s.rateLimited("msg:"+req.Token, 20, time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many messages"})
		return
	}
	msg, ok := s.store.AddMessage(req.Token, "visitor", "", text)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	s.maybeNotify(req.Token, text)
	writeJSON(w, http.StatusOK, map[string]any{"message": msg, "agent_online": s.agentIsOnline()})
}

func (s *ChatService) maybeNotify(sessionID, text string) {
	if s.agentIsOnline() || s.smtp.User == "" {
		return
	}
	now := time.Now().Unix()
	if last, ok := s.notified.Load(sessionID); ok {
		if ts, _ := last.(int64); now-ts < 300 {
			return
		}
	}
	s.notified.Store(sessionID, now)

	sess, _ := s.store.Session(sessionID)
	go func() {
		body := fmt.Sprintf("<h2>网站在线客服新消息</h2><p><strong>会话：</strong>%s</p><p><strong>页面：</strong>%s</p><p><strong>IP：</strong>%s</p><p><strong>内容：</strong></p><p>%s</p><p>回复请打开 <a href=\"https://cf.lanhc.com/chat/admin\">客服工作台</a></p>",
			html.EscapeString(sessionID), html.EscapeString(sess.Page), html.EscapeString(sess.IP), html.EscapeString(text))
		if err := sendMail(s.smtp, buildRawMessage(s.smtp, "官网在线客服新消息", body)); err != nil {
			log.Println("chat: notify mail failed:", err)
		}
	}()
}

type visitorEmail struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (s *ChatService) handleVisitorEmail(w http.ResponseWriter, r *http.Request) {
	s.allow(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req visitorEmail
	if !readJSON(w, r, &req) {
		return
	}
	if !validChatToken(req.Token) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad token"})
		return
	}
	name := trimLen(strings.TrimSpace(req.Name), 80)
	email := trimLen(strings.TrimSpace(req.Email), 200)
	if !strings.Contains(email, "@") || len(email) < 5 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if s.rateLimited("email:"+req.Token, 3, 30*time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
		return
	}
	if !s.store.SetContact(req.Token, name, email) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func trimLen(v string, max int) string {
	if len(v) > max {
		return v[:max]
	}
	return v
}

// ---------- admin auth ----------

func (s *ChatService) sign(exp string) string {
	mac := hmac.New(sha256.New, s.cookieKey)
	mac.Write([]byte(exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ChatService) newAdminToken() string {
	exp := strconv.FormatInt(time.Now().Add(adminTTL).Unix(), 10)
	return exp + "." + s.sign(exp)
}

func (s *ChatService) adminAuthed(r *http.Request) bool {
	c, err := r.Cookie(adminCookie)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	if !hmac.Equal([]byte(parts[1]), []byte(s.sign(parts[0]))) {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	return err == nil && time.Now().Unix() < exp
}

func (s *ChatService) withAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthed(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		s.lastAgentTS.Store(time.Now().Unix())
		next(w, r)
	}
}

func (s *ChatService) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.adminPass == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "admin disabled"})
		return
	}
	if s.rateLimited("login:"+clientIP(r), 10, 10*time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.adminPass)) != 1 {
		time.Sleep(300 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong password"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    s.newAdminToken(),
		Path:     "/chat",
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(adminTTL.Seconds()),
	})
	s.lastAgentTS.Store(time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *ChatService) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/chat", HttpOnly: true, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- admin API ----------

func (s *ChatService) handleAdminSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	includeClosed := r.URL.Query().Get("all") == "1"
	list := s.store.ListSessions(includeClosed, 200)
	if list == nil {
		list = []SessionView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions":     list,
		"agent_online": true,
		"server_time":  time.Now().Unix(),
	})
}

func (s *ChatService) handleAdminMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id := r.URL.Query().Get("id")
	if !validChatToken(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	sess, ok := s.store.Session(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	msgs := s.store.MessagesSince(id, atoiOr(r.URL.Query().Get("since"), 0))
	if len(msgs) > 0 {
		s.store.MarkRead(id, msgs[len(msgs)-1].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":  sess,
		"messages": msgs,
	})
}

func (s *ChatService) handleAdminReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" || !validChatToken(req.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if len(text) > maxTextLength {
		text = text[:maxTextLength]
	}
	msg, ok := s.store.AddMessage(req.ID, "agent", "", text)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func (s *ChatService) handleAdminRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID   string `json:"id"`
		UpTo int64  `json:"up_to"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if validChatToken(req.ID) {
		s.store.MarkRead(req.ID, req.UpTo)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *ChatService) handleAdminClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		ID     string `json:"id"`
		Closed bool   `json:"closed"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !validChatToken(req.ID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if !s.store.SetClosed(req.ID, req.Closed) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
