# 全局开发环境

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。全局开发环境由 `dotfiles/common/mise/config.toml` 和平台覆盖配置管理，部署后在仓库外也生效；根 `mise.toml` 管理仓库任务与配置部署。系统软件入口见[系统软件包](packages.md)，应用偏好见[配置接管](dotfiles.md)。

## 开发工具版本（已启用）

全局默认版本以 [公共配置](../dotfiles/common/mise/config.toml) 和 [macOS 覆盖](../dotfiles/macos/mise/config.toml) 为准，不在文档中重复维护版本号。

| 工具 | 管理方式 |
|---|---|
| Node | mise 安装和选择版本，停用 nvm 初始化；保留原有其他版本和全局 npm 工具 |
| Python | mise 安装和选择解释器；Apple Python 及其原有用户依赖保留 |
| Go | mise 管理 SDK 和默认版本；项目依赖、构建缓存及 `go install` 的命令另行维护 |
| Ruby | mise 管理默认版本；Homebrew Ruby 可作为 CocoaPods 依赖保留 |
| Java | `mise link` 注册现有外部 JBR，保留其他 IDE JDK |
| Rust | mise 内置 rustup 后端；保留 Cargo CLI、rust-src 和交叉编译目标 |
| Bun | mise 管理，不再由 Homebrew 直接管理 |
| uv | mise 管理工具版本；uv 负责 Python 项目依赖和虚拟环境 |
| Yarn Classic | 使用 `npm:yarn` 后端，不另启用 Corepack Yarn |
| Flutter / Dart | mise 固定 Flutter 版本，Dart 使用同一 SDK；当前机器链接原 SDK，新机器从官方归档安装 |

这些配置已针对当前 Mac 核对；真实 Linux 主机仍须验证。macOS 的 Rust 额外组件和目标在 `dotfiles/macos/mise/config.toml` 中声明，避免将 Apple 目标强加给 Linux。

初次迁移时，Node 和 Python 通过 mise 官方同步命令复用旧安装；后续升级版本已安装到 mise 自身目录。删除任何旧安装前，先用 `mise ls --current` 核对实际安装路径和链接。当前 Java 继续链接外部 JBR；同一精确版本已验证可以通过 mise Java 后端重装，新机器无需复制旧路径。下载锁和重建步骤见[新机器重建](rebuild.md)。参见 [Node 迁移](https://mise.jdx.dev/lang/node.html#migrating-from-nvm-nodenv-or-homebrew)、[Python 迁移](https://mise.jdx.dev/lang/python.html#migrating-from-pyenv-or-uv)和[已有 JDK 接入](https://mise.jdx.dev/lang/java.html#use-a-jdk-installed-elsewhere)。

Python 的 `UV_PYTHON` 指向 mise 选择的解释器，`UV_PYTHON_DOWNLOADS=never` 禁止 uv 再下载另一套 Python。项目需要其他 Python 时，在项目 mise 配置或已启用识别的 `.python-version` 中声明，再运行 `mise install`；项目版本覆盖全局默认。Apple Python 的原有用户库继续留在其 3.9 环境中，不会自动迁移到 mise 选择的其他版本；旧脚本需要它时显式使用 `/usr/bin/python3`，新项目通过 `uv sync` 安装自己的依赖。

全局启用 `.nvmrc` / `.node-version`、Python、Ruby、Java 和 Rust 的原生版本文件识别，保留项目级版本选择；Node 不从 `package.json` 自动切换。Rust 仍使用 rustup 作为 mise 的底层安装器，不再由独立的默认通道决定 mise 环境中的版本。参见 [Rust 后端](https://mise.jdx.dev/lang/rust.html)。

首次使用工具配置，先部署对应全局 mise 映射并信任，再安装和核对：

```bash
mise run config-dry-run
mise run config-sync
mise trust --all
mise install --locked
mise ls --current
mise exec -- node --version
mise exec -- python3 --version
```

本仓库的 `settings.task.run_auto_install = false` 禁止任务先自动补装工具，状态和审计入口因此能够报告缺失项。需要补装时显式运行 `mise install`。版本、其他安装来源和实际 PATH 的汇总检查见[只读审计](audit.md)。

### 常用工具命令速查

| 用途 | 命令 |
|---|---|
| 查看当前目录生效的工具、版本和配置来源 | `mise ls --current` |
| 查看 Go 的已安装版本和路径 | `mise ls go` |
| 查看 mise 选择的 Go 可执行文件 | `mise which go` |
| 用当前 mise 环境核对版本 | `mise exec -- go version` |
| 查看终端实际解析到的命令 | `command -v go` |
| 查看 mise 生成的 PATH | `mise env --paths` |
| 新增全局工具并固定本次解析的版本 | `mise use --global --pin go@latest` |
| 补装当前配置已声明的工具 | `mise install` |
| 按已有下载锁安装 | `mise install --locked`，限制与例外见[新机器重建](rebuild.md) |
| 预览全局工具升级 | `mise upgrade --global --bump --no-prune --dry-run go` |
| 取消全局声明并清理不再被其他配置使用的安装 | `mise unuse --global go` |
| 仅清理 Go 的 mise 缓存 | `mise cache clear go` |

以下用 Go 举例；其他工具使用其声明名，例如 `node`、`python`、`uv`、`bun` 或 `npm:yarn`。`mise ls` 与 `mise which` 用于核对 mise 的选择，`command -v` 用于核对当前 Shell 的实际来源。

### 新增工具：全局与项目配置

日常全局工具使用 `--global`（简写 `-g`）。本项目已将 `~/.config/mise/config.toml` 链接到仓库来源；新增 Go 会写入 `dotfiles/common/mise/config.toml`：

```bash
# 安装最新可用版本，并把解析出的完整版本号写入全局配置
mise use --global --pin go@latest

# 或指定完整版本；已安装时复用现有安装
mise use --global --pin go@1.27.3
```

`mise use` 会安装缺失版本并写入配置。不带 `--global` 时，它默认写入最近的项目配置；没有项目配置时在当前目录创建配置。在本仓库运行时可能改到根 `mise.toml`。需要项目独立版本时，到该项目执行 `mise use --pin go@latest`，或用 `--path /path/to/project/mise.toml` 显式指定目标。

`dotfiles/common/mise/miserc.toml` 管理配置发现规则；工具版本写入 `config.toml`。已有工具在全局平台覆盖文件中声明时，`--global` 会更新声明所在的文件，改动后核对实际差异。参见 [mise use 的写入规则](https://mise.jdx.dev/cli/use.html)。

如果误将 Go 写入本仓库根 `mise.toml`，先完成上述全局声明，再只删除项目声明，保留安装：

```bash
mise use --path ./mise.toml --remove go
mise lock --global go
mise ls --current
```

`--pin` 决定写入形式，`@latest` 决定选择范围。`--pin go@latest` 会选择当前允许的最新版本，再写入完整版本号；`mise use --global go@latest` 通常写入 `go = "latest"`（启用全局 `pin` 设置时除外）。版本选择仍遵守 `minimum_release_age` 等策略，已有锁文件也会影响解析；`latest` 不代表每次打开终端都会自动升级。

### 安装、升级与锁文件

`mise install` 按已有声明补齐工具，不新增或修改版本声明。临时安装指定版本但不加入配置时，可以执行：

```bash
mise install go@1.27.3
mise exec go@1.27.3 -- go version
```

仅安装的版本不会自动成为全局默认。使用 `mise upgrade` 更新固定完整版本号的声明时，需加 `--bump`，例如逐个升级 uv：

```bash
mise upgrade --global --bump --no-prune --dry-run uv
mise upgrade --global --bump --no-prune uv
```

`--global` 只处理全局工具；`--bump` 允许超出当前版本范围，可能跨大版本；`--no-prune` 保留旧版本。将 `uv` 替换为 `node`、`python`、`rust`、`bun` 或 `npm:yarn` 即可逐项处理。当前外部 JBR 单独按原来源处理。具体规则见 [mise upgrade](https://mise.jdx.dev/cli/upgrade.html)。

新增或调整版本后，刷新对应工具的全局下载锁；只刷新锁文件不会安装工具：

```bash
mise lock --global go
mise lock --global --sidecars
```

`mise lock` 默认保留仍匹配声明的已锁版本。`mise lock --global --bump go` 可以重新解析 `latest` 或版本范围，但不会改写固定版本声明；固定版本升级继续使用 `mise upgrade --bump`。多平台下载锁和依赖 sidecar 的维护见[新机器重建](rebuild.md#全局锁文件)。

全局配置和锁文件通过 symlink 指向仓库，`mise use --global`、升级和锁文件操作都会影响受管来源。验证相关项目后，一并审阅配置、锁文件及依赖 sidecar；确认后再提交：

```bash
git status --short
git diff -- dotfiles/common/mise dotfiles/macos/mise
```

### 卸载工具与清理缓存

日常取消全局工具使用 `mise unuse --global go`：删除全局声明，并清理没有其他已跟踪配置需要的安装。仅运行 `mise uninstall` 会保留声明，后续 `mise install` 可以重新装回。参见 [mise unuse](https://mise.jdx.dev/cli/unuse.html) 和 [mise uninstall](https://mise.jdx.dev/cli/uninstall.html)。

需要完全移除 mise 安装的所有 Go 版本时，先用 `mise ls go` 核对安装版本，并确认其他项目是否仍需要 Go；其他项目的版本声明也应按需调整。若还要清理 Go 自身缓存，在卸载前检查路径并执行：

```bash
# 先核对模块、构建缓存、命令和用户配置的实际位置
go env GOPATH GOBIN GOCACHE GOMODCACHE GOENV GOTELEMETRYDIR

# 可选：清理当前用户共享的 Go 缓存，会影响其他 Go 项目的缓存
go clean -cache -testcache -modcache -fuzzcache
```

删除声明、全部 Go 安装，并整理锁文件及 mise 缓存：

```bash
# 先只删除全局声明，保留安装供下一条命令统一卸载
mise unuse --global --no-prune go

# 预览后卸载 Go 的所有版本
mise uninstall --all --dry-run go
mise uninstall --all go

mise lock --global
mise cache clear go
mise reshim
```

`--all` 后保留工具名 `go`；省略工具名会卸载所有工具。`mise cache clear go` 只清理 Go 的 mise 缓存，不删除安装、项目源码或 Go 自身的数据，见 [mise cache clear](https://mise.jdx.dev/cli/cache/clear.html)。

如需清除下载归档和遥测记录，确认目录后再清理。以下是本机 macOS 使用默认 mise 数据目录时的路径；自定义了 `MISE_DATA_DIR` 或 Go 路径时，应改用实际路径：

```bash
rm -rf -- "$HOME/.local/share/mise/downloads/go"
rm -rf -- "$HOME/Library/Application Support/go/telemetry"
```

`go install` 的命令位于 `GOBIN`，未设置时通常位于 `GOPATH/bin`；按前面记录的路径逐项清理。`GOENV` 指向的用户配置同样单独处理。不要整目录删除 `GOPATH`（通常为 `~/go`），其中可能有源码。缓存与遥测的定义见 [Go 命令文档](https://pkg.go.dev/cmd/go)和[Go telemetry](https://go.dev/doc/telemetry)。

最后检查安装记录、终端来源和仓库差异：

```bash
mise ls go
command -v go
git diff -- dotfiles/common/mise dotfiles/macos/mise
```

未找到 `go` 时，`command -v` 无输出并返回非零状态；仍有输出时，继续按显示路径核对 Homebrew 或其他安装来源。通过 `mise link` 接入的外部 SDK 另按原来源处理，移除注册时应保留仍需使用的原 SDK。

### 项目依赖与现有 SDK

Python 的 pandas、openpyxl 等第三方依赖属于解释器环境或项目虚拟环境，升级 Python 不会自动迁移这些包。项目应记录依赖，再通过 uv 重建环境。工具完全迁出 Homebrew 的声明后，不再用 `mise run package-install` 为同一工具新增系统包声明。

Flutter 已接入 mise，仍复用原 SDK。迁移前已在 `.system-sync/history/*-roadmap/` 备份原 `pubspec.lock` 和二进制 Git 补丁，未重置 SDK。Flutter / Dart 均由 mise 的同一工具目录解析，公共 PATH 不再显式添加旧 SDK 目录。

使用现有 Flutter 项目的 `master` 隔离副本完成了 `flutter pub get`、`flutter analyze --no-pub` 和 `flutter test --no-pub`。当前 SDK 的分析器在含中文的隔离路径发生 JSON 解析崩溃，改用 ASCII 临时路径后三项均通过；原项目只读，未修改代码、分支或未提交文件。SDK 升级与跨平台构建另行验收。


## zsh（已启用）

2026-10-20 对比本机实际配置后接管以下配置，全部使用 `symlink`：

| 目标 | 来源 |
|---|---|
| `~/.zshenv` | `dotfiles/common/zsh/zshenv` |
| `~/.zshrc` | `dotfiles/common/zsh/zshrc` |
| `~/.p10k.zsh` | `dotfiles/common/zsh/p10k.zsh` |
| `~/.zprofile` | 当前平台的 `dotfiles/macos/zsh/zprofile` 或 `dotfiles/linux/zsh/zprofile` |
| `~/.config/zsh/platform.zsh` | 当前平台的 `platform.zsh` |
| `~/.config/zsh/path.sh` | `dotfiles/common/shell/path.sh`，与 mise 共用 |

所有 zsh 实例先读取 `.zshenv`；登录 Shell 还读取 `.zprofile`，交互 Shell 读取 `.zshrc`。参见 [zsh 启动文件顺序](https://zsh.sourceforge.io/Doc/Release/Files.html#Startup_002fShutdown-Files)。

- `.zshenv` 从共用文件加载现有目录，保持非交互脚本安静，不加载插件或运行 mise，也不再加载旧的 `~/.cargo/env`。
- `.zprofile` 初始化 Homebrew，再恢复共用 PATH。非交互登录 Shell 执行 `mise env -s zsh`，一次加载工具、公共环境和私有变量；交互登录 Shell 留给 `.zshrc` 激活。
- `.zshrc` 加载平台模块、原有 Zinit 插件、Powerlevel10k、本机函数/别名和可选 OpenClaw 补全，在末尾恢复共用 PATH 并执行一次 `mise activate zsh`。git-ai 和 `.local/bin` 保持用户命令优先级；mise 选择的开发工具优先于用户目录中的同名命令，PATH 用 zsh 唯一化数组去重。

macOS locale 已移入 mise 平台配置，保留原值；Android SDK 默认使用 `~/Library/Android/sdk`，Linux 默认使用 `~/Android/Sdk`，已设置的 `ANDROID_HOME` 优先。Linux 使用可选 Linuxbrew，不照搬 macOS locale。不存在的 Android `tools` 目录不会加入 PATH。当前 Mac 的 Powerlevel10k 配置按原文件导入，保留原有外观；Zinit 已下载的插件、历史文件、补全缓存和 instant-prompt 缓存仍由本机维护，不纳入 Git。

原 `.zshrc` 中的 `CASE_SUBMITTER`、`DASHBOARD_HOST` 已从 `~/.zshrc.local` 迁入仓库外的 `~/.config/mise/secrets.env`，原值和 `0600` 权限保留。`~/.zshrc.local` 继续作为本机函数/别名入口，不由 mise 映射。新的机器没有这些私有文件时会正常跳过；需要同步时，通过安全渠道复制并设置权限。

首次接管已有普通文件前，先备份并预览目标；需要替换时只对冲突项执行 `mise bootstrap dotfiles apply --force`。当前 Mac 原 zsh 文件和接管记录位于 `.system-sync/history/*-zsh-*/`，本次环境迁移记录位于 `*-env-path/`。恢复时先取消对应映射，再恢复相应审计目录中的原文件与仓库来源。

以后直接编辑仓库来源文件，链接会反映修改；用 `mise run config-status` 检查映射，重新打开终端加载新配置。当前 Mac 使用真实登录终端验证，公共启动逻辑另有隔离测试；Linux 的平台文件通过语法检查，尚未在真实 Linux 主机验证。


## 环境变量和 PATH

公共声明已启用，并按用途分工：

| 来源 | 管理内容 |
|---|---|
| `dotfiles/common/mise/config.toml` | 工具版本、uv 解释器、可选私有文件和输出脱敏 |
| `dotfiles/common/shell/path.sh` | Android SDK 默认值、Toolbox/Android 目录，以及供 zsh 调用的用户 PATH 函数 |
| `dotfiles/macos/mise/config.toml` | Rust 平台参数、原有 `LANG=zh` 和 `LC_ALL=en_US.UTF-8` |
| `~/.config/mise/secrets.env` | 本机私有变量；不映射、不提交，权限 `0600` |
| `~/.zshrc.local` | 本机 Shell 函数与别名 |

mise 通过原生 [`env._.source`](https://mise.jdx.dev/environments/#env-source) 加载共用文件中的 SDK 环境。脚本只前置存在的目录并保留原 PATH 后缀，路径包含空格也能正常处理；缺失 SDK、旧 `tools`、Gem 等目录自动跳过。用户路径函数由 zsh 在 mise 激活前调用，不作为 mise 的自定义 PATH 前置项，防止 `~/.local/bin` 等目录遮蔽项目工具。实际命令由 mise 选择开发工具及 Flutter/Dart，git-ai 提供 Git。

新终端和交互目录切换由 `mise activate` 动态加载环境；非交互登录 Shell 一次加载。普通非交互 `zsh -c` 保持轻量，只继承已有环境并加载公共路径；脚本或 CI 要按当前目录解析变量和工具时使用 `mise exec -- <命令>` 或 `mise run <任务>`。用户命令路径由 zsh 提供，直接从其他程序调用 mise 时应传入需要的 PATH。仅添加 shims 无法给整个父 Shell 加载环境变量，参见[官方 Shell 配置](https://mise.jdx.dev/shell-setup.html)。

Linux 的 SDK/Toolbox 路径通过同一文件自动识别，无需部署空的 `config.linux.toml`；该文件保留为 locale 候选。先用发行版工具确认已生成的 locale，再按本机选择 `LANG` / `LC_ALL`。Linux 分支有隔离测试，尚未在真实 Linux 主机验证。

以后公共变量编辑对应的 mise 配置，公共路径编辑 `path.sh`。私有值只写入 `~/.config/mise/secrets.env`，可参考 `dotfiles/common/mise/secrets.env.example`；不存在时自动跳过。项目的 `mise.toml` / `mise.local.toml` 可以覆盖全局变量，离开项目后恢复。应用专用凭据和环境策略继续留在各自的本机配置中，不自动扩大到全局环境。

真实密钥文件和 `*.local` 继续被 Git 忽略。私有文件中所有变量已标为敏感，同时启用常见凭据名称的 `redactions`。它只遮蔽 mise 处理的日志和普通任务输出，不是加密；`mise env`（包括 `--redacted`）、`mise exec` 和 `raw` 任务仍可能输出真实值，见[官方脱敏范围](https://mise.jdx.dev/environments/secrets/#what-redaction-covers)。检查路径可使用 `mise env --paths`，不要将完整环境输出提交到仓库。

本机已核对交互和非交互登录 Shell 的工具版本、20 个命令来源、PATH 去重及私有值哈希。迁移前私有文件、仓库来源和校验记录保存在 `.system-sync/history/*-env-path/`；回退时先恢复仓库来源，再从 `zshrc.local.before` 恢复本机文件，确认后移除新建的 `secrets.env` 与 PATH 映射。


## 共享 Shell 文件的位置

`dotfiles/common/shell/path.sh` 同时供 mise 和 zsh 加载。mise 通过全局配置来源的真实路径找到相邻的 `shell/` 目录；zsh 继续通过 `~/.config/zsh/path.sh` 链接加载。目录调整只改变来源位置，SDK 识别、用户目录优先级及 mise 激活顺序保持原逻辑。

已有部署从旧来源迁移时，在仓库更新完成后先预览，再仅更新该目标：

```bash
mise bootstrap dotfiles apply --dry-run ~/.config/zsh/path.sh
mise bootstrap dotfiles apply --yes ~/.config/zsh/path.sh
```

若 mise 报告已有普通文件冲突，先检查并备份该文件，再按[恢复与冲突处理](recovery.md)执行。使用 symlink 部署的机器应在切换仓库版本时同步检查这个链接。

## 参考

- [mise Environments](https://mise.jdx.dev/environments/)
- [mise Configuration Environments](https://mise.jdx.dev/configuration/environments.html)
- [mise config trust](https://mise.jdx.dev/cli/trust.html)
