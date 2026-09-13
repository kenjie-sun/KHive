# KHive 卡策略 API

沿用现有 API 认证。卡策略跟随 ICCID 保存；设备实时操作仍通过 `/api/devices/{id}/network`、`vowifi`、`flight-mode` 的 PATCH 完成。GET/PUT 卡策略本身不切卡、不改变正在运行的设备。

- `GET /api/cards/{iccid}/policy`：已有策略或默认模板，未建档的卡不会因 GET 而写库。
- `PUT /api/cards/{iccid}/policy`：部分字段更新。省略保持原值，显式 `false` 会保存。

| 字段 | 值 |
| --- | --- |
| network_enabled | boolean |
| vowifi_enabled | boolean |
| airplane_enabled | boolean，用户原始飞行意图 |
| ip_version | v4 / v6 / v4v6 |
| apn | string，空字符串表示清空 |

网络不能与 VoWiFi 或飞行同时开启，违反约束返回 400，整次请求不入库。VoWiFi 与 airplane_enabled 可以同时为 true，表示关闭 VoWiFi 后回退到原有飞行状态。

例如，为未激活卡设置 VoWiFi 并保留飞行意图：

```json
{
  "network_enabled": false,
  "vowifi_enabled": true,
  "airplane_enabled": true
}
```

同时提交 network_enabled=true 和 vowifi_enabled=true 会返回 400；客户端需要明确选择期望状态。
