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
├── scripts/                    # 平台操作、预检、共享锁与审计
├── tests/                      # 隔离环境与模拟包管理器测试
├── docs/                       # 分主题的操作说明与后续规划
└── .system-sync/               # 本机审计与捕获状态，Git 忽略
```

包 profile 和配置部署适配器是两条独立配置链。根 `[monorepo].config_roots` 显式列出 Linux 包 profile，不依赖目录扫描，见 [mise monorepo tasks](https://mise.jdx.dev/tasks/monorepo.html)。

## 常用命令

以下命令在仓库根目录执行。运行时与完整 `check` 均要求 mise 2026.10.11+、Python 3.11+ 和 Bash，系统包管理器由系统准备。CI 在 macOS/Linux 上覆盖 Python 3.11 和 3.15；隔离测试不操作本机真实软件包。

本仓库已关闭任务运行前的自动工具补装，避免审计时先改变本机状态。缺失的开发工具通过 `mise install` 显式安装。

```bash
mise trust --all
mise run check

# 查看系统软件和配置漂移
mise run audit
mise run status
mise run dry-run
mise run config-status
mise run config-dry-run
```

| 操作 | 命令 |
|---|---|
| 汇总四类状态、来源与待纳管候选 | `mise run audit`；JSON 用 `mise run --quiet audit --json` |
| 安装 / 卸载一个系统软件并记录清单 | `mise run package-install <包名>` / `mise run package-uninstall <包名>` |
| 预览单包操作 | 在上述任务参数中加 `--dry-run` |
| 按系统软件清单安装并收敛 | `mise run system-sync`，进入交互确认 |
| 单独升级 Brewfile 声明项 | `mise run macos-upgrade` |
| 查看受管配置的实际差异，无变化时提示无需同步 | `mise run config-dry-run` |
| 部署受管配置文件 | `mise run config-sync` |
| 补齐 `[tools]` 已声明的工具版本 | `mise install` |
| 预览工具升级 | `mise upgrade --global --bump --no-prune --dry-run uv`，工具名可替换 |

Linux 清单仍是结构示例。首次整机应用前必须导入本机 baseline；Linux 统一升级尚未实现，继续使用发行版本身的升级流程。macOS 当前仍使用 Homebrew Bundle。

私有变量文件不部署、不提交；ChatGPT / VS Code 只合并来源声明的键。运行时审计保存在 `.system-sync/history/`，与 mise 可选的 dotfiles 历史功能分别管理。

## 详细文档

| 文档 | 内容 |
|---|---|
| [只读审计](docs/audit.md) | tools、packages、PATH、配置漂移及本机保留项，报告格式与检查边界 |
| [系统软件包](docs/packages.md) | 单包操作、漂移捕获、Linux baseline、平台收敛顺序及保护规则 |
| [全局开发环境](docs/development-environment.md) | 工具安装与升级、zsh、环境变量及 PATH |
| [应用配置与接管](docs/dotfiles.md) | Espanso、ChatGPT、VS Code、平台映射及日常同步 |
| [取消、冲突与恢复](docs/recovery.md) | 操作中断、链接和配置恢复、审计及 Git 管理 |
| [后续规划](docs/roadmap.md) | 核对快照、未启用功能及后续推进顺序 |
