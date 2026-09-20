# 1S-UI IPv4/IPv6 配对与双栈回退中转

此补丁为 1S-UI 增加 IPv4/IPv6 配对模式 `paired` 和双栈回退模式 `dualstack`。

每一条入口代理对应：

- 一个上游 IPv4 SOCKS5 出口；
- 一个绑定到 VPS 网卡的 IPv6 出口；
- IPv4-only 目标走 SOCKS5；
- IPv6-only 或双栈目标优先走对应 VPS IPv6。

上面的地址族固定分流属于 `paired` 模式。“双栈出口” `dualstack` 模式会在同一个入口内按以下顺序连接：

1. 先使用绑定到该入口的 VPS IPv6 连接目标 IPv6 地址；
2. 目标同时有 A/AAAA 时，只有本次 IPv6 连接失败才尝试 IPv4；
3. 目标只有 A 记录或直接使用 IPv4 地址时，立即使用同一行对应的 IPv4 SOCKS5；IPv4 回退不会使用 VPS 原生 IPv4。

## 直接安装

先在 VPS 执行 `uname -m`。下载通用安装脚本，并按架构下载一个安装包，上传到 VPS 的同一目录：

- `install-s-ui-paired.sh`
- `x86_64`：`s-ui-linux-amd64-paired.tar.gz`
- `aarch64`：`s-ui-linux-arm64-paired.tar.gz`

然后执行：

```bash
chmod +x install-s-ui-paired.sh
sudo ./install-s-ui-paired.sh
```

安装脚本支持全新安装和覆盖现有 1S-UI。现有数据库会保留在 `/usr/local/s-ui/db`，原面板二进制会先备份；新版本启动失败时自动恢复。

本地构建包支持 `amd64/x86_64` 和 `arm64/aarch64` Linux VPS，并省略与配对功能无关的 Naive 出站。使用 Naive 节点或其他 CPU 架构时，请使用下方完整源码包通过仓库 GitHub Actions 构建。

## 应用补丁

在 1S-UI 源码仓库执行：

```bash
git apply --check 1s-ui-ipv4-ipv6-paired.patch
git apply 1s-ui-ipv4-ipv6-paired.patch
```

然后按仓库现有发布流程重新构建前端和 Linux 二进制。建议在自己的 fork 中提交改动并使用仓库 GitHub Actions 生成 Release。

## 面板使用

1. 打开“入站管理 -> 一键中转 -> 双栈出口”。如果只需要按地址族固定分流，使用“IPv4/IPv6 配对”。
2. 选择 VPS 的公网 IPv6 网卡，必要时填写已路由 IPv6 前缀。
3. 将代理供应商返回的内容粘贴到“上游列表”。保留原有 IPWO 文本格式，同时支持常见 SOCKS5 格式：

```text
203.0.113.10:1080
203.0.113.11:1080:user:password
socks5://user:password@203.0.113.12:1080
user:password@203.0.113.13:1080
203.0.113.14:1080@user:password
203.0.113.15,1080,user,password
user|password|203.0.113.16|1080
```

也可以粘贴常见 JSON 数组或带 `data`/`proxies`/`list`/`items` 外层字段的对象，例如：

```json
[{"ip":"203.0.113.10","port":1080,"username":"user","password":"password"}]
```

字段名支持 `ip`/`host`/`server`/`address`、`port`/`server_port`、`username`/`user`/`login` 和 `password`/`pass`/`pwd`（不区分大小写）。这里只能接收 SOCKS 类代理；HTTP/HTTPS 代理需要先转换为 SOCKS5。

4. 上游列表有多少行，就会按顺序生成多少个 IPv6 和入口端口。
5. 创建成功后复制面板导出的 `VPS地址:端口:账号:密码`。

## IPv6 轮转

在“入站管理 -> 一键中转 -> 中转批次”中：

- 每条 IPv6 都有一个稳定、独立的手动轮转链接；
- 访问某条 `/refresh/令牌` 链接只更换这一条 IPv6，其他地址不会变化；
- 轮转后链接本身不变，新 IPv6 仍对应原来的 IPv4 SOCKS5；
- 入口端口、账号、密码、协议和导出内容不会改变；
- 面板先添加并验证新地址，成功后再删除这一条旧地址；失败时清理新地址并保留旧配置。

刷新链接相当于轮转密码，请勿公开。纯“上游 SOCKS5”池没有 VPS IPv6，因此不生成链接。旧版本创建的 IPv6、配对和双栈池升级后会自动补齐独立链接，无需重新创建。

## IPv6 地址持久恢复

面板会把自己创建且仍被中转批次引用的 IPv6 保存在数据库中。VPS 重启或网络服务重载后，面板启动时会自动把缺失地址重新绑定到原网卡，并每分钟校验一次。短暂的网络、路由或 DAD 未就绪只会等待下次重试，不会删除已保存地址；删除中转批次时仍按原有行为释放其地址。

配对和双栈批次最多支持 500 条上游代理。每条上游都创建独立的入口、出口和路由配置；大批次会增加 sing-box 配置体积和内存占用，起始端口必须满足 `起始端口 + 数量 - 1 <= 65535`。

导出 BitBrowser 批量导入文件时，每条完整轮转链接会写入对应行的“窗口备注”（J 列）并可直接点击；代理信息仍保留在 F 列，批量导入格式不变。

## IPv6 优先回退

启用“Apple ID 专用 IPv4，其余仅 IPv6”后，默认使用对应 VPS IPv6；只有 `appleid.apple.com`、`idmsa.apple.com`、`gsa.apple.com` 固定走同一行 IPv4 SOCKS5，其他 IPv4-only 网站不会回退 IPv4。未启用该选项时保持原有配对/双栈行为。

## 大量地址优化

面板不会再把所有已管理的 IPv6 重复显示在“检测到的公网 IPv6”列表中，并限制地址与中转池的页面预览。复制、BitBrowser 导出和实际代理条目仍包含完整数据。创建和启动恢复时会按网卡批量检查 IPv6 状态，避免每个地址重复执行一次完整扫描。

## 验证

对同一个入口分别访问 IPv4-only 和 IPv6-only 查询地址：

```bash
curl --socks5-hostname VPS地址:端口 --proxy-user 账号:密码 https://api.ipify.org
curl --socks5-hostname VPS地址:端口 --proxy-user 账号:密码 https://api6.ipify.org
```

第二条应显示对应 VPS IPv6，第一条应显示该入口对应的上游 SOCKS5 IPv4。对于同时有 A 和 AAAA 记录的目标，IPv6 连接正常时不应使用 IPv4。

测试双栈回退时，可以先临时阻断或停用该 VPS IPv6 路由，再访问一个同时有 A 和 AAAA 记录的网站；连接应在 IPv6 超时后自动回到对应 IPv4 SOCKS5。

## 限制

- VPS 必须拥有服务商实际路由或授权的 IPv6 前缀。
- 上游 SOCKS5 的 UDP 支持取决于代理供应商；浏览器 QUIC 可能回退到 TCP。
- 域名先由 VPS 本地 DNS 解析并保留 A/AAAA 地址；双栈出口的 IPv6 优先和 IPv4 回退由内置复合 outbound 执行，因此 CDN 节点选择可能反映 VPS DNS 所在地，而不是上游 IPv4 代理所在地。
