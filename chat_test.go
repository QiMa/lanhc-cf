package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestChat(t *testing.T) (*ChatStore, *ChatService, *http.ServeMux) {
	t.Helper()
	t.Setenv("CHAT_ADMIN_PASSWORD", "test-pass")
	t.Setenv("CHAT_COOKIE_KEY", "test-cookie-key")
	dir := t.TempDir()
	store, err := OpenChatStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if store.file != nil {
			store.file.Close()
		}
	})
	chat := newChatService(store, smtpConfig{})

	mux := http.NewServeMux()
	chat.Register(mux)
	return store, chat, mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any, cookies ...*http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://lanhc.com")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	out := map[string]any{}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

func TestVisitorAgentConversation(t *testing.T) {
	store, _, mux := newTestChat(t)

	rec, body := doJSON(t, mux, http.MethodPost, "/chat/api/session", map[string]string{"page": "https://lanhc.com/pricing/"})
	if rec.Code != http.StatusOK {
		t.Fatalf("session status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://lanhc.com" {
		t.Fatalf("CORS header = %q", got)
	}
	token, _ := body["token"].(string)
	if !validChatToken(token) {
		t.Fatalf("bad token %q", token)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 greeting, got %d", len(msgs))
	}

	rec, body = doJSON(t, mux, http.MethodPost, "/chat/api/message", map[string]string{"token": token, "text": "  价格怎么算？  "})
	if rec.Code != http.StatusOK {
		t.Fatalf("message status = %d body=%s", rec.Code, rec.Body.String())
	}
	if msg, _ := body["message"].(map[string]any); msg["text"] != "价格怎么算？" {
		t.Fatalf("message not trimmed: %v", body["message"])
	}

	loginRec, _ := doJSON(t, mux, http.MethodPost, "/chat/api/admin/login", map[string]string{"password": "test-pass"})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginRec.Code)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login produced no cookie")
	}

	rec, _ = doJSON(t, mux, http.MethodGet, "/chat/api/admin/sessions", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessions without cookie = %d, want 401", rec.Code)
	}

	rec, body = doJSON(t, mux, http.MethodGet, "/chat/api/admin/sessions", nil, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions status = %d body=%s", rec.Code, rec.Body.String())
	}
	list, _ := body["sessions"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
	if unread, _ := list[0].(map[string]any)["unread"].(float64); unread != 1 {
		t.Fatalf("unread = %v, want 1", unread)
	}

	rec, body = doJSON(t, mux, http.MethodPost, "/chat/api/admin/reply", map[string]string{"id": token, "text": "按年订阅，详情见报价页。"}, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("reply status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec, body = doJSON(t, mux, http.MethodGet, "/chat/api/poll?token="+token+"&since=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d", rec.Code)
	}
	polled, _ := body["messages"].([]any)
	if len(polled) != 2 {
		t.Fatalf("visitor poll got %d messages, want 2", len(polled))
	}
	if role, _ := polled[1].(map[string]any)["role"].(string); role != "agent" {
		t.Fatalf("second message role = %q", role)
	}

	rec, _ = doJSON(t, mux, http.MethodGet, "/chat/api/admin/messages?id="+token, nil, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin messages status = %d", rec.Code)
	}
	if sess, ok := store.Session(token); !ok || sess.AgentRead == 0 {
		t.Fatalf("agent read cursor not advanced: %+v", sess)
	}
}

func TestContactAndClose(t *testing.T) {
	_, _, mux := newTestChat(t)
	_, body := doJSON(t, mux, http.MethodPost, "/chat/api/session", map[string]string{})
	token, _ := body["token"].(string)

	rec, _ := doJSON(t, mux, http.MethodPost, "/chat/api/email", map[string]string{"token": token, "email": "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid email status = %d, want 400", rec.Code)
	}
	rec, _ = doJSON(t, mux, http.MethodPost, "/chat/api/email", map[string]string{"token": token, "name": "李工", "email": "li@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("contact status = %d", rec.Code)
	}

	loginRec, _ := doJSON(t, mux, http.MethodPost, "/chat/api/admin/login", map[string]string{"password": "test-pass"})
	cookies := loginRec.Result().Cookies()
	rec, _ = doJSON(t, mux, http.MethodPost, "/chat/api/admin/close", map[string]bool{"closed": true}, cookies...)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("close without id should be 400, got %d", rec.Code)
	}
	rec, _ = doJSON(t, mux, http.MethodPost, "/chat/api/admin/close", map[string]any{"id": token, "closed": true}, cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("close status = %d", rec.Code)
	}
	_, body = doJSON(t, mux, http.MethodGet, "/chat/api/poll?token="+token, nil)
	if closed, _ := body["closed"].(bool); !closed {
		t.Fatal("visitor does not see closed session")
	}
}

func TestAdminPasswordRequired(t *testing.T) {
	t.Setenv("CHAT_ADMIN_PASSWORD", "")
	dir := t.TempDir()
	store, err := OpenChatStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.file.Close()
	chat := newChatService(store, smtpConfig{})
	chat.adminPass = ""

	mux := http.NewServeMux()
	chat.Register(mux)
	rec, _ := doJSON(t, mux, http.MethodPost, "/chat/api/admin/login", map[string]string{"password": ""})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login without configured password = %d, want 503", rec.Code)
	}
}

func TestStoreReplay(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenChatStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := store.CreateSession("https://lanhc.com/", "ua", "1.2.3.4")
	store.AddMessage(sess.ID, "visitor", "", "hello")
	store.AddMessage(sess.ID, "agent", "", "hi")
	store.SetContact(sess.ID, "李工", "li@example.com")
	store.file.Close()

	reopened, err := OpenChatStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.file.Close()
	got, ok := reopened.Session(sess.ID)
	if !ok {
		t.Fatal("session lost after replay")
	}
	if got.Email != "li@example.com" {
		t.Fatalf("email = %q", got.Email)
	}
	msgs := reopened.MessagesSince(sess.ID, 0)
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	if _, err := os.Stat(filepath.Join(dir, "chat.jsonl")); err != nil {
		t.Fatalf("journal missing: %v", err)
	}
}

func TestAssetRoutes(t *testing.T) {
	_, _, mux := newTestChat(t)
	for _, path := range []string{"/chat/widget.js", "/chat/admin", "/chat/widget"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s returned empty body", path)
		}
	}
}
