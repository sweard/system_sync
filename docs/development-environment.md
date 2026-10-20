# 全局开发环境

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。全局开发环境由 `dotfiles/common/mise/config.toml` 和平台覆盖配置管理，部署后在仓库外也生效；根 `mise.toml` 管理仓库任务与配置部署。系统软件入口见[系统软件包](packages.md)，应用偏好见[配置接管](dotfiles.md)。

## 开发工具版本（已启用）

全局默认版本以 [公共配置](../dotfiles/common/mise/config.toml) 和 [macOS 覆盖](../dotfiles/macos/mise/config.toml) 为准，不在文档中重复维护版本号。

| 工具 | 管理方式 |
|---|---|
| Node | mise 安装和选择版本，停用 nvm 初始化；保留原有其他版本和全局 npm 工具 |
| Python | mise 安装和选择解释器；Apple Python 及其原有用户依赖保留 |
| Ruby | mise 管理默认版本；Homebrew Ruby 可作为 CocoaPods 依赖保留 |
| Java | `mise link` 注册现有外部 JBR，保留其他 IDE JDK |
| Rust | mise 内置 rustup 后端；保留 Cargo CLI、rust-src 和交叉编译目标 |
| Bun | mise 管理，不再由 Homebrew 直接管理 |
| uv | mise 管理工具版本；uv 负责 Python 项目依赖和虚拟环境 |
| Yarn Classic | 使用 `npm:yarn` 后端，不另启用 Corepack Yarn |

这些配置已针对当前 Mac 核对；真实 Linux 主机仍须验证。macOS 的 Rust 额外组件和目标在 `dotfiles/macos/mise/config.toml` 中声明，避免将 Apple 目标强加给 Linux。

初次迁移时，Node 和 Python 通过 mise 官方同步命令复用旧安装；后续升级版本已安装到 mise 自身目录。删除任何旧安装前，先用 `mise ls --current` 核对实际安装路径和链接。Java 仍使用 `mise link` 注册外部 JBR，其安装和更新由原来源负责；新机器需要先准备并注册该 JBR，不能仅凭版本选择声明假定能自动重建。参见 [Node 迁移](https://mise.jdx.dev/lang/node.html#migrating-from-nvm-nodenv-or-homebrew)、[Python 迁移](https://mise.jdx.dev/lang/python.html#migrating-from-pyenv-or-uv)和[已有 JDK 接入](https://mise.jdx.dev/lang/java.html#use-a-jdk-installed-elsewhere)。

Python 的 `UV_PYTHON` 指向 mise 选择的解释器，`UV_PYTHON_DOWNLOADS=never` 禁止 uv 再下载另一套 Python。项目需要其他 Python 时，在项目 mise 配置或已启用识别的 `.python-version` 中声明，再运行 `mise install`；项目版本覆盖全局默认。Apple Python 的原有用户库继续留在其 3.9 环境中，不会自动迁移到 mise 选择的其他版本；旧脚本需要它时显式使用 `/usr/bin/python3`，新项目通过 `uv sync` 安装自己的依赖。

全局启用 `.nvmrc` / `.node-version`、Python、Ruby、Java 和 Rust 的原生版本文件识别，保留项目级版本选择；Node 不从 `package.json` 自动切换。Rust 仍使用 rustup 作为 mise 的底层安装器，不再由独立的默认通道决定 mise 环境中的版本。参见 [Rust 后端](https://mise.jdx.dev/lang/rust.html)。

首次使用工具配置，先部署对应全局 mise 映射并信任，再安装和核对：

```bash
mise run config-dry-run
mise run config-sync
mise trust --all
mise install
mise ls --current
mise exec -- node --version
mise exec -- python3 --version
```

本仓库的 `settings.task.run_auto_install = false` 禁止任务先自动补装工具，状态和审计入口因此能够报告缺失项。需要补装时显式运行 `mise install`。版本、其他安装来源和实际 PATH 的汇总检查见[只读审计](audit.md)。

### 安装与升级的区别

`mise install` 按已有声明补齐工具，不修改版本声明；固定完整版本号的工具使用 `mise upgrade --bump` 才能升级并更新声明。例如逐个升级 uv：

```bash
mise upgrade --global --bump --no-prune --dry-run uv
mise upgrade --global --bump --no-prune uv
```

`--global` 只处理全局工具；`--bump` 允许超出当前版本范围，可能跨大版本；`--no-prune` 保留旧版本。将 `uv` 替换为 `node`、`python`、`rust`、`bun` 或 `npm:yarn` 即可逐项处理。当前外部 JBR 单独按原来源处理。具体规则见 [mise upgrade](https://mise.jdx.dev/cli/upgrade.html)。

全局配置通过 symlink 指向仓库，升级或执行 `mise use -g` 会修改受管来源。验证现有项目后，检查并提交版本变更：

```bash
git diff -- dotfiles/common/mise/config.toml dotfiles/macos/mise/config.toml
```

Python 的 pandas、openpyxl 等第三方依赖属于解释器环境或项目虚拟环境，升级 Python 不会自动迁移这些包。项目应记录依赖，再通过 uv 重建环境。工具完全迁出 Homebrew 的声明后，不再用 `mise run package-install` 为同一工具新增系统包声明。

Flutter 在核对时存在本地工作树改动，当前保留原安装；迁移前先保存并确认这些改动。


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

mise 通过原生 [`env._.source`](https://mise.jdx.dev/environments/#env-source) 加载共用文件中的 SDK 环境。脚本只前置存在的目录并保留原 PATH 后缀，路径包含空格也能正常处理；缺失 SDK、旧 `tools`、Gem 等目录自动跳过。用户路径函数由 zsh 在 mise 激活前调用，不作为 mise 的自定义 PATH 前置项，防止 `~/.local/bin` 等目录遮蔽项目工具。实际命令保持由 mise 选择开发工具、git-ai 提供 Git、原 Flutter 目录提供 Flutter/Dart。

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
