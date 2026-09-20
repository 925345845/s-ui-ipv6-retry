# S-UI IPv6 Retry

独立维护的 IPv4/IPv6 配对补齐版代理面板，基于 1S-UI。**按输入的 IPv4 上游列表固定节点数量和顺序，IPv6 不通时自动更换并重试，直到全部配齐或达到本次任务时限。**

- 独立仓库：[925345845/s-ui-ipv6-retry](https://github.com/925345845/s-ui-ipv6-retry)
- 正式版本：[v1.0.0](https://github.com/925345845/s-ui-ipv6-retry/releases/tag/v1.0.0)
- Linux：amd64/x86_64、arm64/aarch64；Debian/Ubuntu，systemd。

## 一键安装 / 覆盖升级

使用 root 执行：

```bash
curl -4 -fsSL https://raw.githubusercontent.com/925345845/s-ui-ipv6-retry/main/paired-release/install-s-ui-paired-online.sh | S_UI_PAIRED_VERSION=v1.0.0 bash
```

脚本只从本仓库 Release 下载对应架构的安装包，安装前校验 SHA-256。原数据库保留，原面板二进制会先备份；启动失败时恢复原二进制。

本项目沿用 `/usr/local/s-ui`、`s-ui` 服务及已有数据库，方便直接升级已有面板；独立指仓库、版本和发布渠道独立，不是同一台服务器上的第二套并行面板。默认访问 `http://服务器IP:2095/app/`；已有安装保留原配置。

本仓库的管理脚本更新、Agent 安装及面板生成的受管客户端安装命令均指向本仓库。

## 使用

1. 打开“入站管理 → 一键中转 → IPv4/IPv6 配对”或“双栈出口”。
2. 选择有已路由公网 IPv6 前缀的网卡，并勾选“系统地址创建”。
3. 粘贴 IPv4 SOCKS5 上游列表，支持 `IP:端口:账号:密码` 等原有格式。
4. 点击创建，等待整批补齐；成功后导出节点。

输入 N 条上游，就生成 N 条入口（最多 500 条）。每轮并发检测 8 条，检测成功的行保留，仅为失败行生成新的 IPv6。IPv4 账号、端口和行顺序保持对应，访问网站时的分流策略不变。

- 手动填写的 IPv6 如果检测不通，也会自动替换。
- 只删除本次添加的失败 IPv6，不删除服务器原有地址。
- 整个补齐阶段最多等待 **8 分钟**，每行没有固定重试次数上限。
- 若整个前缀不通、地址耗尽或系统操作失败，则明确报错，清理本次新地址并取消本次新批次；已有批次不受影响。
- 检测是 IPv6 对公网 TCP 连通性检查，不验证供应商 IPv4 SOCKS5 账号有效性，也不保证所有网站可访问。

安装包包含常用 sing-box 功能和 Agent，未编入 Naive 出站扩展。Windows、OpenWrt 和 Docker 不属于当前发布范围。

## 验证与构建

分配逻辑覆盖 500 条配对、部分地址连续失败后补齐、原有地址保护、超时取消、地址耗尽和清理错误。GitHub Actions 执行 Linux 回归测试、分配器竞态检查、前端构建和安装脚本检查。

```bash
cd frontend
npm ci
npm run build
cd ..
mkdir -p web/html
cp -a frontend/dist/. web/html/
CGO_ENABLED=1 go test ./service ./internal/relayipv6 -run 'Relay|Fill' -count=1
go test -race ./internal/relayipv6 -count=1
CGO_ENABLED=1 go build -trimpath \
  -tags 'with_quic,with_grpc,with_utls,with_acme,with_gvisor,badlinkname,tfogo_checklinkname0,with_tailscale' \
  -ldflags '-w -s -checklinkname=0' -o sui main.go
```

要求 Go 1.26.5、Node.js 25、npm 和 C 编译器。Actions 的“Build independent Linux release”可按已有新标签构建两种架构并生成待发布草稿。

## 来源与许可

- 直接基础：[925345845/s-ui](https://github.com/925345845/s-ui)，提交 `5a66a5e8ef767ca4f051fca14aacc1935829843b`。
- 上游项目：[Hhz0823/1s-ui](https://github.com/Hhz0823/1s-ui)、[alireza0/s-ui](https://github.com/alireza0/s-ui)。
- 许可：[GNU GPL v3](LICENSE)。保留上游版权及许可，修改后的完整源码在本仓库公开。
- Go 模块路径保留上游路径以保持内部导入兼容，不影响独立 GitHub 发布。
- [上游原始说明](docs/UPSTREAM-README.md)作为来源记录保留，其旧安装链接不适用于本项目。
