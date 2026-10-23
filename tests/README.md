# 测试与覆盖迁移

[返回项目首页](../README.md) · [Go helper](../helper/README.md)

日常运行、构建和开发验收均无需 Python。检查运行器、外部命令替身及集成测试已迁入 `helper/dev/`，复用 helper 的 Go 模块和已纳入仓库的依赖。`tests/` 保留检查入口、逐项覆盖清单和独立报告基线。

## 运行检查

准备 `dotfiles/common/mise/config.toml` 的 `tools.go` 固定的 Go、mise、Bash，以及 Go race 检查需要的本机 C 工具链；Shell 启动测试还需要 zsh。无需安装 Python 或额外 Go 测试框架：

```bash
toolchain_version="$(./helper/scripts/go-version.sh)"
mise install "go@$toolchain_version"
mise run --tool "go@$toolchain_version" check

# CI 的完整范围：四平台归档及全部跨平台模拟场景
mise run --tool "go@$toolchain_version" check --all-platforms

# 串行诊断或指定报告路径
mise run --tool "go@$toolchain_version" check --jobs 1
mise run --tool "go@$toolchain_version" check --report .system-sync/check/measurement.json

# 显式调整集成测试并发；不改变检查阶段顺序或平台范围
mise run check --jobs 2
mise run check --jobs 4
```

`tests/check.sh` 优先选择已安装的固定 Go，再在本机缓存中编译 Go 检查运行器，结束时删除该临时程序。缺失工具仍需显式安装。运行器依次执行以下十阶段：

| 阶段 | 内容 |
|---|---|
| Go 版本与下载锁 | 对比固定版本、四平台 Go 下载锁和归档构建版本；交互终端可输入完整 `yes` 备份并同步，非交互/CI 仅检查并在差异时失败 |
| 预编译包与构建输入 | 核对当前平台归档和公共构建输入；`--all-platforms` 检查四个平台归档 |
| 本机 helper 构建与启用 | 用固定 Go 编译本机 helper，校验后启用 |
| 开发环境预检 | 直接调用 Go `runtime --check`，检查 mise 版本和平台所需命令 |
| mise task 定义 | `mise tasks validate` |
| Go 单元与 race 测试 | 检查生产模块及检查运行器；集成测试用 build tag 隔离，避免重复执行 |
| Go 集成测试 | 验收实际 CLI、Shell、mise 部署和外部命令交互，记录每项耗时及结果 |
| Go 静态检查 | `go -C helper vet -mod=vendor -tags=integration ./...`，包括集成测试源码 |
| Git 差异检查 | `git diff --check`，检查未暂存差异中的空白错误 |
| Git 待提交改动 | `git status --short`，供审阅版本配置、锁、归档及其他本地改动 |

Go 准备只处理版本或下载锁差异，源码变化、格式错误和归档损坏仍失败；无变化不建立准备备份。确认前及锁解析后会复查文件，共享锁覆盖准备写入；准备失败或可处理的中断信号会回滚锁及归档，诊断保存在 `.system-sync/check/go-prepare-*/`。准备成功后测试失败保留新产物。具体流程、取消规则和恢复边界见[Go 升级后的准备](../helper/README.md#go-升级后的准备)。四平台准备不改变默认的本机测试范围。

默认范围依据执行命令的机器识别 macOS/Linux、ARM64/x86_64，以及 Arch/Debian/Fedora/openSUSE profile，运行公共与本平台场景。公共解析及安全规则继续执行；其他平台专属包事务保留在 `--all-platforms` 中。平台标记未知、结果缺失、重复、失败或意外跳过都会导致检查失败。

集成测试以完整方法对应的 Go 子测试为单位运行，每项使用独立的临时仓库、HOME、PATH、mise 状态和命令替身，保留原子场景及清理流程。默认并发数为 CPU 数与 2 的较小值；`--jobs N` 只调整集成测试并发，不控制 mise 自身的任务并发或 Go 单元测试的包并发。执行反馈与阻塞安装等 10 项时序敏感测试在并发组结束后串行执行。上次报告中的耗时用于优先调度较慢方法。没有放宽原有 3/6/10 秒执行反馈断言。

每 10 秒显示阶段状态或集成测试完成数量；阶段结束显示耗时，最后列出最慢的 10 项。默认 JSON 报告 `.system-sync/check/latest.json` 包含范围、目标平台、阶段和逐用例结果、排除清单与日志位置。报告 schema 为 2，测试结果来自 Go 的 JSON 事件；用例标识仍沿用迁移前的标识，便于对照历史报告。原始 Go 输出按阶段保存在报告旁的独立目录，Git 忽略这些本机数据。中断会停止当前命令进程组；集成程序另追踪自身启动的进程及 Go 替身的存活租约，回收测试子进程。

已有本机 helper 时，可单独运行一组测试：

```bash
# 在仓库根目录设置公共构建缓存，关闭模块网络
export GOCACHE="$PWD/.system-sync/go/build"
export GOMODCACHE="$PWD/.system-sync/go/mod"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off

# 普通 go test 默认执行完整模拟范围；用 -run 选择一组
mise exec "go@$toolchain_version" -- go test -C helper -mod=vendor \
  -tags=integration -count=1 -parallel=2 \
  -run 'TestIntegration/parallel/test_parsers' ./dev/integration
```

## 覆盖核对

迁移起点为 `da287a6` 的全部 **201 个 Python 测试方法**。[coverage.json](coverage.json) 逐项记录旧标识、Go 替代位置、平台和串行要求。Go 集成入口逐项调用对应实现，未迁移的标识会失败；检查运行器对所选标识与实际完成结果进行完整性校验。不能只减少清单数量来消除失败。

| 迁移内容 | 方法数 | Go 位置（位于 `helper/dev/integration/`） |
|---|---:|---|
| 应用配置、miserc、配置预览/同步、历史、迁出及 Espanso | 26 | `config_test.go` |
| 审计行为及真实 task | 18 | `audit_test.go` |
| Linux 包事务、解析及运行时预检 | 35 | `converge_test.go` |
| PATH、环境变量、zsh、兼容入口及重建 | 16 | `environment_test.go` |
| 预编译安装、校验、无解释器写流程及审计完整报告 | 20 | `helper_test.go` |
| Homebrew 信任、安装、升级、取消及实时输出 | 17 | `homebrew_test.go` |
| 软件采集和机器接入 | 8 | `onboard_test.go` |
| VS Code 扩展与版本采集 | 17 | `vscode_test.go` |
| 缓存清理及错误恢复 | 14 | `clean_test.go` |
| 长时间执行反馈及信号边界 | 9 | `feedback_test.go` |
| 检查计时、平台、并发、失败、中断及 Go 准备交互 | 14 | `checkrunner_test.go`、`checkprepare_test.go` |
| 干净 CI、固定 Go 来源及错误提示 | 7 | `ci_test.go` |
| **合计** | **201** | |

原来的三项 Python 版本门槛/桥接测试已改为验收“没有 Python 时直接运行 Go 预检、只读检查不修改包状态、预检失败保留错误”。检查运行器自身的异常、跳过及子场景失败使用 Go 事件和合成 Go 测试验收；并发重叠通过标记屏障验证，时序组检查并发组已全部完成。生产逻辑的共享锁、回滚和进程超时仍由原有 Go 单元测试与 CLI 集成测试共同覆盖。

复杂的包管理器、VS Code、审计、构建和查询替身由 `mock_test.go` 及 `feedback_test.go` 的 Go 实现处理。测试程序可作为替身子进程启动，不调用 Python；仅保留用于 PATH、zsh、Shell 心跳及命令参数验证的最小 Shell 夹具。交互确认直接使用 Go 的 macOS/Linux PTY 适配，不依赖额外终端工具。

通过管道执行的夹具命令使用独立进程会话，避免继承调用终端的作业控制状态。交互 zsh 启动回归会额外在拥有控制终端的隔离会话中重跑，验收从终端运行 `check` 时不会因后台进程组被暂停而超时；未放宽原有 45 秒超时或降低默认并发数。

## 分工与依赖

| 位置 | 职责 |
|---|---|
| `helper/internal/*/*_test.go` | 生产 Go 解析、安全、锁、日志、原子写入、回滚和子进程清理；完整检查启用 race |
| `helper/dev/check/`、`helper/dev/checkrun/` | 检查 CLI、平台选择、阶段执行、计时、结果完整性和报告；含独立 race 回归 |
| `helper/dev/integration/` | 真实 CLI、Shell、mise 的隔离验收及 Go 外部命令替身；使用 `integration` build tag |
| `tests/check.sh` | 固定 Go 的开发检查启动入口 |
| `tests/coverage.json` | 201 项迁移覆盖对照，不是待执行的 Python 文件列表 |
| `tests/fixtures/audit/reports.json` | 迁移前冻结的 21 份完整 JSON/文字报告基线 |

fixture 直接解析受管 TOML 中的 `tools.go` 作为预期值；构建和实际 workflow 继续通过 Bash 入口调用 mise 的 TOML 解析器。回归覆盖修改工具版本、错误类型、非法 TOML、模板、范围值、缺少 mise、全局/本地覆盖、环境变量干扰，以及不加载私有环境、不安装工具。

Go 准备的单元与 race 回归覆盖下载锁版本/绑定/URL/SHA256、无变化、严格确认、非交互拒绝、源码与归档异常、共享锁、确认及解析期间的并发变化、各阶段失败回滚和中断。使用实际 mise 的隔离锁解析回归保留其他工具记录；检查运行器集成场景还验收已安装版本选择、管道、CI 终端、取消、确认后继续十阶段以及构建失败时停止并回滚。新增场景扩展现有检查运行器用例，原 201 项迁移覆盖保持。

`helper/dev/` 仅为开发检查使用，日常 helper 不导入这些包；生产预编译输入仍只覆盖生产源码和构建脚本。只改开发测试、检查运行器和文档，无需重建四平台生产归档。CI 保留 macOS/Ubuntu 两个操作系统检查及四平台产物构建，移除 setup-python 与 Python 版本矩阵。Linux 原生包管理器的真实软件事务仍待真实主机验收，模拟测试和交叉编译不能代替该验收。

## 本轮验收与耗时

2026-10-23 在 macOS ARM64 上通过 103 项 Go 单元测试（含 race）、201 项完整集成测试、24 项 task 和四平台归档校验；报告逐项包含原来的全部 201 个标识，冻结审计基线未修改。移除 Python 文件后，另在隔离 HOME、空 mise 全局配置和禁止调用 `python` / `python3` 的 PATH 下通过默认范围 175 项集成测试，未触发解释器替身。

| 本机采样 | 集成方法数 | 六阶段总耗时 |
|---|---:|---:|
| 迁移前 Python，完整范围 | 201 | 176.20s |
| 本轮 Go，完整范围 | 201 | 133.75s |
| 迁移前 Python，默认本机范围 | 175 | 120.98s |
| 本轮 Go，默认本机范围且禁用 Python | 175 | 86.22s |

采样包含构建、race 和集成测试；编译缓存及隔离环境不同，用于观察本机检查耗时。之后针对无变化时的文件时间戳、检查入口的工具链错误提示、平台筛选及中断回收完成定向回归；`go vet -tags=integration` 及 macOS x86_64、Linux ARM64/x86_64 的集成测试编译通过。Linux 原生执行结果由后续 CI 验收。

接入 Go 准备后，同日 macOS ARM64 的默认 `check` 十阶段通过，选中 175 项、排除 26 项，总耗时 92.04s；另行通过 Linux 包事务模拟测试和 Linux ARM64/x86_64、macOS x86_64 的测试编译。Go 准备的 race、隔离 mise 锁解析、终端/CI/管道及回滚回归通过；这些隔离场景不升级本机实际 Go，也不代替真实 Linux 主机验收。

终端启动回归复现了交互 zsh 在后台进程组中被暂停、45 秒超时的问题；修复夹具会话隔离后，同一控制终端场景约 0.9s 通过。保留默认 2 并发的完整本机 `check` 再次通过 175 项、十个阶段，总耗时 93.31s；静态检查和 Linux ARM64/x86_64、macOS x86_64 的测试编译通过。

## 审计完整报告基线

[fixtures/audit/reports.json](fixtures/audit/reports.json) 在移除旧生产实现前，从提交 `4ca4a25` 的 Python 审计实现采集，并逐组与 Go 核对后冻结。7 组共 21 份完整报告继续逐字段比较 JSON，部分同时核对完整文字和退出码；不从当前被测程序生成预期值。

输入均为合成值，临时目录替换为 `<fixture>`；测试不读取本机私有配置。修改报告契约时，需要同时审阅夹具输入、完整输出差异和行为断言，再明确更新预期。
