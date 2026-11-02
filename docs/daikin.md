# 大金社区固件接入

适用于刷入 [IoTCloud 社区固件](https://phicomm-io.github.io/guides/daikin-guide.html)的 BRY88AX151K 检测仪。当前源码已适配新版接口，已发布的 0.2.10 安装包尚不包含此次更新。

## 局域网

在“添加设备”中扫描，或选择 DAIKIN 并填写设备内网地址。设备设置了网页密码时，在连接设置中填写该密码；未设置密码则留空。已有设备可直接沿用原连接和历史记录。

管理端通过公开的 `/api/v1/info` 自动识别通信格式。新版固件使用 MessagePack，设置密码后通过 `/api/v1/auth/login` 获取 Bearer 令牌；令牌只保存在内存中，到期或失效后重新登录。旧版 JSON / Basic 接口继续兼容，无需手动选择协议。

指示灯等控制请求只有在设备明确拒绝认证或数据格式时才允许协商后重试。网络中断、超时或响应不匹配时不会重放控制操作。

## MQTT

继续使用 TMCP v2 主题。支持新版 schema 2 发现消息，并从发现、能力或实时状态消息识别 JSON / MessagePack 格式；命令采用对应格式，在线状态仍使用 `online` / `offline`。更新后保留已有设备的名称、空间和历史。

## 读数与验证

支持 PM2.5、PM10、CO₂、温度、湿度、TVOC（ppb）和可选甲醛；各项仍按传感器有效性显示并记录历史，可导出 CSV。没有甲醛模块时不显示该项。

本次使用 `1.0.0-rc.5-c09a06` 的实际设备验证了局域网身份读取、六项传感器读数、历史记录与 CSV。密码登录、令牌更新、指示灯控制及 MQTT MessagePack 收发使用模拟设备回归测试，未修改实际设备的配置。

新通信支持编译进现有 Go 程序，NAS 无需额外运行时。开发者可用 `PHICOMM_TEST_DAIKIN_URL` 指定设备地址，运行只读实机检查：

```sh
PHICOMM_TEST_DAIKIN_URL=http://192.168.1.100 go test -run '^TestDAIKINLiveReadOnly$' -v .
```

检查使用临时管理端数据库，不修改设备 Wi-Fi、MQTT、指示灯或固件。
