# IPv4 固定配对、IPv6 失败自动补齐

独立发布版本：`1.0.0`。安装请以[仓库首页](../README.md)为准。

基于 `925345845/s-ui` 的 main 提交 `5a66a5e8ef767ca4f051fca14aacc1935829843b`。
发布地址：`https://github.com/925345845/s-ui-ipv6-retry/releases/tag/v1.0.0`。原仓库的安装命令不会下载本修复。

## 修改后的行为

- 适用于“IPv4/IPv6 配对”和“双栈出口”的新批次创建。
- 输入多少条上游 SOCKS5，就按原顺序创建多少条入口，最多 500 条。
- 检测失败只替换该行 IPv6，保留对应的 IPv4 上游、账号、密码和顺序。
- 并发检测 8 条，先检查本轮所有待处理项，再仅对失败项补充新 IPv6；已通过的地址不重复检测。
- 自动补充仅使用当前选择的网卡和 IPv6 前缀，避开现有地址、本批其他地址和已经失败的地址。
- 本次添加的不通 IPv6 会先删除，再生成替代地址；服务器原有地址不会删除。
- 全部配齐后才保存新批次。IPv4/IPv6 的网站访问分流规则不变。

必须勾选“添加到系统网卡/系统地址创建”。手动填写的 IPv6 如检测失败也会被替换。
不勾选时，只能使用已绑定且通过检测的 IPv6。

每行没有固定重试次数上限，但整个补齐阶段最多等待 8 分钟。
若前缀全部不通、地址空间用完、权限不足或网卡操作失败，会停止并显示错误；
本次新批次不保存，清理本次新地址，已有批次不受影响。不会把未通过检测的地址当作成功。
这些检测验证 IPv6 对外 TCP 连通性，并不保证任意目标网站都可访问，也不验证供应商 IPv4 SOCKS5 账号有效性。

## 安装 amd64 修复包

适用于 `uname -m` 为 `x86_64` 或 `amd64` 的 Debian/Ubuntu VPS。
将 `s-ui-linux-amd64.tar.gz` 和 `install-s-ui-paired.sh` 上传到服务器同一目录。
进入该目录，执行：

```bash
bash install-s-ui-paired.sh ./s-ui-linux-amd64.tar.gz
```

安装器需要 root，会保留 `/usr/local/s-ui/db`，备份原面板二进制；新面板启动失败时恢复原二进制。
安装成功后刷新网页，在“入站管理 → 一键中转 → IPv4/IPv6 配对/双栈出口”重新创建。
如操作的是受管子服务器，应更新实际执行创建的子服务器。

安装包沿用仓库本地配对包的构建方式，包含 sing-box 常用功能，未启用 Naive 出站扩展。
本地没有连接你的 VPS，未执行实际部署或真实网卡通网测试。

## 源码构建

源码包包含本次修改和回归测试。要求 Go 1.26.5、Node.js 及 npm、Linux C 编译器。

```bash
cd frontend
npm ci
npm run build
cd ..
mkdir -p web/html
cp -a frontend/dist/. web/html/
CGO_ENABLED=1 go test ./internal/relayipv6 ./service -run 'Fill|Relay' -count=1
CGO_ENABLED=1 go build -trimpath \
  -tags 'with_quic,with_grpc,with_utls,with_acme,with_gvisor,badlinkname,tfogo_checklinkname0,with_tailscale' \
  -ldflags '-w -s -checklinkname=0 -X github.com/Hhz0823/1s-ui/config.version=1.0.0' \
  -o sui main.go
```

源码包省略仓库中旧的 `paired-release/*.zip`、`*.tar.gz`、旧补丁及校验清单，以免误装旧二进制。
要在 GitHub 发布，请将源码修改提交到自己的仓库并使用现有 Actions 重新构建一个新版本。
本次补丁以以上 main 提交为基准，不能保证直接应用到 v1.5.20 源码。

## 验证记录

- 新增 10 项分配逻辑测试通过，包括 500 条配对中 72 条连续失败 3 次后全部补齐、只重试失败项、已有地址保护、超时取消、前缀耗尽和清理失败。
- `CGO_ENABLED=1 go test ./service ./internal/relayipv6 -run 'Relay|Fill' -count=1` 通过。
- 修复了原有 6 项中转数据库测试未关闭数据库连接的问题，避免 Windows 临时目录清理失败。
- `npm run build` 通过（包含 TypeScript 检查）。
- Linux amd64 面板和 Agent 编译通过，确认 ELF 架构和静态链接；安装脚本 Bash 语法检查通过。
- 补丁反向应用检查通过，压缩包完整性和包内可执行权限检查通过。
- 额外尝试的 Go `-race` 检查受本机 Windows ThreadSanitizer 内存映射错误限制，未能运行完成；不计作通过。
- 未进行真实 Linux 网卡操作、VPS 部署、供应商 IPv4 代理或指定网站访问测试。
