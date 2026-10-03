# lanhc-cf

`cf.lanhc.com` 表单后端。接收站点联系表单与下载页订阅表单的 POST 请求，通过腾讯企业邮 SMTP 将内容发送到指定邮箱，并返回静态结果页。

## 接口

- `POST /` — 表单提交
  - 字段：`email`（必填）、`name`（必填）、`message`（必填）、`subject`（可选）、`platform`（可选，下载页使用）
  - 成功：渲染 `src/success.html`
  - 发信失败：渲染 `src/faild.html`
  - 非 POST 请求：渲染 `src/error.html`
- `GET /src/*` — 静态资源（样式与脚本）

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

## 构建

本地已安装 Go 时：

```bash
CGO_ENABLED=0 go build -o bin/main .
docker build -t ccr.ccs.tencentyun.com/lucky/lanhccf:<version> .
docker push ccr.ccs.tencentyun.com/lucky/lanhccf:<version>
```

部署在 `/lucky/docker-compose.override.yml` 中设置镜像版本与环境变量，然后 `docker compose up -d lanhc-cf`。

## 依赖

仅使用 Go 标准库，无第三方依赖。
