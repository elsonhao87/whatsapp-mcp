# WhatsApp tag 自动调查

同事在 WhatsApp 群 tag 我 → bridge 当下侦测到 → 叫 Claude 查问题 → **直接回复到群里**
(开头带 🤖,没有人先审)。调查的结论和发出去的内容会抄一份到我自己的 WhatsApp
(「传讯息给自己」,手机不会为它跳通知)。

**管理页面:<http://127.0.0.1:8765>**(只有这台电脑打得开)。可以勾选要监听的群、
看 WhatsApp 和 Claude 的连线状态、登入 Claude。

基于 [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp) 改的(原版说明见 git 历史)。

## 组成

| 档案 | 作用 |
|---|---|
| `whatsapp-bridge/` | Go 程式,以「已连结装置」连上我的 WhatsApp,把白名单群的讯息存进 `store/messages.db` |
| `whatsapp-bridge/allowed_chats.txt` | **群白名单**,一行一个群 JID。不在里面的聊天不存、也不能发 |
| `whatsapp-mcp-server/` | MCP server,让 Claude 读讯息、发文字、下载图片(已用 `claude mcp add whatsapp` 登记) |
| `whatsapp-bridge/triage.go` | 侦测到 tag 就跑 `claude -p`(只给读档案、搜代码、查 DB 的工具),把回复发回群里 |
| `whatsapp-bridge/web.go` + `web.html` | 管理页面和它的接口(挡掉其他网站发来的请求) |
| `TRIAGE.md` | 给 Claude 的调查说明和输出格式。改这份不用重新编译 |
| `config.json` 的 `answer_all_questions` | 设成 `true`:没 tag 你的讯息也会看(最慢 2 分钟后),是问题才回;不是问题就不回、也不通知你。每条讯息都会叫一次 Claude |
| `CODE_CHANGE.md` + `whatsapp-bridge/codechange.go` | 改代码的流程,`config.json` 的 `code_change_on_go` 设成 `true` 才启用。有人要求改代码时,bot 只在群里贴提案并 tag 你;**你本人在那个群回 `go`** 才会动手(一小时内有效,一个 go 对应最近一个提案)。Claude 在一份临时副本里改档案(没有 shell、没有 git),bridge 自己 commit 并 push 到提案的分支。只会推 `code_branch_prefix` 开头的分支;`code_push_url` 是 git 网址,`{repo}` 会换成 repo 资料夹名。你自己的工作目录不会被动到,本机分支要自己 pull。`code_staging_branch` 有填的话,推完会再 tag 你问要不要把那个 commit cherry-pick 到这个分支,同样是你回 `go` 才推(有冲突就不推,留给你处理) |
| `TRIAGE.local.md`(不进 git,可有可无) | 只属于这台机器的补充说明:有哪些 DB MCP、日志表名、系统名称。有这个档就会接在 `TRIAGE.md` 后面 |
| `config.json` 的 `owner_command_prefix` | 填一个词(例如 `bot`):你本人在监听的群里发「bot 查一下…」,bot 就当成你的指令去做并回你。只认你自己帐号发的;要改代码仍然先出提案、等你回 `go`。留空 = 关闭 |
| `TRIAGE_LIVE_LOG.md` | 第二轮(用 Chrome 查 live ClickHouse 日志)的说明。只有 `config.json` 的 `live_log_via_chrome` 设成 `true` 才会用到,SQL console 的网址填在 `live_log_console_url`:第一轮说要看 live 日志时,bridge 再跑一轮,这一轮可以操作你的 Chrome(要装 Claude in Chrome 扩充、Chrome 开着、ClickHouse 已登入)。每次用到都会在你自己的 WhatsApp 标「🌐 这次用了 Chrome」和跑过的查询 |
| `config.json` | 这台机器的设定:`claude_bin`(claude 指令的路径,留空 = 只收讯息不调查)、`triage_dir`(Claude 在哪个 repo 里调查)、`memory_index`(先读的笔记档,可留空)。改了不用重启 |
| `install.sh` + `launchd/` | macOS:编译并设成开机自动在背景跑 |
| `install.ps1` | Windows:编译并设成登入时自动在背景跑(工作排程器) |

不需要开 Claude 桌面版,也没有排程。Claude 只在有人 tag 我时才被叫起来。
命令行的 `claude` 要保持登入:在终端机跑 `claude`,过期了就 `/login`。

## 在新机器上安装(macOS / Windows)

需要:Go、[uv](https://docs.astral.sh/uv/)、Claude Code 命令行(`claude`,要先 `claude auth login`)。
不需要 C 编译器。

1. 第一次先在终端机手动跑一次,扫 QR 码登入 WhatsApp,看到 Connected 后按 Ctrl+C:
   - macOS:`cd whatsapp-bridge && go build -o whatsapp-bridge . && ./whatsapp-bridge`
   - Windows:`cd whatsapp-bridge; go build -o whatsapp-bridge.exe .; .\whatsapp-bridge.exe`
2. 安装成自动启动:macOS 跑 `./install.sh`,Windows 在 PowerShell 跑 `.\install.ps1`
   (被挡的话先 `Set-ExecutionPolicy -Scope Process Bypass`)。
3. 改 `config.json` 的 `triage_dir`,指到要让 Claude 调查的 repo。
4. 开 <http://127.0.0.1:8765> 勾选要监听的群。
5. Claude 要用的 DB MCP 在那台机器的 Claude Code 里另外设好,再把名字填进 `config.json` 的 `allowed_tools`(逗号分隔,例如 `mcp__mysql-local`);没设也能跑,只是查不到 DB。`denied_tools` 填那台机器上不准它碰的 connector。

**同一个 WhatsApp 帐号只在一台机器上跑。** 两台同时跑,每个 tag 会被回复两次。

## 常用操作

```bash
./install.sh             # 重新编译 + 重启常驻 bridge(改了代码或白名单后跑这个)
./install.sh uninstall   # 停掉并移除开机自动跑
tail -f whatsapp-bridge/store/bridge.log
```

- **加 / 减群**:在管理页面勾选,立即生效,不用重启。
- **重新扫 QR 码**(大约 20 天一次,或 log 出现 `logged out`):
  `./install.sh uninstall`,在终端机跑 `cd whatsapp-bridge && ./whatsapp-bridge` 扫码,成功后 Ctrl-C,再 `./install.sh`。
- **只收讯息、不自动调查**:把 `config.json` 的 `claude_bin` 清空。
- **整个停用**:`./install.sh uninstall`,手机 WhatsApp「已连结装置」里登出这台。

## 和原版的差别

- 接口只听 `127.0.0.1:8765`(原版是对整个区域网开放的 `:8080`)
- 群白名单:其他聊天不落地
- 只能发纯文字、只能发到白名单群;拿掉 `send_file` / `send_audio_message`
- 讯息表加 `mentions_me`、`sender_name`,`list_messages` 加 `mentions_me_only`
- 加 `triage.go`:tag 触发调查、自动回复群
- 图片 / 影片的说明文字也会存
- whatsmeow 升级到 2026-11
- SQLite 换成纯 Go 的驱动,Windows 不用装 C 编译器

## 限制

- 走的是非官方协议,违反 WhatsApp 条款,有封号风险。
- 电脑要开着才会查;电脑睡眠时收到的 tag,醒来连上后才处理。
- 一次只查一个问题,后面的排队。每次调查最多 15 分钟。
- 自己从手机发的讯息目前不会进库(待查)。
- Windows 版只做过交叉编译,还没在真的 Windows 上跑过。
