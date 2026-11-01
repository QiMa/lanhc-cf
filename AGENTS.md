# AGENTS.md — lanhc-cf

## 定位
`cf.lanhc.com` 表单后端 + 官网在线客服：接收站点联系表单/下载页订阅表单 POST，
经腾讯企业邮 SMTP 发信，返回静态结果页；同时提供访客聊天挂件与客服工作台。

## 约定
- 分支 `main`，远程 `origin` = `git@github.com:QiMa/lanhc-cf.git`。
- Go 项目：`go build ./...` 验证；无内置密钥，SMTP 凭据全部走环境变量
  （`SMTP_HOST/PORT/USER/PASS`、`MAIL_TO`、`LISTEN_ADDR`），不提交任何密钥。
- 接口：`POST /` 收表单，`GET /src/*` 静态资源；成功/失败页在 `src/`。
  客服接口全部挂在 `/chat/*`，前端资源以 `//go:embed chatui` 编进二进制。
- 客服消息落盘在 `CHAT_DATA_DIR/chat.jsonl`（容器内 `/app/data`，必须挂持久卷），
  部署时需注入 `CHAT_ADMIN_PASSWORD`、`CHAT_COOKIE_KEY`、`CHAT_ALLOWED_ORIGINS`。
- AI 兜底走 `CHAT_AI_*` 环境变量，默认调用同网络的 `airgap:8080`；话术事实源是
  `chatui/kb.md`，改知识只改这个文件。价格/合同类问题必须转人工，不要放开关键词拦截。
- 与 `lanhc-hugo` 表单提交配合：表单来源字段由 hugo 透传。

## 关系
- 前端站点 `lanhc-hugo`（`lanhc.com`），本服务是其表单/订阅后端。
