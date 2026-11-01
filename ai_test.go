package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNeedsHandoff(t *testing.T) {
	cases := map[string]bool{
		"你们支持私有化部署吗？":                false,
		"Windows 客户端怎么安装":            false,
		"huayu-v2 和 flash 有什么区别":     false,
		"这个多少钱":                      true,
		"能报个价吗":                      true,
		"合同可以按年签吗":                   true,
		"需要开增值税发票":                   true,
		"能不能便宜点，有没有折扣":               true,
		"this is a LICENSE question": true,
	}
	for text, want := range cases {
		if got := needsHandoff(text); got != want {
			t.Errorf("needsHandoff(%q) = %v, want %v", text, got, want)
		}
	}
}

// newFakeUpstream is a minimal OpenAI-compatible gateway returning body.
func newFakeUpstream(t *testing.T, status int, body string, check func(aiRequest)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization header = %q", got)
		}
		var req aiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q", req.Model)
		}
		if check != nil {
			check(req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newAITestService(t *testing.T, upstream string) (*ChatStore, *ChatService) {
	t.Helper()
	t.Setenv("CHAT_ADMIN_PASSWORD", "test-pass")
	t.Setenv("CHAT_COOKIE_KEY", "test-cookie-key")
	t.Setenv("CHAT_AI_BASE_URL", upstream)
	t.Setenv("CHAT_AI_KEY", "test-key")
	t.Setenv("CHAT_AI_MODEL", "test-model")

	store, err := OpenChatStore(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.file.Close() })

	svc := newChatService(store, smtpConfig{})
	if !svc.ai.enabled() {
		t.Fatal("ai client should be enabled")
	}
	return store, svc
}

func waitForAIMessage(t *testing.T, store *ChatStore, sessionID string, timeout time.Duration) ChatMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, m := range store.MessagesSince(sessionID, 0) {
			if m.Role == "ai" {
				return m
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no ai message within %s", timeout)
	return ChatMessage{}
}

func TestAIAnswersPreSalesQuestion(t *testing.T) {
	var got aiRequest
	srv := newFakeUpstream(t, http.StatusOK,
		`{"choices":[{"message":{"role":"assistant","content":"支持，可私有化部署，数据不出内网。"}}]}`,
		func(req aiRequest) { got = req })
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("https://lanhc.com/faq/", "ua", "1.2.3.4")
	store.AddMessage(sess.ID, "visitor", "", "你们支持私有化部署吗？")
	svc.startAIReply(sess.ID, "你们支持私有化部署吗？")

	msg := waitForAIMessage(t, store, sess.ID, 3*time.Second)
	if !strings.Contains(msg.Text, "私有化") {
		t.Fatalf("unexpected ai text: %q", msg.Text)
	}
	if len(got.Messages) < 2 {
		t.Fatalf("expected system + user messages, got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "知识库") {
		t.Fatal("system prompt missing knowledge base")
	}
	if got.Messages[len(got.Messages)-1].Content != "你们支持私有化部署吗？" {
		t.Fatalf("last message = %q", got.Messages[len(got.Messages)-1].Content)
	}

	updated, _ := store.Session(sess.ID)
	if updated.AIReplies != 1 {
		t.Fatalf("ai_replies = %d, want 1", updated.AIReplies)
	}
	if updated.AITyping {
		t.Fatal("ai_typing should be cleared after the reply")
	}
}

func TestPricingQuestionSkipsModel(t *testing.T) {
	called := false
	srv := newFakeUpstream(t, http.StatusOK, `{"choices":[{"message":{"content":"x"}}]}`, func(aiRequest) { called = true })
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("", "", "")
	svc.startAIReply(sess.ID, "私有化部署一年多少钱？")

	updated, _ := store.Session(sess.ID)
	if !updated.NeedHuman {
		t.Fatal("commercial question must be flagged for a human")
	}
	msgs := store.MessagesSince(sess.ID, 0)
	last := msgs[len(msgs)-1]
	if last.Role != "system" || !strings.Contains(last.Text, "人工") {
		t.Fatalf("expected handoff notice, got %+v", last)
	}
	time.Sleep(150 * time.Millisecond)
	if called {
		t.Fatal("model must not be called for pricing questions")
	}
}

func TestAIHandsOffWhenModelRefuses(t *testing.T) {
	srv := newFakeUpstream(t, http.StatusOK,
		`{"choices":[{"message":{"content":"[[HANDOFF]]"}}]}`, nil)
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("", "", "")
	svc.startAIReply(sess.ID, "你们公司在哪个城市有交付团队？")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := store.Session(sess.ID); s.NeedHuman {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s, _ := store.Session(sess.ID); !s.NeedHuman {
		t.Fatal("handoff marker should escalate to a human")
	}
	if s, _ := store.Session(sess.ID); s.AIReplies != 0 {
		t.Fatalf("handoff should not count as an ai reply, got %d", s.AIReplies)
	}
}

func TestAIStopsWhenAgentOnline(t *testing.T) {
	called := false
	srv := newFakeUpstream(t, http.StatusOK, `{"choices":[{"message":{"content":"hi"}}]}`, func(aiRequest) { called = true })
	store, svc := newAITestService(t, srv.URL)
	svc.lastAgentTS.Store(time.Now().Unix())

	sess := store.CreateSession("", "", "")
	svc.startAIReply(sess.ID, "你们支持私有化部署吗？")
	time.Sleep(200 * time.Millisecond)
	if called {
		t.Fatal("model must not answer while a human agent is online")
	}
}

func TestAIReplyCap(t *testing.T) {
	srv := newFakeUpstream(t, http.StatusOK, `{"choices":[{"message":{"content":"答"}}]}`, nil)
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("", "", "")
	store.UpdateSession(sess.ID, func(s *ChatSession) { s.AIReplies = maxAIReply })
	svc.startAIReply(sess.ID, "你们支持私有化部署吗？")
	time.Sleep(200 * time.Millisecond)
	msgs := store.MessagesSince(sess.ID, 0)
	for _, m := range msgs {
		if m.Role == "ai" {
			t.Fatal("ai should stop after the reply cap")
		}
	}
}

func TestAIStaysOutAfterHandoff(t *testing.T) {
	called := false
	srv := newFakeUpstream(t, http.StatusOK, `{"choices":[{"message":{"content":"答"}}]}`, func(aiRequest) { called = true })
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("", "", "")
	store.UpdateSession(sess.ID, func(s *ChatSession) { s.NeedHuman = true })
	svc.startAIReply(sess.ID, "你们支持私有化部署吗？")
	time.Sleep(200 * time.Millisecond)
	if called {
		t.Fatal("ai must stay out of a conversation already handed to a human")
	}
}

func TestAIUpstreamErrorIsQuiet(t *testing.T) {
	srv := newFakeUpstream(t, http.StatusInternalServerError, `{"error":{"message":"boom"}}`, nil)
	store, svc := newAITestService(t, srv.URL)

	sess := store.CreateSession("", "", "")
	svc.startAIReply(sess.ID, "你们支持私有化部署吗？")
	time.Sleep(300 * time.Millisecond)

	updated, _ := store.Session(sess.ID)
	if updated.AITyping {
		t.Fatal("typing flag must be cleared on upstream failure")
	}
	for _, m := range store.MessagesSince(sess.ID, 0) {
		if m.Role == "ai" {
			t.Fatal("no ai message should be written on failure")
		}
	}
}
