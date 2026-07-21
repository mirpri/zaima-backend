# 部署指南（Deployment）

本文档说明如何把"在吗"后端部署到一台 Linux 服务器，并打包前端 Android App 连接它。

## 架构与端口

```
Android App ──HTTPS/WSS──▶ Nginx(443) ──▶ 后端 api 容器(8080)
                                            ├─ postgres 容器(5432)
                                            └─ redis 容器(6379)
```

- 后端三件套用 docker-compose 一键起：`postgres` + `redis` + `api`，`api` 监听 **8080**。
- 数据库、Redis 仅供内部访问，生产建议不对公网开放 5432/6379，只暴露 443（经 Nginx）。

---

## 一、后端部署

### 1. 安装 Docker

```bash
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

### 2. 拉取代码

```bash
git clone <zaima-backend 仓库地址> && cd zaima-backend
```

### 3. 配置密钥（环境变量，勿写进 yaml）

```bash
cp .env.example .env
```

编辑 `.env`，至少改这几项：

| 变量 | 说明 |
|---|---|
| `ZAIMA_SERVER_MODE` | 生产填 `release`（`debug` 下短信验证码会直接返回，禁止用于生产） |
| `ZAIMA_JWT_SECRET` | 换成一串足够长的随机字符串 |
| `POSTGRES_PASSWORD` | 数据库密码，postgres 容器与后端共用此变量 |
| `ZAIMA_LLM_API_KEY` | 大模型 Key，留空则 AI 回退规则引擎 |
| `ZAIMA_LLM_BASE_URL` | 大模型接口地址（只到 `/v1`），默认 DeepSeek；换 OpenAI/通义见 `.env.example` |
| `ZAIMA_LLM_MODEL` | 模型名，需与服务商匹配，如 `deepseek-chat` / `gpt-4o-mini` / `qwen-plus` |
| `ZAIMA_STORAGE_PUBLIC_BASE_URL` | 自建存储对外域名，如 `https://api.你的域名.com`；暂无域名可留空 |

> AI 接口地址即大模型的 `base_url`（对应 `configs/config.docker.yaml` 的 `llm.base_url`）。代码会自动在其后拼接 `/chat/completions`，所以只填到 `/v1` 即可。也可直接改 yaml 而不用环境变量。

> `docker compose` 会自动读取本目录的 `.env`。`ZAIMA_*` 前缀变量会覆盖 `configs/config.docker.yaml` 中的同名项。

其他非密钥项（新闻 RSS 源 `news.feeds`、天气地址、CORS 白名单等）直接改 `configs/config.docker.yaml`。天气用的是 Open-Meteo，免费无需 key。

### 4. 启动

```bash
docker compose up -d --build
docker compose ps                     # 三个服务都应 Up / healthy
curl http://localhost:8080/health     # 期望 {"status":"ok","deps":{"postgres":true,"redis":true}}
```

首次启动会自动建表（AutoMigrate），无需手动初始化数据库。上传的头像/录音存放在命名卷 `uploads`（挂载到容器 `/app/data/uploads`），重建容器不会丢失。

### 5. 常用运维命令

```bash
docker compose logs -f api      # 看后端日志
docker compose up -d --build    # 更新代码后重新构建并滚动重启
docker compose down             # 停止 (数据卷保留)
```

---

## 二、HTTPS + 反向代理（强烈建议）

自建文件存储、WebSocket（wss）、以及 Android 发布都要求 HTTPS。用 Nginx + 免费证书（Let's Encrypt）：

```bash
apt install -y nginx certbot python3-certbot-nginx
```

新建 `/etc/nginx/sites-available/zaima`：

```nginx
server {
    server_name api.你的域名.com;
    client_max_body_size 12M;              # 允许上传 (后端限 10M)

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /api/v1/ws {                   # WebSocket 升级
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;
    }
}
```

启用并签发证书：

```bash
ln -s /etc/nginx/sites-available/zaima /etc/nginx/sites-enabled/
certbot --nginx -d api.你的域名.com     # 自动配置 443 + 证书自动续期
```

然后在 `.env` 里把 `ZAIMA_STORAGE_PUBLIC_BASE_URL` 设为 `https://api.你的域名.com`，执行 `docker compose up -d` 重启使其生效。

DNS：把 `api.你的域名.com` 的 A 记录指向服务器公网 IP；安全组/防火墙放行 80、443。

---

## 三、前端 Android App 打包

前端是 Tauri 项目（仓库 `zaima-tauri`）。打包需要在**装有 Android SDK/NDK 的开发机**上进行（不是服务器）。

### 1. 环境准备（开发机）

- Node.js + pnpm、Rust 工具链
- Android Studio（含 SDK、NDK），并设置 `ANDROID_HOME`、`NDK_HOME`
- 首次需初始化 Android 工程：`pnpm tauri android init`（本仓库已生成 `src-tauri/gen/android`）

### 2. 配置后端地址

在 `zaima-tauri` 根目录建 `.env.production`：

```bash
VITE_API_BASE_URL=https://api.你的域名.com/api/v1
```

App 会据此自动推导 WebSocket 地址（https → wss）。App 直连后端，不涉及浏览器跨域，无需配置 CORS。

### 3. 构建 APK

```bash
cd zaima-tauri
pnpm install
pnpm tauri android build --apk
```

产物在 `src-tauri/gen/android/app/build/outputs/apk/`。用你的签名密钥签名后即可分发或上架应用商店。

> 提示：正式发布需在 `src-tauri/tauri.conf.json` 配置应用标识、图标、版本号，并准备 Android 签名 keystore。

---

## 四、上线检查清单

- [ ] `.env` 中 `ZAIMA_SERVER_MODE=release`，`ZAIMA_JWT_SECRET` 与 `POSTGRES_PASSWORD` 已改为强随机值
- [ ] `curl https://api.你的域名.com/health` 返回 `postgres:true, redis:true`
- [ ] HTTPS 生效，`ZAIMA_STORAGE_PUBLIC_BASE_URL` 为 https 域名
- [ ] 已填 `ZAIMA_LLM_API_KEY`（或确认接受规则引擎兜底）
- [ ] `configs/config.docker.yaml` 的 `news.feeds` 已换成实际 RSS 源
- [ ] 数据库/Redis 端口未对公网开放（仅 443 对外）
- [ ] App 内 `VITE_API_BASE_URL` 指向线上域名，实机验证登录/聊天/上传/广场

---

## 五、当前实现的边界（已知未覆盖）

- **短信**：未接入，`release` 模式下 `/auth/sms-code` 不会返回验证码（需接短信网关后才能真实收码）。
- **离线推送**：仅在线推送（App 在线时经 WebSocket 收消息/事件）；离线消息在重新上线后由 `/chat/history?since_id=` 增量补齐。真正的 APNs/FCM 推送需后续接入。
- **语音转文字（STT）**：按规划暂不做，长辈录音以语音消息直接发送。
