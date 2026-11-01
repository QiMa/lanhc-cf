package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	maxMessagesPerSession = 2000
	maxStoredMessages     = 50000
)

// ChatMessage is a single message inside one conversation.
type ChatMessage struct {
	ID      int64  `json:"id"`
	Session string `json:"session"`
	Role    string `json:"role"` // visitor | agent | system
	Name    string `json:"name,omitempty"`
	Text    string `json:"text"`
	TS      int64  `json:"ts"`
}

// ChatSession tracks one visitor conversation.
type ChatSession struct {
	ID          string `json:"id"`
	Created     int64  `json:"created"`
	LastSeen    int64  `json:"last_seen"`
	LastVisitor int64  `json:"last_visitor"`
	LastAgent   int64  `json:"last_agent"`
	AgentRead   int64  `json:"agent_read"`
	Page        string `json:"page,omitempty"`
	UA          string `json:"ua,omitempty"`
	IP          string `json:"ip,omitempty"`
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	Closed      bool   `json:"closed,omitempty"`
}

type chatRecord struct {
	Type    string       `json:"type"`
	Session *ChatSession `json:"session,omitempty"`
	Message *ChatMessage `json:"message,omitempty"`
}

// ChatStore keeps conversations in an append-only JSONL file plus an in-memory
// index. It only uses the standard library, matching the rest of this service.
type ChatStore struct {
	mu       sync.RWMutex
	path     string
	file     *os.File
	sessions map[string]*ChatSession
	messages []ChatMessage
	nextID   int64
}

func newChatToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func validChatToken(v string) bool {
	if len(v) != 32 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

// OpenChatStore loads the journal, replays it, then rewrites a compacted copy.
func OpenChatStore(dir string) (*ChatStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &ChatStore{
		path:     filepath.Join(dir, "chat.jsonl"),
		sessions: make(map[string]*ChatSession),
		nextID:   1,
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	if err := s.compact(); err != nil {
		return nil, err
	}
	log.Printf("chat store: %d sessions, %d messages, journal %s", len(s.sessions), len(s.messages), s.path)
	return s, nil
}

func (s *ChatStore) replay() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec chatRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			log.Println("chat store: skipping corrupt journal line:", err)
			continue
		}
		switch rec.Type {
		case "session":
			if rec.Session != nil && rec.Session.ID != "" {
				cp := *rec.Session
				s.sessions[cp.ID] = &cp
			}
		case "message":
			if rec.Message != nil && rec.Message.ID > 0 {
				s.messages = append(s.messages, *rec.Message)
				if rec.Message.ID >= s.nextID {
					s.nextID = rec.Message.ID + 1
				}
			}
		}
	}
	return scanner.Err()
}

func (s *ChatStore) compact() error {
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)

	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sess := s.sessions[id]
		if err := enc.Encode(chatRecord{Type: "session", Session: sess}); err != nil {
			f.Close()
			return err
		}
	}
	for i := range s.messages {
		msg := s.messages[i]
		if err := enc.Encode(chatRecord{Type: "message", Message: &msg}); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}

	appendFile, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	s.file = appendFile
	return nil
}

func (s *ChatStore) appendRecord(rec chatRecord) {
	if s.file == nil {
		return
	}
	data, err := json.Marshal(rec)
	if err != nil {
		log.Println("chat store: marshal failed:", err)
		return
	}
	data = append(data, '\n')
	if _, err := s.file.Write(data); err != nil {
		log.Println("chat store: append failed:", err)
	}
}

func (s *ChatStore) saveSessionLocked(sess *ChatSession) {
	s.appendRecord(chatRecord{Type: "session", Session: sess})
}

// CreateSession registers a new visitor conversation.
func (s *ChatStore) CreateSession(page, ua, ip string) *ChatSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	sess := &ChatSession{
		ID:          newChatToken(),
		Created:     now,
		LastSeen:    now,
		LastVisitor: now,
		Page:        page,
		UA:          ua,
		IP:          ip,
	}
	s.sessions[sess.ID] = sess
	s.saveSessionLocked(sess)

	msg := ChatMessage{
		ID:      s.nextID,
		Session: sess.ID,
		Role:    "system",
		Text:    "您好，这里是清大华宇AI 在线支持。请描述您的问题，我们会尽快回复。",
		TS:      now,
	}
	s.nextID++
	s.messages = append(s.messages, msg)
	s.appendRecord(chatRecord{Type: "message", Message: &msg})
	s.trimLocked()
	return sess
}

func (s *ChatStore) Session(id string) (ChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return ChatSession{}, false
	}
	return *sess, true
}

// Touch marks the visitor as online without persisting on every poll.
func (s *ChatStore) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		sess.LastSeen = time.Now().Unix()
	}
}

func (s *ChatStore) SetContact(id, name, email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return false
	}
	sess.Name = name
	sess.Email = email
	s.saveSessionLocked(sess)
	return true
}

// AddMessage appends a message and returns it.
func (s *ChatStore) AddMessage(sessionID, role, name, text string) (ChatMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return ChatMessage{}, false
	}
	now := time.Now().Unix()
	msg := ChatMessage{
		ID:      s.nextID,
		Session: sessionID,
		Role:    role,
		Name:    name,
		Text:    text,
		TS:      now,
	}
	s.nextID++
	s.messages = append(s.messages, msg)
	s.appendRecord(chatRecord{Type: "message", Message: &msg})

	sess.LastSeen = now
	if role == "visitor" {
		sess.LastVisitor = now
		sess.Closed = false
	} else if role == "agent" {
		sess.LastAgent = now
		sess.AgentRead = msg.ID
	}
	s.saveSessionLocked(sess)
	s.trimLocked()
	return msg, true
}

func (s *ChatStore) trimLocked() {
	if len(s.messages) <= maxStoredMessages {
		return
	}
	drop := len(s.messages) - maxStoredMessages
	s.messages = append([]ChatMessage(nil), s.messages[drop:]...)
}

// MessagesSince returns messages of one session with ID greater than since.
func (s *ChatStore) MessagesSince(sessionID string, since int64) []ChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChatMessage, 0, 16)
	for i := range s.messages {
		m := s.messages[i]
		if m.Session == sessionID && m.ID > since {
			out = append(out, m)
		}
	}
	return out
}

// MarkRead advances the agent read cursor of a session.
func (s *ChatStore) MarkRead(sessionID string, upTo int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || upTo <= sess.AgentRead {
		return
	}
	sess.AgentRead = upTo
	sess.LastAgent = time.Now().Unix()
	s.saveSessionLocked(sess)
}

func (s *ChatStore) SetClosed(sessionID string, closed bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.Closed = closed
	s.saveSessionLocked(sess)
	return true
}

// SessionView is the admin-facing snapshot of a conversation.
type SessionView struct {
	ChatSession
	Unread int    `json:"unread"`
	Last   string `json:"last,omitempty"`
}

// ListSessions returns conversations ordered by most recent visitor activity.
func (s *ChatStore) ListSessions(includeClosed bool, limit int) []SessionView {
	s.mu.RLock()
	defer s.mu.RUnlock()

	last := make(map[string]string, len(s.sessions))
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if _, ok := last[m.Session]; !ok {
			last[m.Session] = m.Text
		}
	}
	unread := make(map[string]int, 32)
	for i := range s.messages {
		m := s.messages[i]
		sess, ok := s.sessions[m.Session]
		if !ok || m.Role != "visitor" || m.ID <= sess.AgentRead {
			continue
		}
		unread[m.Session]++
	}

	out := make([]SessionView, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sv := SessionView{ChatSession: *sess, Unread: unread[sess.ID], Last: last[sess.ID]}
		if !includeClosed && (sv.Closed || isStaleSession(sv.ChatSession)) {
			continue
		}
		out = append(out, sv)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastVisitor == out[j].LastVisitor {
			return out[i].ID < out[j].ID
		}
		return out[i].LastVisitor > out[j].LastVisitor
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func isStaleSession(sess ChatSession) bool {
	return time.Since(time.Unix(sess.LastSeen, 0)) > 30*24*time.Hour
}

// Strconv helpers kept together for the admin pagination defaults.
func atoiOr(v string, fallback int64) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}
