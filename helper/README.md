# Go helper：源码、构建与预编译分发

[返回项目首页](../README.md)

Go 源码、测试、构建脚本及四个平台的预编译归档都随仓库保存。当前 helper 为 `v0.2.0`，日常管理入口已经完成迁移。分发包位于 `helper/prebuilt/<版本>/`，解压后的本机安装状态和构建缓存位于 Git 忽略的 `.system-sync/`。TOML 解析固定使用 [go-toml v2.4.3](https://github.com/pelletier/go-toml/releases/tag/v2.4.3)，源码和 MIT 许可保存在 `helper/vendor/`，使用 `-mod=vendor` 离线构建；模块校验记录在 `helper/go.sum`。

## 资源目录

本组件的源码、依赖、版本、许可、脚本、分发包和说明集中在 `helper/`。下列路径相对于本目录；文中的命令均在仓库根目录执行。

| 资源 | 路径 |
|---|---|
| Go 模块与依赖校验 | [`go.mod`](go.mod)、[`go.sum`](go.sum)、[`vendor/`](vendor/) |
| 构建、安装、校验、测试与启动 | [`scripts/`](scripts/) 中的 `build.sh`、`install.sh`、`verify.sh`、`test.sh`、`run.sh`；共用函数在 `common.sh` |
| 预编译分发包与源码输入清单 | [`prebuilt/`](prebuilt/) |
| Go 许可文本 | [`licenses/`](licenses/) |

Go 单元测试随源码保存；项目级 CLI、离线安装和 Python 行为对照测试仍统一放在根目录 [`tests/`](../tests/)。`helper/` 是独立 Go 模块，直接使用 `go test`、`go vet` 等命令时应先进入本目录；通过 mise task 运行时，脚本会自动选择模块目录。启动器始终向二进制传入仓库根路径，包清单、配置、锁和日志仍以仓库根目录为准，本机安装缓存继续使用 `.system-sync/helper/`。

## 已迁移的范围

| 命令或入口 | 实现与状态 |
|---|---|
| `mise run audit` | Go；保持 JSON schema 1、颜色和固定标记、查询并发、脱敏及退出状态 |
| `parse-status` | Go；校验 mise 包状态结构、状态、名称及非空声明，输出 TSV |
| `parse-zypper` | Go；校验 XML 根节点、名称、对象种类及完整移除事务 |
| 整机 `status` / `dry-run` / `system-sync`、`macos-upgrade` | 保留 Bash 平台流程，预检、共享锁、事务日志和解析调用 Go |
| `package-install` / `package-uninstall` | Go；保留五平台限制、精确信任、单包状态复核、清单原子写入与多文件失败回滚及审计 |
| `config-detach` | Go；保留链接物化、PATH 与锁文件依赖迁出、私有备份、并发检查及事务回滚 |
| VS Code 扩展 | Go；仍调用官方 CLI，保留清单外扩展、隔离目录、安装后版本复核及超时子进程清理 |
| 本机配置历史 | Go；仍调用 mise 原生历史能力，保持 `track-local`、手动保存及严格候选范围 |
| 原有 Python 测试 | 保留；另增 Go 测试、CLI 对照和预编译安装回归 |

除开发检查 `check` 外，所有日常 task 均不需要 Python 或 Go 编译器。仍被使用的 7 个 Python 参考实现集中在 [tests/reference/](../tests/reference/)，用于行为对照和回归测试；其中 `sync_runtime.py` 还承担检查测试解释器要求及跨语言锁兼容性测试。未被执行或测试引用的旧 `config-history.py` 已移除，`scripts/` 不再保留 Python 文件。Python 测试依赖仍保留，未将运行时迁移等同于测试语言迁移。单包操作与配置迁出继续共享操作锁；单包原生事务失败后保留人工复核记录，配置迁出失败则按原行为回滚且不覆盖并发修改。

## 新机器直接运行

完整克隆仓库后，在仓库根目录执行：

```bash
mise trust --all
mise run audit

# 也可提前初始化，或直接检查版本
mise run helper-install
./helper/scripts/run.sh --version
```

首次调用自动识别 macOS/Linux 和 ARM64/x86_64，按 `helper/VERSION` 从 `helper/prebuilt/<版本>/` 校验本机归档并解压，无需 Go、Python、网络或 Git LFS。只需系统现有 Bash、tar、awk 及 `sha256sum` 或 `shasum` 等基础命令。初始化只写入仓库本机缓存，安装提示输出到 stderr；审计 JSON/解析 TSV 的 stdout 保持原格式。多个首次调用通过独立安装锁串行激活。

后续调用直接校验并复用已安装的二进制。完整开发检查 `mise run check` 需要编译器和测试环境，新机器使用 helper 无需先运行它。审计会执行 mise 和系统包管理器的正常只读查询；helper 的离线初始化不意味着所有系统包查询或安装都无需网络。

## 修改源码并构建

| 内容 | 路径 |
|---|---|
| CLI 入口、版本注入 | [`cmd/system-sync-helper/`](cmd/system-sync-helper/) |
| 子命令与参数 | [`internal/helpercli/`](internal/helpercli/) |
| 审计逻辑和报告 | [`internal/audit/`](internal/audit/) |
| JSON/XML 解析 | [`internal/packagestate/`](internal/packagestate/) |
| 前置检查、共享锁和日志 | [`internal/syncstate/`](internal/syncstate/) |
| 查询超时、进程树清理和终端识别 | [`internal/query/`](internal/query/)、[`internal/terminal/`](internal/terminal/) |
| 单包清单编辑与状态复核 | [`internal/packageedit/`](internal/packageedit/) |
| 配置迁出、快照与事务回滚 | [`internal/configdetach/`](internal/configdetach/) |
| VS Code 与本机历史 | [`internal/extensions/`](internal/extensions/)、[`internal/confighistory/`](internal/confighistory/) |
| TOML、路径与写入命令的进程生命周期 | [`internal/localconfig/`](internal/localconfig/)、[`internal/taskexec/`](internal/taskexec/) |
| 固定 helper 版本 | [`VERSION`](VERSION) |

构建固定使用 Go 1.27.3。沿用本机已有的 mise 全局 Go 声明，根 `mise.toml` 不再重复声明一套工具版本。修改 `.go` 源码后，在仓库根目录显式执行：

```bash
mise run helper-build
mise run helper-bundle
mise run check
```

如果当前环境未选择这一编译器，可显式运行 `mise exec go@1.27.3 -- mise run helper-build`。mise 可能先安装所指定的编译器；`helper-build` 本身使用 `GOTOOLCHAIN=local` 和关闭的 Go 模块网络，不自动升级工具链或拉取依赖。完整检查还需要 Python 3.11+ 和支持 Go race 检查的本机 C 工具链。

构建使用 `CGO_ENABLED=0`、`-trimpath` 和固定版本注入，关闭自动 VCS 信息捕获；`REVISION` 记录 Git 提交及是否有本地改动，不嵌入本机配置或绝对源码路径。`helper-build` 构建并启用本机版本；`helper-bundle` 更新随仓库保存的四平台归档。修改源码不会隐式重编译，源码与新的 `helper/prebuilt/<版本>/` 应一同提交。

## 生成和安装预编译产物

```bash
# 开发机更新随 clone 保存的四平台包及校验清单
mise run helper-bundle

# 可选：开发机或 CI 导出四平台包到被 Git 忽略的目录
mise run helper-dist

# 目标机器使用仓库内的包，不需要编译器
mise run helper-install

# 也可从其他经过核对的本机目录安装同版本产物
mise run helper-install /绝对路径/产物目录

# 核对当前二进制版本
./helper/scripts/run.sh --version
```

`helper-bundle` 写入 `helper/prebuilt/<helper 版本>/`；`helper-dist` 写入 `.system-sync/dist/<helper 版本>/`。文件名为 `system-sync-helper_<版本>_<系统>_<架构>.tar.gz`，目标为 `darwin_arm64`、`darwin_amd64`、`linux_arm64`、`linux_amd64`，作为普通 Git 文件保存。归档只携带二进制、版本、构建 revision、内部 SHA256、Go 许可文本 `LICENSE-Go` 和 TOML 解析库许可 `LICENSE-go-toml`。跨平台构建方式见 [Go 构建文档](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)和[目标平台变量](https://go.dev/doc/install/source#environment)。

每批产物同时生成外层 `SHA256SUMS` 和 `SOURCE_SHA256SUMS`。后者以 `helper/` 为基准，只记录非测试 Go 源码、Go 模块文件、vendored 依赖、helper 版本、构建脚本和许可等公共构建输入，不收集 dotfiles、本机历史、私有配置或日志。`./helper/scripts/verify.sh` 无需 Go 即可校验四平台归档及源码输入；它也作为 `mise run check` 的第一步，源码或构建输入改变但未更新分发包时会停止并提示运行 `helper-bundle`。它用于防止遗漏更新，不是可重现构建的证明。

安装器默认从**随仓库保存的本机目录**读取归档，不下载工具、不编译源码。它校验外层归档 SHA256、内部二进制 SHA256、固定版本、协议和当前平台，拒绝额外路径、链接及不支持的归档结构。归档和校验清单应来自同一次可信构建；SHA256 用于检测文件损坏，不替代发布者身份认证。

成功安装位于 `.system-sync/helper/<版本>/<二进制 SHA256>/`，然后原子更新 `current` 指针；失败时保留之前激活的版本。启动入口每次校验固定版本和二进制 SHA256，并显式传入仓库路径，不依赖调用时所在目录。只有尚未初始化时自动安装；已有记录过期或校验失败时停止，避免覆盖异常现场。更新 `helper/VERSION` 后运行 `mise run helper-install` 启用仓库的新版本；同版本更新分发包后也需显式安装才能切换已有缓存。旧构建保留在本机，可在不使用时自行清理对应目录。

CI 在准备编译器前校验并启动仓库内的预编译工具，再运行四组 macOS/Linux 与 Python 版本检查；通过后重新构建四平台包，上传 `system-sync-helper-<提交 SHA>` artifact，仅包含归档和校验清单。当前工作流见 [check.yml](../.github/workflows/check.yml)，不会自动创建 Release、推送代码或打包本机历史、私有配置和日志。普通 clone 已有运行所需的包，不依赖 GitHub artifact 或额外下载。

## 验证与后续迁移

`mise run check` 先校验仓库分发包，再显式重建本机 helper、检查前置版本和 task 配置，运行 Go race 检查和原有 Python 测试。Go 覆盖安全解析、查询超时清理、并发和共享状态、锁冲突与继承、日志状态；CLI 对照测试使用临时 HOME、合成私有值和替身命令，比较两种实现的完整报告。预编译回归另覆盖空缓存首次调用、并发初始化、禁用 Go/Python/下载命令、JSON 输出、四平台归档校验、源码漂移及损坏拒绝执行。

2026-10-21 本机 macOS ARM64 的 `v0.2.0` 已通过 49 项 Go 测试（含 race 检查）、166 项 Python 回归（含 19 项 CLI/安装回归）、22 项 task 校验及 `go vet`。原有隔离部署、扩展安装和本机历史测试已改为调用 Go，原 Python 单元测试作为参考继续保留。新增验证在禁用 Go、Python 和下载命令的环境中完成单包安装、共享锁冲突检查、配置迁出、扩展同步及本机历史登记/保存；配置内容保持本机，输出不含测试私有值。

四个平台归档、内外两层 SHA256、目标格式及 Linux 静态链接均已核对，压缩包合计约 6.4 MiB；模块缓存为空且关闭网络时，仍可从 `helper/vendor/` 构建。此前真实只读审计与 Python 参考报告逐字段一致。上述验证均未修改本机真实软件包、扩展或用户配置；新 CI 流程尚未在远端执行。

日常管理入口的迁移已完成；后续按需要迁移测试，再移除对应 Python 参考实现。当前 Linux 原生包管理器的真实主机验收仍待完成，交叉编译成功不代表已完成真实 Linux 系统安装/卸载验收。
