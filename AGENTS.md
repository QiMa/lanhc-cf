# AGENTS.md — lanhc-cf

## 定位
`cf.lanhc.com` 表单后端：接收站点联系表单/下载页订阅表单 POST，经腾讯企业邮
SMTP 发信，返回静态结果页。

## 约定
- 分支 `main`，远程 `origin` = `git@github.com:QiMa/lanhc-cf.git`。
- Go 项目：`go build ./...` 验证；无内置密钥，SMTP 凭据全部走环境变量
  （`SMTP_HOST/PORT/USER/PASS`、`MAIL_TO`、`LISTEN_ADDR`），不提交任何密钥。
- 接口：`POST /` 收表单，`GET /src/*` 静态资源；成功/失败页在 `src/`。
- 与 `lanhc-hugo` 表单提交配合：表单来源字段由 hugo 透传。

## 关系
- 前端站点 `lanhc-hugo`（`lanhc.com`），本服务是其表单/订阅后端。
