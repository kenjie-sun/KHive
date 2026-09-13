# 开发与检查

## 目录

| 目录 | 用途 |
| --- | --- |
| `cmd/vohive/` | KHive 主程序入口；保留原模块路径 |
| `internal/` | 设备、eSIM、短信、IMS 集成、网络、数据库、API 与通知 |
| `pkg/` | 协议编解码及可复用模块 |
| `third_party/` | 本地维护的通信依赖，通过 `go.mod replace` 引用 |
| `web/` | Vue 界面、前端锁文件与模拟测试 |
| `config/` | 不含实际账户和设备身份的配置模板 |
| `scripts/` | 构建、发布内容检查及隔离回归工具 |

Go 版本要求由根目录 `go.mod` 声明，前端依赖以 `web/package-lock.json` 为准。请从仓库根目录编译，使本地 `replace` 生效；不需要单独获取私有通信库。

## 构建与版本

```sh
make deps
make build-amd64 UPX=
```

版本默认 `v1.0.0`。需要制作开发构建时显式传入自己的版本，例如 `make build-amd64 VERSION=v1.0.0-local UPX=`。版本和 UTC 构建时间经链接参数写入程序；`-trimpath` 避免嵌入本机源码绝对路径，`-buildvcs=false` 避免注入工作树状态。

前端构建由 `scripts/sync-web-dist.mjs` 同步到嵌入目录，最终程序不依赖运行期 Node.js。不要直接把 `web/dist` 或 `internal/web/dist` 提交到 Git。

## 前端开发

```sh
npm ci --prefix web --ignore-scripts --no-audit --no-fund
npm run dev --prefix web
```

页面位于 `http://127.0.0.1:5173`，默认 API 代理为 `http://127.0.0.1:8788`。可用 `VITE_API_PROXY_TARGET` 指向自己的开发后端。

`web/tests/*-preview.html` 是使用内存数据的浏览器夹具，不属于生产入口。测试页面只使用虚构身份与激活信息，不应连接实际设备写入接口。

```sh
node web/node_modules/tsx/dist/cli.mjs --test web/tests/*.test.ts
npm run build --prefix web
```

## 后端隔离回归

完整后端依赖 Linux。`scripts/check-stability.py` 将经过选择的测试编译后，在独立 network namespace 中执行；普通测试降为 uid/gid 65534，使用临时目录，不调用实际模组或生产 API。内核路由用例在其自己的命名空间验证对象创建和清理。

需要 Python 3、Linux amd64，以及创建网络命名空间和降权所需的权限。在独立 Linux 测试主机的仓库根目录运行：

```sh
sudo -E python3 scripts/check-stability.py --local
```

也可从开发机使用自己已配置、明确允许执行隔离测试的 SSH 主机：

```sh
python3 scripts/check-stability.py --ssh linux-test-host
```

脚本输出每项结果和 `output/stability/` 下的汇总，结束后清理测试程序。隔离失败时直接停止，不回退到宿主网络。它是指定范围的回归，不代表全仓测试全绿或真实运营商兼容性验收。不要在正在管理实际模组的环境中随意执行未经检查的整仓测试。

## 发布内容检查

```sh
make check-release
```

检查 Git 索引中选定的发布文件及其工作树内容，拒绝本地运行目录、部署记录、数据库、抓包、私钥、二维码图片、Docker 文件和常见凭据模式；也检查版本、示例配置及产品文档链接。准备提交时应先查看 `git diff --cached`，确保索引与审核的内容一致。

CI 从检出的源码运行相同检查，并使用 Gitleaks 扫描当前文件。工具检查不能代替人工核对：新夹具必须使用虚构数据，问题反馈与公开材料不得含真实 IMSI、ICCID、EID、IMEI、号码、激活资料、账户密钥或内部部署细节。

对外发布前另外审查历史提交和仓库可见性。删除当前分支的文件不会删除 Git 历史；不要为了清理文件自动重写历史。

## GitHub 发布流程

主分支和 PR 执行内容检查、凭据扫描、隔离回归及 amd64 构建。仅当推送到 `main` 的提交消息以 `release:` 开头且前述检查全部通过时，后续发布任务才根据 `web/package.json` 创建正式版本标签与 GitHub Release。

普通构建只读仓库；仅发布任务具有写入发行版所需的 `contents: write` 权限。发行说明取自 `CHANGELOG.md` 当前版本，不附加预编译程序或部署数据；已有版本不会覆盖，已有同名标签也不会强行移动。仓库可见性保持原设置，不由工作流修改。
