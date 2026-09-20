# IPv6 Retry — 独立 IPv4 / IPv6 配对工具

**v1.1.0 起独立运行，不再覆盖原来的 s-ui。** 首页直接提供 IPv4 上游列表输入、IPv6 设置、批量创建、连接导出和独立刷新链接。

## 一键安装

Debian/Ubuntu、systemd，支持 amd64 和 arm64。以 root 执行：

```bash
curl -4 -fsSL https://raw.githubusercontent.com/925345845/s-ui-ipv6-retry/main/paired-release/install-s-ui-paired-online.sh | S_UI_PAIRED_VERSION=v1.1.0 bash
```

访问：`http://服务器IP:2195/app/`。首次账号密码：`admin` / `admin`，登录后请修改密码。

| 项目 | 本独立工具 |
| --- | --- |
| 安装目录 | `/usr/local/s-ui-ipv6-retry` |
| 数据库 | `/usr/local/s-ui-ipv6-retry/db/s-ui-ipv6-retry.db` |
| 服务 | `s-ui-ipv6-retry.service` |
| 管理命令 | `s-ui-ipv6-retry status` / `log` / `restart` / `update` |
| 网页 / 订阅端口 | `2195` / `2196` |
| 控制套接字 | `/run/s-ui-ipv6-retry/control.sock` |
| 登录 Cookie | `s-ui-ipv6-retry` |
| 配对入口默认起始端口 | `40000` |

**独立安装从空数据库开始，不复制原 s-ui 的节点或设置。** 原 `/usr/local/s-ui`、`/usr/bin/s-ui`、`s-ui.service` 均不改动、不停止。两套实例可同时运行，代理入口端口仍应选择互不冲突的范围。

如果装过 v1.0.0：那一版只是仓库独立，实际仍覆盖原面板。v1.1.0 会另外安装独立实例，不自动恢复或删除原面板。旧版安装包已停用，请勿继续使用 v1.0.x。

## 使用

1. 登录后首页即为“IPv4 / IPv6 配对工作台”。
2. 在顶部 **IPv4 上游列表（必填）** 粘贴代理，每行一个，支持 `IP:端口:账号:密码`、`socks5://` 和供应商 JSON。
3. 选择网卡和已路由公网 IPv6 前缀。保持“自动添加到系统网卡”开启。
4. 创建配对。输入 N 条上游，就按原順序配齐 N 条入口，最多 500 条。
5. 从下方已创建批次复制连接、导出比特浏览器导入表，或查看每条 IPv4 / IPv6 对应关系和刷新链接。

IPv6 不通时只替换失败行，成功的配对保留。每轮最多 8 个并发检测，整个补齐阶段最多 8 分钟；地址空间耗尽、网卡操作失败或前缀整体不通时明确报错，不保存未完成的新批次。只清理本次添加的地址。

网站访问分流保持原逻辑。IPv6 通网检测不验证供应商 IPv4 SOCKS5 账号，也不保证任意网站均可访问。

## 构建和验证

要求 Go 1.26.5、Node.js 25、npm、C 编译器。发布包包含 sing-box 和 Agent，未编入 Naive 出站扩展。

```bash
npm ci --prefix frontend
npm run build --prefix frontend
mkdir -p web/html
cp -a frontend/dist/. web/html/
CGO_ENABLED=1 go test ./service ./config -run 'Relay|Independent' -count=1
go test -race ./internal/relayipv6 -count=1
CGO_ENABLED=1 go build -trimpath -tags 'with_quic,with_grpc,with_utls,with_acme,with_gvisor,badlinkname,tfogo_checklinkname0,with_tailscale' -ldflags '-w -s -checklinkname=0' -o sui main.go
```

CI 还运行独立安装器隔离/回滚测试和 Chromium 界面测试。界面测试使用模拟 API 验证表单显示与提交；真实 IPv6 网络仍需要在自己的 VPS 上验证。

## 来源与许可

基于 [925345845/s-ui](https://github.com/925345845/s-ui) 的 `5a66a5e8ef767ca4f051fca14aacc1935829843b`，上游为 [Hhz0823/1s-ui](https://github.com/Hhz0823/1s-ui) 和 [alireza0/s-ui](https://github.com/alireza0/s-ui)。保留 [GPL-3.0](LICENSE) 许可及上游版权，完整修改源码公开。

内部 Go 模块路径保留上游名字，与安装目录和运行隔离无关。[上游原始文档](docs/UPSTREAM-README.md)仅用于来源记录，其中旧安装命令不适用于本项目。
