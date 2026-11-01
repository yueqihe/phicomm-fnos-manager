# 斐讯设备管理 · fnOS

基于 [phicomm-io 斐讯设备社区项目](https://github.com/phicomm-io/)制作的 fnOS 原生控制工具，在飞牛 NAS 上集中管理 TC1 智能排插、M1 空气检测仪、A1 空气净化器和 DAIKIN 大金空气检测仪。由 [情深深雨蒙蒙（yueqihe）](https://github.com/yueqihe) 独立维护。

使用 Go 后端和 React 前端，网页、SQLite 数据库和 MQTT 服务均内置于程序。NAS 无需安装 Node.js、Go、独立数据库或 Docker，支持局域网直连与 TMCP v2 MQTT 接入。

## 界面预览

桌面端：

![fnOS 桌面端设备列表与 TC1 控制](docs/screenshots/desktop.png)

手机端：

<img src="docs/screenshots/mobile.jpg" alt="通过 fnOS 远程访问的手机端 TC1 控制界面" width="360" />

以上为早期版本的实机截图；当前版本已去掉页面顶部重复的图标、应用标题和手动刷新按钮，设备状态自动更新。

## 下载安装

需要 fnOS 1.2.0 及以上，以及刷入兼容社区固件的 TC1 / M1 / A1 / DAIKIN。[查看最新版本](https://github.com/yueqihe/phicomm-fnos-manager/releases/latest)。

当前版本为 **0.2.10**。下载对应架构的安装包；自行构建请参阅下方“开发与构建”。

| NAS 架构 | 安装包（0.2.10） |
| --- | --- |
| ARM64 / AArch64 | [phicomm-manager-0.2.10-arm.fpk](https://github.com/yueqihe/phicomm-fnos-manager/releases/download/v0.2.10/phicomm-manager-0.2.10-arm.fpk) |
| x86-64 / AMD64 | [phicomm-manager-0.2.10-x86.fpk](https://github.com/yueqihe/phicomm-fnos-manager/releases/download/v0.2.10/phicomm-manager-0.2.10-x86.fpk) |

1. 在飞牛应用中心选择手动安装，上传对应架构的 FPK。
2. 使用飞牛管理员账号，从桌面打开“斐讯设备管理”，无需再设置应用管理密码。
3. 点击列表或空间视图下方的“添加设备”，扫描设备或填写设备内网地址。
4. 在“设置”中调整设备连接、MQTT 服务和电价；“外观”可选择浅色、深色或跟随系统，即时生效并记住此设备的偏好。

从 0.1.x 或 0.2.0–0.2.9 升级时保留应用数据，即可继续使用原设备、空间、连接凭据、历史和电费记录。详细操作与备份方式见 [安装说明](fnos/INSTALL.md)。

## 通过 fnOS 远程控制

可以借助 fnOS 自带的远程访问，在外网打开本工具并查看、控制家中的设备：

1. 先确认 NAS 能通过局域网或 MQTT 正常连接设备。
2. 在 fnOS 中启用并配置远程访问。
3. 在外网登录飞牛，通过桌面的“斐讯设备管理”入口打开应用。

控制请求由 NAS 转发给设备，设备网页和 MQTT 无需单独映射到公网。应用通过 fnOS 统一网关及管理员身份访问；是否可从外网打开，以 NAS 的远程访问和应用入口配置为准。

## 功能

- TC1 / M1 / A1 / DAIKIN 集中查看与控制，支持设备列表、按空间管理和手机自适应界面。
- DAIKIN 支持 IoTCloud 社区固件的 BRY88AX151K 检测仪，显示 PM2.5、PM10、CO₂、温湿度和 TVOC（ppb），按实际模块显示甲醛；支持指示灯控制、独立传感器有效性、CO₂ 预热提示、历史曲线和 CSV 导出。
- 同一设备的局域网与 MQTT 连接按型号和 MAC 合并，接入时核验设备身份。
- 一个“扫描设备”入口结合 mDNS / DNS-SD 与局域网探测，检查所有可用局域网并过滤容器网桥等虚拟接口；接口枚举受限时尝试路由识别，也可在高级选项手填内网网段或单个 IPv4 地址扫描；支持进度和停止。
- 内置 MQTT Broker，也可连接外部 Broker；支持 TMCP v2 自动发现、设备凭据和主题权限，外部连接可使用 TLS。
- 设备连接、服务设置、控制和历史通过弹窗操作。
- 浅色 / 深色 / 跟随系统主题，覆盖设备列表、空间卡片、控制和设置弹窗。
- TC1 用电换算为“度”，支持统一电价与峰谷电价，控制区显示当前单价和预估消费；空间卡片显示实时功率与累计用电。
- TC1 空间卡片逐一显示六路插口编号、名称与开启 / 关闭状态，名称与设备网页同步；离线时显示离线。桌面三列两行，窄屏两列三行，点击状态区域打开控制弹窗。
- 空气检测仪空间卡片显示温度、湿度、甲醛等实时读数；大金另显示 PM10、CO₂、TVOC，按实际模块和传感器有效性显示。
- 默认打开空间视图，四种设备卡片均显示最近 24 小时的小曲线，点击曲线打开历史数据；曲线轻微柔化并搭配淡渐变填充和虚线网格。
- 历史弹窗中“数据指标”右侧的“卡片显示曲线”下拉框，可为每台设备指定卡片与控制弹窗的近 24 小时指标，选择后自动保存到 NAS，跨浏览器保留；设置的设备连接中也可配置。点击曲线直接打开对应指标的历史。默认展示 TC1 功率、检测仪 PM2.5、A1 风机输出，也可选择用电、花费、其他空气指标或滤芯；用电和花费按区间用量显示柱形。
- PM2.5、PM10、CO₂、甲醛、TVOC、温湿度、功耗、实际风机输出和滤芯余量按读数分级变色，列表、卡片、控制弹窗和历史数值使用相同规则，支持深浅主题；悬停数值可查看提示范围。
- 列表控制与空间控制弹窗均可点击插口名称编辑，写入设备后读取确认；设备原网页上的名称改动也会自动同步。
- 历史弹窗可切换“历史数据”和“活动记录”；按设备查看功率、用电、预估花费、空气指标或风机 / 滤芯趋势，支持 24 小时 / 7 天 / 30 天筛选及 CSV 导出。
- “设置 → 数据导出”可下载所有设备已保存的完整历史读数为 CSV，一条记录一行，不受图表范围或汇总粒度限制，包含设备、空间、北京时间和各项原始读数。
- SQLite 本地保存数据，连接凭据使用 AES-256-GCM 加密，沿用旧版 `secrets.key`。

状态自动更新：局域网设备默认每 15 秒采集一次，MQTT 状态随设备上报更新；页面默认每 5 秒同步最新数据，切回前台或网络恢复时立即同步。“设置 → 刷新设置”可配置页面刷新间隔（2–300 秒）和局域网采集间隔（5–60 秒），保存后立即生效，同步到其他浏览器并在重启后保留。历史仍按分钟记录。

打开管理页面后自动扫描一次，页面保持打开时每分钟扫描一次。新发现且尚未登记的设备会显示“添加 / 暂不添加”提示，点击添加后预填型号和地址，由用户确认接入；选择暂不添加后，本次打开期间不重复提醒同一设备。多个页面共用正在进行或最近一分钟的自动扫描，关闭所有页面后停止新设备扫描，异常断开时也会自动结束。此流程与离线设备的地址恢复独立，不会自动添加未知设备。

局域网地址变化时，连续 3 次采集失败会自动重新发现设备；每轮恢复扫描至少间隔 2 分钟，并重新读取 NAS 当前网段，适用于 DHCP 换 IP 或更换路由后 NAS 与设备已连接到新网络的情况。核对型号和 MAC 后只更新原设备地址，保留名称、空间、曲线设置、历史和累计电费；手动扫描也会恢复已登记的设备。同一设备重复添加会合并连接并保留原配置。

设备设有网页密码、使用未通过 mDNS 广播的自定义端口，或处于默认扫描范围之外时，可在“设置 → 设备连接”直接修改原设备的内网地址，或手动指定网段扫描，无需删除设备。扫描不会向未知地址尝试已保存的密码。设备必须先接入 NAS 可访问的网络，管理端不能替设备更换 Wi-Fi；MQTT 设备更换 Broker 地址时仍需更新设备端配置。历史仍遵循最近 30 天保留规则，断网期间不补造读数；此前已删除的设备配置和电费累计无法凭空恢复。

电费从首次有效读数开始累计，不追溯未知历史。峰谷按北京时间每日一段谷时计算，支持跨午夜；两次读数间的电量按时间比例分摊，消费金额供参考。

历史数据从启用本功能后的成功采集开始记录，每个设备每分钟保存一份到现有 SQLite，保留最近 30 天，重启后继续使用；局域网和 MQTT 共用记录。离线或无效采样留空，不回填旧数据。TC1 的功率和电量来自实际计量接口，设备没有上报的指标显示为空。

点击顶部历史图标，或设备卡片 / 控制区的小曲线进入。24 小时按 5 分钟展示，7 天按小时、30 天按 3 小时汇总；功率等指标取区间均值，用电和花费取区间合计。用电与花费按所选时段内有效读数的差额统计，计量归零后建立新起点，花费沿用采集时的计费记录。导出的 CSV 与当前范围和汇总粒度一致，时间为北京时间。

## 开发与构建

后端需要 Go 1.25+；FPK 打包需要 Python 3 和 [fnpack](https://github.com/ckcoding/fnnas-docs)。前端编译与浏览器测试使用 Node.js 22 / 24，仅用于开发电脑。

已提交 React 静态产物，直接构建 Go 或 FPK 无需 Node.js：

```sh
go test ./...
go vet ./...
python scripts/verify-web.py
python scripts/build-fpk.py --platform arm
python scripts/build-fpk.py --platform x86
```

Windows 可使用 `py -3` 替代 `python`。将 Go 和 fnpack 加入 PATH，或分别用 `GO_BIN`、`FNPACK` 环境变量指定路径。构建输出位于 `release/phicomm-manager/`，包含 FPK、SHA256 和安装说明；两种架构分别静态编译，不依赖 NAS 的语言运行时。

修改前端后，在开发电脑重新生成静态文件并验证：

```sh
npm ci
npm run build:web
npm run test:browser
npm run test:history
npm run test:discovery
```

浏览器测试需要先运行 `npx playwright install chromium`，或通过 `PLAYWRIGHT_CHROMIUM_EXECUTABLE` 指定已安装的 Chromium。打包前会检查源码与静态产物的 SHA256，避免将旧网页打入新包。

历史浏览器测试还需 Python 3（Windows 默认 `py -3`，其他平台 `python3`，可用 `PYTHON_BIN` 指定），仅向临时测试数据库写入合成历史数据，不使用实际设备记录。

本机预览：

```sh
go run . --dev --listen 127.0.0.1:8800 --base / --data ./dev-data
```

`--dev` 仅允许回环地址。fnOS 正式服务使用本地 Unix Socket，经统一网关访问，不额外开放 HTTP 管理端口。

## 项目结构

| 路径 | 内容 |
| --- | --- |
| 根目录 Go 文件 | REST API、设备协议、发现、MQTT、SQLite 与电费 |
| `frontend/` | React 组件与样式 |
| `public/` | 编译后的网页、构建校验信息和前端许可证 |
| `fnos/` | FPK 安装模板、生命周期脚本与桌面入口 |
| `scripts/` | 前端构建、FPK 打包与产物校验 |
| `test/`、`native_*_test.go` | 浏览器测试、Go 测试和合成设备 / 迁移样本 |

验证覆盖设备控制、旧版 TC1 兼容、MQTT 权限、统一扫描、旧数据迁移、电费及 320–1440px 页面。FPK 会检查架构、静态链接、权限和网关配置；fnOS 实机安装仍待验证。

## 项目来源与致谢

本工具根据 [phicomm-io](https://github.com/phicomm-io/) 社区项目的设备协议、固件生态和公开资料制作，面向 fnOS 提供独立管理界面。设备需使用兼容的社区固件，原厂固件不保证可接入。

本仓库只发布 fnOS 管理端及其构建、测试文件，不包含上游设备固件、SDK 或 `phicomm-io.github.io` 站点源码。FPK 也不附带这些原项目源码；以下链接用于说明项目来源和查阅设备资料。

- [社区文档、设备资料与下载](https://phicomm-io.github.io/)
- [iotcloud-community-frontend](https://github.com/phicomm-io/iotcloud-community-frontend)
- [iotcloud-community-mqtt](https://github.com/phicomm-io/iotcloud-community-mqtt)
- [设备接口与协议](https://phicomm-io.github.io/iotcloud-community-protocols/)
- [DAIKIN 安装与操作](https://phicomm-io.github.io/guides/daikin-guide.html)
- [fnOS 开发文档参考](https://github.com/ckcoding/fnnas-docs)

感谢上游作者和贡献者。第三方组件保留各自的授权声明，详见 [项目来源与第三方组件](THIRD_PARTY_NOTICES.md)。
