# sub2api fork 部署记录（测试服务器）

## 环境

| 项 | 值 |
|---|---|
| 主机 | `38.39.30.186`（HK9708812） |
| 系统 | Ubuntu 24.04.1 LTS / x86_64 |
| 资源 | 15 GiB RAM、146 GB 磁盘（13% 已用） |
| Docker | 29.9.2 + Compose v5.6.1（宝塔面板安装） |
| Nginx | 宝塔 nginx 占用 80 端口 |
| 部署目录 | `/opt/sub2api` |

## 部署形态

**本地交叉编译 → SFTP 上传 → Docker 构建运行**（服务器不需要 Go/Node）。

- 本机 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` + `-tags embed` 构建单文件二进制（约 122 MB，前端 `dist` 已 embed）。
- 服务器只 `docker compose build`（仅拉 `alpine:3.20`、`postgres:18-alpine`、`redis:8-alpine`），不拉 golang/node 镜像。

## 目录结构

```
/opt/sub2api/
├── .env                          # 部署密钥（JWT/DB/管理员）
├── Dockerfile                    # 精简运行镜像（alpine + 预编译二进制）
├── docker-compose.yml            # app + postgres + redis
├── docker-entrypoint.sh          # 数据目录权限修正 + 降权运行
├── sub2api                       # 交叉编译二进制
├── resources/model-pricing/      # LiteLLM 价格目录（兜底）
├── data/                         # 持久化（挂载到容器 /app/data）
│   ├── config.yaml               # AUTO_SETUP 生成
│   ├── model-pricing-overrides.json   # 管理端价格覆盖写回此处
│   ├── cny-overrides.json             # 人民币口径文件
│   ├── model_pricing.json        # 运行时下载的价格目录
│   └── logs/
├── pgdata/                       # Postgres 数据
└── redisdata/                    # Redis AOF
```

## 端口

- 外部：`http://38.39.30.186:8081`（容器内 8080）
- 80/443 留给宝塔 nginx；如需域名 + HTTPS，在宝塔建反代指向 `127.0.0.1:8081`

## 凭据（部署时自动生成，建议登录后立即修改）

- 管理员账号由部署时配置，登录凭据通过受控渠道交付。
- 数据库密码、JWT_SECRET 见服务器 `/opt/sub2api/.env`

## 部署过程中遇到的问题与处置

1. **Postgres 18 数据卷挂载点变更**
   `postgres:18-alpine` 拒绝挂载到 `/var/lib/postgresql/data`（新版本按 major 版本分子目录）。改为挂载 `/var/lib/postgresql` 后正常启动。

2. **管理员合规确认拦截**
   首次调用 `/api/v1/admin/*` 返回 `ADMIN_COMPLIANCE_ACK_REQUIRED`。这是上游项目内置的运营合规确认（需接受 `docs/legal/admin-compliance.zh.md`）。处置：调用 `POST /api/v1/admin/compliance/accept` 提交确认短语。
   > 测试环境已代为确认；**正式环境请由实际运营者本人阅读并确认**。

3. **价格覆盖文件缺失告警**
   启动日志出现 `override merge skipped: ... no such file or directory`。因 `PRICING_OVERRIDE_FILE` 指向的文件尚不存在——属正常降级，创建空 `{}` 后消失。

4. **Windows 本地 `ssh` 无法传密码**
   改用 paramiko 脚本（`scripts/deploy/ssh_run.py` / `sftp_upload.py`），凭据只从环境变量读取，不落盘。

## 运维命令

```bash
cd /opt/sub2api
docker compose ps                 # 状态
docker compose logs -f sub2api    # 日志
docker compose restart sub2api    # 重启应用
docker compose down && docker compose up -d   # 全量重启
```

更新版本（本机执行）：

```powershell
# 1. 重新编译
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go -C backend build -tags embed -trimpath -ldflags="-s -w -X main.Version=<ver>" -o "$env:TEMP\sub2api-deploy" ./cmd/server
# 2. 上传 + 重建
$env:SSH_PW="<pw>"; python scripts/deploy/sftp_upload.py "$env:TEMP\sub2api-deploy-pkg" "/opt/sub2api"
# 3. 服务器上
cd /opt/sub2api && docker compose build && docker compose up -d
```

## 冒烟测试结果（2026-11-15）

| 项 | 结果 |
|---|---|
| `/health` | 200 `{"status":"ok"}` |
| 管理员登录 | 成功，返回 access/refresh token |
| 工单表迁移 | `tickets`、`ticket_replies` 已创建 |
| 用户创建工单 | `id=1`，`status=open`、`reply_count=1` |
| 管理员工单列表/统计 | `{open:1,pending:0,resolved:0,closed:0,total:1}` |
| 管理员回复工单 | 成功，状态转 `pending`、`reply_count=2` |
| 模型价格列表 | 269 个模型，`kimi-k2.5` 输入 0.6 / 输出 2.25 USD per Mtok |
| 人民币登记 + direct | `kimi-k2` 输入 = **4.0** USD per Mtok |
| 切回 convert(7.14) | `kimi-k2` 输入 = **0.56022409** USD per Mtok |
| USD 覆盖 gpt-5.4 | 输入 2.5 → **3.0**，`overridden=true` |
| 恢复内置 | 回到 2.5，`overridden=false` |
| 文件落盘 | `cny-overrides.json` / `model-pricing-overrides.json` 内容正确 |
| 首页 / SPA 路由 | `/home`、`/tickets` 均 200 |
| 新配色令牌 | CSS 含 `--ui-canvas`、`4F46E5`、`6366F1`、`070B14` |
| 新功能打包 | JS bundle 含 `tickets`、`model-pricing` 路由串 |
| 外网访问 | `http://38.39.30.186:8081/health` → 200 |

## 已知限制

- SMTP 未配置：管理员回复工单时 `notify_email=true` 会跳过发信（`email_sent=false`），需在管理后台配置 SMTP。
- 未配置 HTTPS：如需域名访问，用宝塔反代 + Let's Encrypt。
- 单实例限流：工单 60s/5 次为进程内计数，多副本部署时各自计数。
- 测试数据：已创建 1 条工单与 gpt-5.4 的临时覆盖（已恢复，`model-pricing-overrides.json` 当前为 `{}`）。
