package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// aiClient calls the model gateway that lives on the same docker network as this
// service. It is optional: with no base URL or key configured the chat simply
// stays human-only.
type aiClient struct {
	baseURL string
	key     string
	model   string
	http    *http.Client
	kb      string
}

func newAIClient(kb string) *aiClient {
	base := strings.TrimRight(env("CHAT_AI_BASE_URL", ""), "/")
	key := env("CHAT_AI_KEY", "")
	c := &aiClient{
		baseURL: base,
		key:     key,
		model:   env("CHAT_AI_MODEL", "huayu-hermes-flash"),
		http:    &http.Client{Timeout: 45 * time.Second},
		kb:      kb,
	}
	if c.enabled() {
		log.Println("chat ai: enabled, model:", c.model, "upstream:", c.baseURL)
	} else {
		log.Println("chat ai: disabled (CHAT_AI_BASE_URL / CHAT_AI_KEY not set)")
	}
	return c
}

func (c *aiClient) enabled() bool {
	return c != nil && c.baseURL != "" && c.key != ""
}

const aiSystemPrompt = `你是清大华宇AI官网（lanhc.com）的在线客服助手，用简体中文回答访客的产品咨询。

规则：
1. 只依据下面「知识库」中的事实回答；不要编造价格、参数、客户案例、时间承诺。
2. 回答控制在 3 句话以内，语气专业、友好，可适当给出站内链接。
3. 遇到价格、报价、合同、付款、发票、采购、招标，或知识库没有覆盖的问题，
   不要猜测，直接只输出标记：[[HANDOFF]]
4. 不要提及自己是 AI 模型或"知识库"，以"我们"的口吻作答。
5. 不要输出 Markdown 表格或代码块。

知识库：
`

// handoffMarker is emitted by the model when the question is out of scope.
const handoffMarker = "[[HANDOFF]]"

// handoffKeywords let us escalate deterministically without waiting for a model
// round trip: pricing/commercial questions must never be answered by the bot.
var handoffKeywords = []string{
	"价格", "报价", "报个价", "多少钱", "费用", "收费", "计费", "折扣", "优惠", "预算",
	"合同", "条款", "付款", "支付", "发票", "采购", "招标", "投标", "商务",
	"授权费", "license", "免费试用", "退款",
}

func needsHandoff(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range handoffKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

type aiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiRequest struct {
	Model       string      `json:"model"`
	Messages    []aiMessage `json:"messages"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature float64     `json:"temperature"`
}

type aiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// reply asks the model for an answer given the transcript so far.
func (c *aiClient) reply(history []ChatMessage) (string, error) {
	msgs := []aiMessage{{Role: "system", Content: aiSystemPrompt + c.kb}}
	start := 0
	if len(history) > 12 {
		start = len(history) - 12
	}
	for _, m := range history[start:] {
		switch m.Role {
		case "visitor":
			msgs = append(msgs, aiMessage{Role: "user", Content: m.Text})
		case "agent", "ai":
			msgs = append(msgs, aiMessage{Role: "assistant", Content: m.Text})
		}
	}

	body, err := json.Marshal(aiRequest{
		Model:       c.model,
		Messages:    msgs,
		MaxTokens:   400,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out aiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return "", fmt.Errorf("model gateway: %s", out.Error.Message)
		}
		return "", fmt.Errorf("model gateway: http %d", resp.StatusCode)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("model gateway returned no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
