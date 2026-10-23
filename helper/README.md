# Go helper：源码、构建与预编译分发

[返回项目首页](../README.md)

Go 源码、测试、构建脚本及四个平台的预编译归档都随仓库保存。当前 helper 为 `v0.2.0`，日常管理入口已经完成迁移。分发包位于 `helper/prebuilt/<版本>/`，解压后的本机安装状态和构建缓存位于 Git 忽略的 `.system-sync/`。TOML 解析固定使用 [go-toml v2.4.3](https://github.com/pelletier/go-toml/releases/tag/v2.4.3)，源码和 MIT 许可保存在 `helper/vendor/`，使用 `-mod=vendor` 离线构建；模块校验记录在 `helper/go.sum`。

## 资源目录

本组件的源码、依赖、helper 版本、许可、脚本、分发包和说明集中在 `helper/`；Go 工具链版本复用受管 mise 配置。下列路径相对于本目录；文中的命令均在仓库根目录执行。

| 资源 | 路径 |
|---|---|
| Go 模块与依赖校验 | [`go.mod`](go.mod)、[`go.sum`](go.sum)、[`vendor/`](vendor/) |
| 构建、安装、校验、测试与启动 | [`scripts/`](scripts/) 中的 `build.sh`、`install.sh`、`verify.sh`、`test.sh`、`run.sh`；共用函数在 `common.sh` |
| 固定 Go 工具链及读取入口 | [`../dotfiles/common/mise/config.toml`](../dotfiles/common/mise/config.toml) 的 `tools.go`、[`scripts/go-version.sh`](scripts/go-version.sh)；本机默认 Go、CI、构建及测试共用 |
| 预编译分发包与源码输入清单 | [`prebuilt/`](prebuilt/) |
| 开发检查与隔离验收 | [`dev/check/`](dev/check/)、[`dev/checkrun/`](dev/checkrun/)、[`dev/integration/`](dev/integration/) |
| Go 许可文本 | [`licenses/`](licenses/) |

Go 单元测试随生产源码保存；检查运行器与项目级 CLI/离线安装回归集中在 [`dev/`](dev/)，根目录 [`tests/`](../tests/README.md) 保留入口、覆盖清单和独立报告基线。`helper/` 是独立 Go 模块，直接使用 `go test`、`go vet` 等命令时应先进入本目录；通过 mise task 运行时，脚本会自动选择模块目录。启动器始终向二进制传入仓库根路径，包清单、配置、锁和日志仍以仓库根目录为准，本机安装缓存继续使用 `.system-sync/helper/`。

## 已迁移的范围

| 命令或入口 | 实现与状态 |
|---|---|
| `mise run audit` | Go；保持 JSON schema 1、颜色和固定标记、查询并发、脱敏及退出状态；非正常分组完整展开明细，正常分组明确省略数量 |
| `package-capture` / `machine-init` | Go；默认预览，五平台清单采集、追加候选、分阶段补装、配置备份与工具来源核对；保留已有软件，不调用整机清理 |
| `parse-status` | Go；校验 mise 包状态结构、状态、名称及非空声明，输出 TSV |
| `parse-zypper` | Go；校验 XML 根节点、名称、对象种类及完整移除事务 |
| 系统软件 `package-status` / `package-dry-run` / `system-sync`、`macos-upgrade` | 保留 Bash 平台流程，预检、共享锁、事务日志和解析调用 Go |
| `package-install` / `package-uninstall` | Go；保留五平台限制、精确信任、单包状态复核、清单原子写入与多文件失败回滚及审计 |
| `config diff` / `config sync` | Go；与 `machine-init` 共用 `internal/configops/` 的目标筛选、差异、备份和复查；日常同步加入共享锁及本机事务记录 |
| `config-detach` | Go；保留链接物化、PATH 与锁文件依赖迁出、私有备份、并发检查及事务回滚 |
| VS Code 扩展 | Go；调用官方 CLI，列出并保留清单外扩展；`vscode-status --capture` 预览新增项及已纳管项的本机版本变化，新项默认归入 `common`，已有项原分组更新，可筛选后备份并原子写入清单；同步明确提示降级；保留隔离目录、版本复核及超时子进程清理 |
| 本机配置历史 | Go；仍调用 mise 原生历史能力，保持 `track-local`、手动保存及严格候选范围；逐项列出登记、保存和跳过的路径，不输出正文 |
| 开发测试 | Go 单元与 race 测试、Go CLI/部署集成测试、冻结报告和预编译安装回归；旧 Python 实现及测试已移除 |

所有日常 task 均不需要 Python 或 Go 编译器；开发检查 `check` 需要 Go 和本机 C 工具链，同样无需 Python。原有 7 个 Python 参考实现及全部 Python 测试/辅助文件已在逐项核对覆盖后移除。检查运行器、隔离集成测试和复杂命令替身集中在 `dev/`，复用当前模块和 vendor 依赖；系统预检直接调用 Go，共享锁竞争使用 Go 的 POSIX `flock`。覆盖分工和审计基线来源见[测试说明](../tests/README.md)。单包操作与配置迁出继续共享操作锁；单包原生事务失败后保留人工复核记录，配置迁出失败则按原行为回滚且不覆盖并发修改。

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

已有克隆更新仓库后，可运行 `mise run helper-install` 将本机缓存切换到仓库当前分发包；开发者修改源码后的构建验收见下文。

后续调用直接校验并复用已安装的二进制。完整开发检查 `mise run check` 需要编译器和测试环境，新机器使用 helper 无需先运行它。审计会执行 mise 和系统包管理器的正常只读查询；helper 的离线初始化不意味着所有系统包查询或安装都无需网络。

## 修改源码并构建

| 内容 | 路径 |
|---|---|
| CLI 入口、版本注入 | [`cmd/system-sync-helper/`](cmd/system-sync-helper/) |
| 子命令与参数 | [`internal/helpercli/`](internal/helpercli/) |
| 软件采集与机器接入 | [`internal/onboard/`](internal/onboard/) |
| 审计逻辑和报告 | [`internal/audit/`](internal/audit/) |
| JSON/XML 解析 | [`internal/packagestate/`](internal/packagestate/) |
| 前置检查、共享锁和日志 | [`internal/syncstate/`](internal/syncstate/) |
| 查询超时、进程树清理和终端识别 | [`internal/query/`](internal/query/)、[`internal/terminal/`](internal/terminal/) |
| 安全阶段及定时等待提示 | [`internal/taskexec/progress.go`](internal/taskexec/progress.go)，与捕获输出分离，保持私有诊断过滤 |
| 单包清单编辑与状态复核 | [`internal/packageedit/`](internal/packageedit/) |
| 配置计划、同步与初始化共用能力 | [`internal/configops/`](internal/configops/) |
| 配置迁出、快照与事务回滚 | [`internal/configdetach/`](internal/configdetach/) |
| VS Code 与本机历史 | [`internal/extensions/`](internal/extensions/)、[`internal/confighistory/`](internal/confighistory/) |
| TOML、路径与写入命令的进程生命周期 | [`internal/localconfig/`](internal/localconfig/)、[`internal/taskexec/`](internal/taskexec/) |
| 固定 helper 版本 | [`VERSION`](VERSION) |

本机默认 Go、构建和测试的版本只在 [`../dotfiles/common/mise/config.toml`](../dotfiles/common/mise/config.toml) 的 `tools.go` 声明，读取入口为 `./helper/scripts/go-version.sh`；仅接受完整的 `major.minor.patch` 字符串，缺失、TOML 错误或非法值会在 Go 查询、安装和构建之前停止。入口使用 `mise config get --file ... tools.go` 解析仓库指定文件，不读取合并后的有效版本、不展开模板或加载私有环境；需要 Bash 和 mise，可在尚无 Go/Python、无网络的 CI 环境运行。根 `mise.toml` 不另行声明 Go 工具。修改 `.go` 源码后，在仓库根目录显式执行：

```bash
toolchain_version="$(./helper/scripts/go-version.sh)"
mise install "go@$toolchain_version"
mise run --tool "go@$toolchain_version" helper-bundle
mise run --tool "go@$toolchain_version" check
```

`check` 已包含本机构建和安装，不需要在完整验收前再运行一次 `helper-build`；开发中仅需快速构建并试用本机版本时，单独使用 `mise run helper-build`。

`check` 的默认范围随执行机器自动识别，包含公共逻辑及当前 OS、CPU 架构和 Linux profile；集成测试默认最多 2 项并发，显示阶段和逐用例耗时摘要。`check --jobs 1` 用于串行诊断，发布前或 CI 使用 `check --all-platforms` 保留完整跨平台覆盖；报告和详细日志位于 `.system-sync/check/`，见[测试说明](../tests/README.md#运行检查)。

`--tool` 为这次 task 显式选择已安装的版本，不改写项目或全局工具声明。外层 `mise exec "go@$toolchain_version" -- mise run ...` 的内层 task 会重新计算工具环境，不能代替内层的显式选择。直接调用脚本时可使用 `mise exec "go@$toolchain_version" -- ./helper/scripts/build.sh`。构建与 Go 测试脚本均严格检查当前编译器是否与 `tools.go` 一致；`GOTOOLCHAIN=local` 禁止自动切换工具链，Go 模块网络保持关闭。完整检查还需要 zsh 和支持 Go race 检查的本机 C 工具链，无需 Python。

通过 `mise upgrade --global --bump --no-prune go` 更新受管 `tools.go` 后，workflow、脚本、测试替身和错误提示会自动读取新值，无需另改版本文件。升级不会自动重建预编译包；需按上述流程重建四平台包、运行检查并刷新相应 mise 锁文件，将配置、锁文件、归档及校验清单一同审阅提交。直接编辑版本时同样适用。`go.mod` 的 `go` 声明用于模块兼容性要求，不承担精确构建版本选择。

构建使用 `CGO_ENABLED=0`、`-trimpath` 和固定版本注入，关闭自动 VCS 信息捕获；`REVISION` 记录 Git 提交及是否有本地改动，不嵌入本机配置或绝对源码路径。`helper-build` 构建并启用本机版本；`helper-bundle` 更新随仓库保存的四平台归档。修改生产源码不会隐式重编译，生产源码与新的 `helper/prebuilt/<版本>/` 应一同提交。`dev/` 的检查运行器和集成测试不进入生产分发包；仅修改开发检查时无需重建预编译包。

## 生成和安装预编译产物

```bash
# 开发机更新随 clone 保存的四平台包及校验清单
toolchain_version="$(./helper/scripts/go-version.sh)"
mise run --tool "go@$toolchain_version" helper-bundle

# 可选：开发机或 CI 导出四平台包到被 Git 忽略的目录
mise run --tool "go@$toolchain_version" helper-dist

# 目标机器使用仓库内的包，不需要编译器
mise run helper-install

# 也可从其他经过核对的本机目录安装同版本产物
mise run helper-install /绝对路径/产物目录

# 核对当前二进制版本
./helper/scripts/run.sh --version
```

`helper-bundle` 写入 `helper/prebuilt/<helper 版本>/`；`helper-dist` 写入 `.system-sync/dist/<helper 版本>/`。文件名为 `system-sync-helper_<版本>_<系统>_<架构>.tar.gz`，目标为 `darwin_arm64`、`darwin_amd64`、`linux_arm64`、`linux_amd64`，作为普通 Git 文件保存。归档只携带二进制、版本、构建 revision、内部 SHA256、Go 许可文本 `LICENSE-Go` 和 TOML 解析库许可 `LICENSE-go-toml`。跨平台构建方式见 [Go 构建文档](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies)和[目标平台变量](https://go.dev/doc/install/source#environment)。

每批产物同时生成外层 `SHA256SUMS` 和 `SOURCE_SHA256SUMS`。后者以 `helper/` 为基准，记录非测试 Go 源码、Go 模块文件、vendored 依赖、helper 版本、Go 读取入口、构建脚本、共享的 `../scripts/task-output.sh` 和许可等公共构建输入。配置只记录 `tools.go` 的语义值：`../dotfiles/common/mise/config.toml#tools.go` 条目是“版本字符串 + 换行”的 SHA256，不是整个 TOML 文件的摘要。其他合法配置、注释及排版变化不会触发重建，也不收集私有环境、本机历史或日志。

`./helper/scripts/verify.sh` 需要 mise 解析版本，无需 Go/Python 即可校验四平台归档及源码输入。`check` 的第一步通过 `verify.sh --current-platform` 只核对执行机器对应的归档，`check --all-platforms` 和 CI 的独立归档校验保留四平台检查。Go 版本、源码或其他构建输入改变但未更新分发包时会停止并提示运行 `helper-bundle`。它用于防止遗漏更新，不是可重现构建的证明。

`helper-build` / `helper-dist` / `helper-bundle` 在开始编译每个平台前报告目标，静默编译超过 10 秒时继续显示等待提示；失败指出目标及原退出码，结束临时构建且不发布本批产物。`check` 的 Go 测试启用详细结果，并在编译测试准备阶段持续反馈。`helper-install` 显示归档校验、解包及安装锁等待，默认不联网、不编译。上述进度写入 stderr。

安装器默认从**随仓库保存的本机目录**读取归档，不下载工具、不编译源码。它校验外层归档 SHA256、内部二进制 SHA256、固定版本、协议和当前平台，拒绝额外路径、链接及不支持的归档结构。归档和校验清单应来自同一次可信构建；SHA256 用于检测文件损坏，不替代发布者身份认证。

成功安装位于 `.system-sync/helper/<版本>/<二进制 SHA256>/`，然后原子更新 `current` 指针；失败时保留之前激活的版本。启动入口每次校验固定版本和二进制 SHA256，并显式传入仓库路径，不依赖调用时所在目录。只有尚未初始化时自动安装；已有记录过期或校验失败时停止，避免覆盖异常现场。更新 `helper/VERSION` 后运行 `mise run helper-install` 启用仓库的新版本；同版本更新分发包后也需显式安装才能切换已有缓存。旧构建保留在本机，可在不使用时自行清理对应目录。

CI 在准备编译器前校验并启动仓库内的预编译工具，再通过 `go-version.sh` 读取 `tools.go`，显式安装并选择该 Go 运行 macOS/Linux 两组 `check --all-platforms`，不再配置 Python 矩阵；产物 job 使用同一读取入口。CI 的全局配置为空，不依赖手工把 mise 安装目录写入 `GITHUB_PATH`；回归测试在隔离 HOME 下执行 workflow 的实际调用，覆盖没有 Go 路径、已有 mise Go 路径及旧系统 Go 三种环境，并用临时版本验证安装、测试、构建和错误提示随 `tools.go` 改变、非法配置在 Go 查询或安装前停止。通过后重新构建四平台包，上传 `system-sync-helper-<提交 SHA>` artifact，仅包含归档和校验清单。当前工作流见 [check.yml](../.github/workflows/check.yml)，不会自动创建 Release、推送代码或打包本机历史、私有配置和日志。普通 clone 已有运行所需的包，不依赖 GitHub artifact 或额外下载。

## 验证与后续工作

`mise run check` 先校验仓库分发包，再显式重建本机 helper、检查前置版本和 task 配置，运行 Go race 检查和 Go 集成测试。Go 覆盖安全解析、查询超时清理、并发和共享状态、锁冲突与继承、日志状态；CLI 测试使用临时 HOME、合成私有值和替身命令，将 Go 的完整报告与迁移前冻结的 21 份独立基线核对。预编译回归另覆盖空缓存首次调用、并发初始化、禁用 Go/Python/下载命令、JSON 输出、四平台归档校验、源码漂移及损坏拒绝执行。

2026-10-23 本机 macOS ARM64 的 `v0.2.0` 已通过 103 项 Go 单元测试（含 race 检查）、201 项 Go 集成回归、24 项 task 校验及 `go vet`。扩展采集回归覆盖新增项及已纳管版本的选择与排除、默认公共分组与显式平台范围、原分组更新、缺失项和注释保留、实际版本双向采集与同步降级提示、权限和备份、重复执行、并发修改及锁冲突；真实 mise 的合并入口在隔离 HOME 中通过。安装与同步输出回归覆盖五平台实时 stdout/stderr、机器初始化、扩展安装过程、失败退出码、阶段顺序及安全等待提示；计时进程启动后的信号边界回归确保快速命令不会遗留后台 sleep 或未关闭的管道。输出回归覆盖清单外扩展的版本明细与保留、长列表完整展开与脱敏，以及历史登记/保存/跳过的路径。机器接入覆盖五平台采集与补装、保留清单外软件和 orphan、重复执行、固定版本阻止误装、失败与并发检查；另有 7 项接入 CLI 回归，包含真实 mise 在临时 HOME 中部署、私有键保留及备份核对。原有隔离部署、扩展安装和本机历史测试继续调用 Go；旧 Python 参考实现与测试文件已移除，真实 CLI 回归由 Go 执行；禁用 Go、Python CLI 和下载命令的接入验收通过。真实 Mac 只读预览已运行，未在宿主执行初始化或扩展采集 apply。

四个平台归档、内外两层 SHA256、目标格式及 Linux 静态链接均已核对，压缩包合计约 6.4 MiB；模块缓存为空且关闭网络时，仍可从 `helper/vendor/` 构建。此前真实只读审计与 Python 参考报告逐字段一致。上述验证均未修改本机真实软件包、扩展或用户配置；本次本机验证不代表远端 CI 结果，历史已提交版本的检查结果见 [dev CI](https://github.com/sweard/system_sync/actions/workflows/check.yml?query=branch%3Adev)。

本轮另有 8 项只读及维护入口反馈回归：用阻塞替身验证审计、采集、状态/差异/历史和迁出规划在原生命令结束前就显示进度，JSON 预览保持可解析且安静；覆盖原生命令失败与不可识别版本的分类、帮助命令失败退出、构建目标和退出码、捕获数据隔离，以及 Bash 3.2 严格模式下快操作及时退出、不遗留计时器。Go 测试覆盖捕获查询超时及错误身份保留，原生安装不套用查询超时，扩展查询与安装分别使用独立时限。

日常管理、检查运行器、外部命令替身及集成测试均已完成 Go 迁移，项目不再依赖 Python。当前 Linux 原生包管理器的真实主机验收仍待完成，交叉编译成功不代表已完成真实 Linux 系统安装/卸载验收。
