# lanhc-cf

`cf.lanhc.com` 表单后端。接收站点联系表单与下载页订阅表单的 POST 请求，通过腾讯企业邮 SMTP 将内容发送到指定邮箱，并返回静态结果页。

同时提供官网在线客服（替代第三方 Crisp）：访客挂件、客服工作台与消息持久化，
全部由本服务自己承担，不依赖外部脚本或 SaaS。

## 接口

- `POST /` — 表单提交
  - 字段：`email`（必填）、`name`（必填）、`message`（必填）、`subject`（可选）、`platform`（可选，下载页使用）
  - 成功：渲染 `src/success.html`
  - 发信失败：渲染 `src/faild.html`
  - 非 POST 请求：渲染 `src/error.html`
- `GET /src/*` — 静态资源（样式与脚本）

## 在线客服

| 路径 | 说明 |
| --- | --- |
| `GET /chat/widget.js` | 访客挂件脚本，站点只需一行 `<script src="https://cf.lanhc.com/chat/widget.js"></script>` |
| `GET /chat/widget` | 独立聊天页（移动端/无 JS 挂件场景） |
| `GET /chat/admin` | 客服工作台（口令登录） |
| `POST /chat/api/session` | 创建或恢复访客会话，返回 `token` |
| `GET /chat/api/poll` | 访客拉取新消息（`since` 为已收到的最大消息 ID） |
| `POST /chat/api/message` | 访客发消息 |
| `POST /chat/api/email` | 访客留邮箱，离线时接收邮件通知 |
| `POST /chat/api/admin/login` | 工作台登录，签发 HttpOnly 会话 Cookie |
| `GET /chat/api/admin/sessions` | 会话列表（`all=1` 含已结束） |
| `GET /chat/api/admin/messages` | 单个会话的消息，并推进已读游标 |
| `POST /chat/api/admin/reply` | 客服回复 |
| `POST /chat/api/admin/close` | 结束/重新打开会话 |

实现要点：

- 消息写入 `CHAT_DATA_DIR/chat.jsonl`（追加式日志，启动时回放并压缩），无数据库依赖。
- 访客与客服均用 3–4 秒轮询；客服在线状态由工作台最近一次调用维护，
  在线时不再触发离线邮件通知。
- 访客侧仅允许白名单 Origin 的跨域请求，写接口按 IP/会话做了限流。

## 配置

通过环境变量注入，无内置密钥：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SMTP_HOST` | `smtp.exmail.qq.com` | SMTP 服务器 |
| `SMTP_PORT` | `465` | 465 走隐式 TLS，其它端口走 STARTTLS |
| `SMTP_USER` | 空 | 发件账号（同时作为 From） |
| `SMTP_PASS` | 空 | 发件账号授权码 |
| `MAIL_TO` | `info@lanhc.com` | 收件地址 |
| `LISTEN_ADDR` | `:80` | 监听地址 |
| `CHAT_DATA_DIR` | `data` | 客服会话日志目录（需挂载持久卷） |
| `CHAT_ADMIN_PASSWORD` | 空 | 客服工作台口令；留空则工作台禁用 |
| `CHAT_COOKIE_KEY` | 随机 | 工作台 Cookie 签名密钥，固定值可在重启后保留登录态 |
| `CHAT_ALLOWED_ORIGINS` | `https://lanhc.com https://www.lanhc.com http://localhost:1313` | 允许调用客服 API 的站点来源 |

## 构建

本地已安装 Go 时：

```bash
CGO_ENABLED=0 go build -o bin/main .
docker build -t ccr.ccs.tencentyun.com/lucky/lanhccf:<version> .
docker push ccr.ccs.tencentyun.com/lucky/lanhccf:<version>
```

在 Windows/macOS 交叉编译 Linux 二进制时：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/main .
```

部署在 `/lucky/docker-compose.override.yml` 中设置镜像版本与环境变量，然后 `docker compose up -d lanhc-cf`。

## 依赖

仅使用 Go 标准库，无第三方依赖。
