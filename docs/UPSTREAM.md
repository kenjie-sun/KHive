# 来源与第三方说明

KHive 基于 VoHive 开发，保留上游版权、许可及相关声明。根模块暂保留 `github.com/1239t/vohive`，主程序入口保留 `cmd/vohive`；产品名称与构建产物为 KHive。

| 组件 | 来源与基线 | 使用方式 |
| --- | --- | --- |
| VoHive | [1239t/vohive](https://github.com/1239t/vohive)，`0965af3d3c59bf51ef09ebd7edcafc06f516321f` | 主程序开发基线；项目许可证见根目录 [LICENSE](../LICENSE) |
| vowifi-go | [1239t/vowifi-go](https://github.com/1239t/vowifi-go)，v1.1.3，`21eb46189e0ab82c56c791c4834a9383078f9c5f` | 本地维护 IMS、认证、短信及连接生命周期实现 |
| swu-go | [1239t/swu-go](https://github.com/1239t/swu-go)，v0.0.3，`c4fb4016432c3851d2f4e6ba44261368b01a2be3` | 本地维护协商与 rekey 路径；[MIT 许可证](../third_party/swu-go/LICENSE) |
| sipgo | [emiago/sipgo](https://github.com/emiago/sipgo)，v1.4.0 | 本地 SIP 栈及下一跳处理；[BSD 2-Clause 许可证](../third_party/sipgo/LICENSE) |
| jsQR | [cozmo/jsQR](https://github.com/cozmo/jsQR)，1.4.0 | 浏览器本地二维码解码；[Apache-2.0 许可证](../web/public/licenses/jsqr-LICENSE.txt) |
| Swagger UI | [swagger-api/swagger-ui](https://github.com/swagger-api/swagger-ui) | API 文档静态资源；[许可证](../internal/api/docs_assets/swagger-ui/LICENSE)与 [NOTICE](../internal/api/docs_assets/swagger-ui/NOTICE) |

其余依赖及固定版本以根目录和本地通信库的 `go.mod`／`go.sum`、前端 `package.json`／`package-lock.json` 为准，遵循各自的许可证。

本地通信库保留运行代码和必要测试；与 KHive 构建无关的上游 Docker 示例、独立实验程序和第三方 CI 配置未纳入产品发布。上游文档描述的全部能力不等同于 KHive 的实机支持范围，请以项目 README 的版本范围为准。

KHive 的许可证未改为宽松商业许可。后续发布不得移除现有版权、许可文件或上游要求保留的声明。
