# macOS + 多发行版 Linux 声明式系统配置同步

这个仓库通过 mise 统一管理系统软件清单、全局开发环境和应用配置。Espanso、zsh、ChatGPT 和 VS Code 已接管；应用私有设置保留在本机。

## 职责与修改入口

| 要管理什么 | 修改哪里 | 如何生效 |
|---|---|---|
| 系统软件、保护项与精确信任 | [packages/](packages/) | `mise run package-install` / `package-uninstall`，或审阅后 `system-sync` |
| 全局工具版本 `[tools]`、mise 设置 `[settings]`、环境变量 `[env]` | [公共全局配置](dotfiles/common/mise/config.toml)、[macOS 覆盖](dotfiles/macos/mise/config.toml) | 首次部署后由 mise 加载；工具安装用 `mise install`，升级见[开发环境](docs/development-environment.md) |
| SDK 默认路径和共用 PATH | [共享 Shell 脚本](dotfiles/common/shell/path.sh) | mise 与 zsh 共用，zsh 的稳定入口为 `~/.config/zsh/path.sh` |
| zsh 启动、应用偏好和片段 | [dotfiles/](dotfiles/) | `mise run config-status` / `config-dry-run` / `config-sync` |
| 仓库任务和配置部署映射 | [根 mise.toml](mise.toml)、[macOS 适配器](mise.macos.toml)、[Linux 适配器](mise.linux.toml) | 在仓库内由 mise 读取 |
| 本机私有变量和覆盖 | `~/.config/mise/secrets.env`、`~/.zshrc.local`、`mise.local.toml` | 仅保留本机，Git 忽略 |

这里有两套用途不同的 mise 配置：

- **仓库任务与部署配置**：根 `mise.toml`、`mise.macos.toml` 和 `mise.linux.toml`。负责命令入口及来源到目标的映射，根 `.miserc.toml` 自动选择平台适配器。
- **全局开发环境配置**：`dotfiles/common/mise/config.toml` 等来源部署到 `~/.config/mise/`，负责工具版本与环境，在仓库外也生效。全局 `miserc.toml` 同样由仓库部署。

`[tools]` 管理可按项目选择版本的工具；`packages/` 管理系统软件。Python 的 pandas 等项目依赖另由 uv 和项目虚拟环境管理，不会随 Python 升级自动迁移。

## 目录结构

```text
system_sync/
├── mise.toml                    # 仓库任务与公共部署映射
├── mise.macos.toml              # macOS 部署适配器
├── mise.linux.toml              # Linux 部署适配器
├── .miserc.toml                 # 仓库的平台配置发现
├── dotfiles/
│   ├── common/
│   │   ├── mise/               # 全局 tools/settings/env、miserc 和私有变量模板
│   │   ├── shell/path.sh       # mise 与 zsh 共用的路径规则
│   │   ├── zsh/                # Shell 启动、主题
│   │   ├── espanso/
│   │   ├── chatgpt/
│   │   └── vscode/
│   ├── macos/                  # 已启用的平台差异
│   └── linux/                  # 平台差异及未启用的 locale 候选
├── packages/
│   ├── macos/                  # Brewfile、保护项与精确信任
│   └── linux/                  # Arch、Debian、Fedora、openSUSE profile
├── scripts/                    # 平台 Bash 流程、配置预览和本机缓存清理
├── helper/                     # Go helper 的源码、构建与分发资源
│   ├── README.md               # helper 开发和使用说明
│   ├── cmd/system-sync-helper/ # Go CLI 入口
│   ├── internal/               # 实现及 Go 单元测试
│   ├── go.mod / go.sum         # 独立 Go 模块与固定依赖校验值
│   ├── vendor/                 # TOML 库源码与许可，构建不联网取依赖
│   ├── scripts/                # build、install、verify、test、run 及共用函数
│   ├── VERSION                 # 与预编译 helper 匹配的固定版本
│   ├── licenses/               # Go 许可文本
│   └── prebuilt/               # 随 clone 保存的四平台归档与校验清单
├── tests/                      # 隔离环境与模拟包管理器测试
│   └── reference/              # Python 行为参考、检查前置与锁兼容性代码
├── docs/                       # 分主题的操作说明与后续规划
└── .system-sync/               # 本机 helper、构建缓存和审计状态，Git 忽略
```

包 profile 和配置部署适配器是两条独立配置链。根 `[monorepo].config_roots` 显式列出 Linux 包 profile，不依赖目录扫描，见 [mise monorepo tasks](https://mise.jdx.dev/tasks/monorepo.html)。

## 常用命令

以下命令在仓库根目录执行。审计、整机包流程、单包清单编辑、配置迁出、VS Code 扩展和配置历史均已接入 Go helper；日常任务无需 Python 或 Go 编译器。首次运行自动从随仓库保存的 `helper/prebuilt/<版本>/` 校验并解压本机 helper，无需联网准备 helper。共同前置要求是 mise 2026.10.11+、Bash、tar、SHA256 校验工具和对应系统包管理器；扩展管理还需 VS Code CLI。

开发验收使用 `mise run check`，先校验仓库预编译包及源码输入，再显式构建 helper 并运行 Go 与 Python 测试，需要 Go 1.27.3、Python 3.11+ 和支持 Go race 检查的本机 C 工具链。新机器日常使用无需先运行 `check`。CI 配置覆盖 macOS/Linux × Python 3.11/3.15，并构建四个平台归档；隔离测试不操作本机真实软件包。源码、预编译安装和迁移范围见 [Go helper](helper/README.md)。

Go helper 资源集中在 [helper/](helper/)，项目级隔离回归统一放在 `tests/`，Python 参考实现位于 [tests/reference/](tests/reference/)。helper 启动器向二进制显式传入仓库根路径；本机缓存仍保存在根目录 `.system-sync/`。

本仓库已关闭任务运行前的自动工具补装，避免审计时先改变本机状态。缺失的开发工具通过 `mise install` 显式安装。

```bash
mise trust --all

# 以下按需选择；首次自动校验安装仓库内的 helper
mise run package-capture
mise run machine-init
mise run audit
mise run package-status
mise run package-dry-run
mise run config-status
mise run config-dry-run
```

| 操作 | 命令 |
|---|---|
| 预览本机根包及清单合并候选 | `mise run package-capture`；`--apply` 仅保存本机快照与候选，见[清单采集](docs/packages.md#从当前系统反向校正清单漂移) |
| 预览 / 执行机器接入，保留已有软件 | `mise run machine-init` / `machine-init --apply`；支持 `--only packages`、`config`、`tools`，见[分阶段接入](docs/rebuild.md#使用-machine-init-分阶段接入) |
| 查看系统软件声明、缺失项与额外软件 | `mise run package-status`，不运行安装或清理预演 |
| 预览系统软件安装、收敛和清理计划 | `mise run package-dry-run` |
| 汇总四类状态、来源与待纳管候选 | `mise run audit`；JSON 用 `mise run --quiet audit --json` |
| 修改 Go 源码后构建并启用本机工具 | `mise run helper-build` |
| 更新随仓库保存的四平台包及源码输入清单 | `mise run helper-bundle`，与源码一同提交 |
| 导出四平台包到被 Git 忽略的本机目录 | `mise run helper-dist` |
| 提前初始化或更新仓库版本的 helper | `mise run helper-install`；指定其他本机产物用 `mise run helper-install /绝对路径/产物目录` |
| 开发验收：校验预编译包、构建并运行完整测试 | `mise run check` |
| 预览 / 清理本机验证环境与缓存 | `mise run clean` / `mise run clean --apply`，详见[清理范围](docs/recovery.md#清理本机验证环境与缓存) |
| 安装 / 卸载一个系统软件并记录清单 | `mise run package-install <包名>` / `mise run package-uninstall <包名>` |
| 预览单包操作 | 在上述任务参数中加 `--dry-run` |
| 按系统软件清单安装并收敛 | `mise run system-sync`，进入交互确认 |
| 单独升级 Brewfile 声明项 | `mise run macos-upgrade` |
| 查看受管配置的实际差异，无变化时提示无需同步 | `mise run config-dry-run` |
| 备份、部署并复查本仓库受管配置 | `mise run config-sync`，输入 `yes`；支持 `--dry-run`、`--yes` 和目标路径 |
| 将当前配置迁为本机独立文件 | `mise run config-detach --dry-run` 预览；`mise run config-detach` 确认执行，见[配置迁出](docs/recovery.md#配置迁出保留本机文件) |
| 核对或采集 VS Code 扩展 / 按清单安装 | `mise run vscode-status` / `vscode-sync`；采集用 `vscode-status --capture` 默认预览，候选归入 `common`，平台专属项显式指定 `--group`，见[扩展采集](docs/dotfiles.md#采集本机未纳管扩展) |
| 登记本机配置历史 / 手动保存 / 查看检查点 | `mise run config-history-init` / `config-save` / `config-history`；登记、保存及跳过的目标均列出路径 |
| 查看当前生效的开发工具、版本和来源 | `mise ls --current` |
| 安装全局工具并记录完整版本号 | `mise use --global --pin go@latest`，写入受管全局配置 |
| 补齐 `[tools]` 已声明的工具版本 | `mise install` |
| 按已有下载锁安装工具 | `mise install --locked`，平台限制见[新机器重建](docs/rebuild.md) |
| 预览工具升级 | `mise upgrade --global --bump --no-prune --dry-run uv`，工具名可替换 |
| 取消全局工具声明并清理无其他配置使用的版本 | `mise unuse --global go` |

旧任务名 `status`、`dry-run`、`vscode-dry-run` 分别作为 `package-status`、`package-dry-run`、`vscode-status` 的兼容别名，参数原样转交。`audit` 汇总状态；需要审阅软件事务时再运行 `package-dry-run`。完整 helper 开发验收使用 `helper-bundle → check`，无需提前重复执行 `helper-build`。

`mise install` 不写入工具声明；`mise use` 安装并写入声明。全局与项目配置、`--pin`、锁文件、升级和完整卸载示例见[工具常用命令](docs/development-environment.md#常用工具命令速查)。

任务脚本要求确认时，统一输入小写 `yes` 继续；直接回车或其他输入取消。mise 的 Yes/No 提示选择 Yes；包管理器自己的事务确认按其提示操作。

Linux 清单尚未导入目标机器的完整 baseline，首次整机应用前必须补齐。Arch 已按 Brewfile 补充官方仓库对应软件，详见 [Arch 对照记录](packages/linux/arch/README.md)。Linux 统一升级尚未实现，继续使用发行版本身的升级流程。macOS 当前仍使用 Homebrew Bundle。

新机器或已有软件的机器，先运行 `package-capture` 和 `machine-init` 预览。初始化只补装声明缺失的软件，不降级未声明根包、不清理已有 orphan，也不自动修改清单或启用服务。配置和工具可以分阶段接管；普通文件冲突、缺失来源和重复工具管理会列为待处理项。Git、mise 和系统包管理器需先自行准备，WSL 整机备份在 Windows 侧执行。

私有变量文件不部署、不提交；ChatGPT / VS Code 只合并来源声明的键。运行时审计保存在 `.system-sync/history/`；应用配置检查点由 mise 的本机历史单独保存，不开启后台捕获或远端历史同步。

## 详细文档

| 文档 | 内容 |
|---|---|
| [只读审计](docs/audit.md) | tools、packages、PATH、配置漂移及本机保留项，报告格式与检查边界 |
| [系统软件包](docs/packages.md) | 单包操作、漂移捕获、Linux baseline、平台收敛顺序及保护规则 |
| [全局开发环境](docs/development-environment.md) | 工具命令速查、全局与项目版本、安装升级卸载、zsh、环境变量及 PATH |
| [新机器重建](docs/rebuild.md) | 公共配置重建、全局锁文件、JBR 与 Flutter 来源及验证范围 |
| [Go helper](helper/README.md) | 随仓库预编译包、离线初始化、源码与构建、Python 迁移范围 |
| [应用配置与接管](docs/dotfiles.md) | Espanso、ChatGPT、VS Code、平台映射及日常同步 |
| [取消、冲突与恢复](docs/recovery.md) | 操作中断、链接和配置恢复、审计及 Git 管理 |
| [后续规划](docs/roadmap.md) | 核对快照、未启用功能及后续推进顺序 |
