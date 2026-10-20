# macOS + 多发行版 Linux 声明式系统配置同步

这个仓库用统一的 mise task 作为入口，把各平台的系统包收敛逻辑分开，并通过全局 mise 配置管理开发工具版本。Espanso、zsh、ChatGPT 和 VS Code 已接管；ChatGPT / VS Code 只共享选定的偏好，私有设置保留在本机。

| 平台 / profile | 声明来源 | 收敛方式 |
|---|---|---|
| macOS | `packages/macos/Brewfile` | Homebrew Bundle 安装和 cleanup |
| Arch / Manjaro 系 | `packages/linux/arch/mise.toml` | pacman/AUR 安装，再降级未声明 explicit 包并清理 orphan |
| Debian / Ubuntu 系 | `packages/linux/debian/mise.toml` | APT 安装，再调整 manual/auto 并 autoremove |
| Fedora / RHEL 系 | `packages/linux/fedora/mise.toml` | DNF 安装，再调整 user/dependency 原因并 autoremove |
| openSUSE / SLE | `packages/linux/opensuse/mise.toml` | mise 原生 zypper 安装，脚本校验后 `remove --clean-deps` |

mise 原生支持 `apt`、`dnf`、`pacman`、`aur` 和 `zypper`，负责读取声明与安装缺失项；本仓库的 Linux 脚本补充未声明根包的收敛、保护校验和移除事务审阅。

> **重要：** Linux 下的清单只是结构示例，不是你已有系统的 baseline。首次 `apply` 前必须按本文导入本机的手工根包。

## 文件结构

```text
system_sync/
├── .miserc.toml                     # 在仓库内自动选择 macOS / Linux 配置环境
├── .gitignore
├── README.md
├── mise.toml                         # 公共 task 入口
├── mise.macos.toml                   # macOS dotfiles 适配器；espanso、mise、zsh 和 VS Code
├── mise.linux.toml                   # Linux dotfiles 适配器；espanso、zsh 和 VS Code
├── mise.local.toml                   # 本机私有映射（Git 忽略，不随仓库同步）
├── dotfiles/
│   ├── common/                       # 两个平台共用
│   │   ├── espanso/                 # 已启用：设置、默认示例与公共片段
│   │   │   ├── config/default.yml
│   │   │   ├── match/base.yml
│   │   │   ├── match/snippets.yml
│   │   │   ├── private.local/match/private-snippets.yml  # 私有目录，Git 忽略
│   │   │   └── mise.local.example.toml
│   │   ├── mise/
│   │   │   ├── config.toml           # 已启用的工具版本与 uv 解释器选择
│   │   │   ├── miserc.toml
│   │   │   └── secrets.env.example
│   │   ├── zsh/
│   │   │   ├── zshenv
│   │   │   ├── zshrc
│   │   │   └── p10k.zsh             # 已启用：当前提示符配置，保持原样
│   │   ├── chatgpt/                 # 已启用：通用偏好、协作规则和快捷键
│   │   │   ├── config.toml          # 按键合并；不包含本机私有配置
│   │   │   ├── AGENTS.md
│   │   │   └── keybindings.json
│   │   ├── vscode/
│   │   │   └── settings.json        # 已启用：16 项通用偏好，按键合并
│   │   └── claude/
│   │       ├── CLAUDE.md
│   │       └── hooks/
│   ├── macos/                        # 当前 Mac 脱敏快照与平台路径
│   │   ├── espanso/match/macos.yml  # 已启用：仅在 macOS 使用的 defaults 命令片段
│   │   ├── mise/config.toml
│   │   ├── zsh/
│   │   │   ├── zprofile
│   │   │   └── platform.zsh
│   │   └── claude/settings.json.tmpl
│   └── linux/                        # 安全的 Linux 候选模板
│       ├── mise/config.toml
│       ├── zsh/
│       │   ├── zprofile
│       │   └── platform.zsh
│       └── claude/settings.json.tmpl
├── packages/                         # 系统软件包声明与收敛策略
│   ├── macos/
│   │   ├── Brewfile
│   │   ├── protected-packages.txt
│   │   ├── trusted-formulae.txt
│   │   └── trusted-casks.txt
│   └── linux/
│       ├── arch/
│       │   ├── mise.toml
│       │   └── protected-packages.txt
│       ├── debian/
│       │   ├── mise.toml
│       │   └── protected-packages.txt
│       ├── fedora/
│       │   ├── mise.toml
│       │   └── protected-packages.txt
│       └── opensuse/
│           ├── mise.toml
│           └── protected-packages.txt
└── scripts/
    ├── system-sync.sh               # 统一调度入口
    ├── sync-runtime.sh              # Shell 入口共用的执行保护
    ├── sync_runtime.py              # 版本预检、共享锁与全程审计
    ├── package-command.py           # install/uninstall：原生操作成功后记录一个根包
    ├── detect-linux-profile.sh      # 解析 /etc/os-release
    ├── homebrew-converge.sh
    ├── pacman-converge.sh
    ├── apt-converge.sh
    ├── dnf-converge.sh
    ├── linux-reason-converge.sh
    ├── extract-mise-packages.py
    ├── zypper-converge.sh
    └── parse-zypper-xml.py
```

`mise.toml` 设置了 `monorepo_root = true`，并在 `[monorepo].config_roots` 中显式列出 Arch、Debian、Fedora 和 openSUSE 的包 profile。`.miserc.toml` 只负责启用 mise 的 `auto_env`：在 macOS 自动叠加 `mise.macos.toml`，在 Linux 自动叠加 `mise.linux.toml`。包 profile 与 dotfiles 平台适配器是两条独立配置链，不依赖自动目录扫描。

运行时审计清单保存在 `.system-sync/history/<UTC 时间>/`；该目录和任意层级的 `.DS_Store` 都已被 Git 忽略。

## 统一使用方法

先在仓库根目录信任根配置和 Linux 子配置：

```bash
mise trust --all
```

本仓库的验证基线为 **mise 2026.10.11+、Python 3.9+ 和 Bash**。根配置通过 `min_version` 限定 mise 版本；直接执行脚本或单包入口也会预检版本和本机必需命令。开发工具默认版本由 `dotfiles/common/mise/config.toml` 管理；首次部署全局配置后运行 `mise install`，系统包管理器等前置命令仍须由系统准备。

```bash
mise run check
```

`check` 依次检查运行前置条件、验证 mise 任务并运行隔离测试，不修改系统软件。Linux 的示例清单仍须先完成 baseline 导入，测试通过不代表本机清单已经适合整机收敛。

然后使用同一组命令：

```bash
mise run status
mise run dry-run
mise run system-sync
```

- `status`：只读地显示声明、当前根包和差异。
- `dry-run`：同时预览缺失包安装和收敛计划，不修改系统。
- `system-sync`：经 mise 和脚本精确确认后安装缺失项；删除阶段另行审阅，并交给包管理器确认。

所有包写入口共用操作锁。Linux apply 的顺序是：**终端/版本预检 → 加锁并建立审计 → 读取声明和当前状态 → 校验保护项及变更阈值 → 精确确认 → 保存安装前状态 → 原生 mise 安装 → 重新校验 → 调整安装原因及再次审阅删除事务 → 执行删除**。直接运行平台脚本同样经过保护；mise 原生安装阶段可能使用非交互参数，因此本仓库的精确确认始终在它之前。

整机操作和升级的 `transaction.json` 记录 `preflight`、`installing` / `upgrading`、`marking`、`removing` 等阶段，以及最终 `complete` / `cancelled` / `failed` / `interrupted` 状态。安装失败也会保留 `.before` 清单和安装前包列表；包装层要求平台脚本明确记录完成，不能只凭退出码 0 判定成功。审计不是系统事务回滚，失败后应先检查 `last_phase` 与实际包状态。

应用配置还提供三个独立入口：

```bash
mise run config-status
mise run config-dry-run
mise run config-sync
```

当前启用 espanso、全局 mise 配置、zsh、ChatGPT 和 VS Code 的 `[dotfiles]` 映射；mise 会选择当前平台适配器。Claude 的完整文件映射仍被注释。zsh 的私有值保留在仓库外的 `~/.zshrc.local`；ChatGPT / VS Code 使用按键合并，`config-sync` 保留来源未声明的本机设置。

Linux 使用 `/etc/os-release` 自动选择 profile。当前映射为：

- Arch、Manjaro、EndeavourOS、CachyOS → `arch`；
- Debian、Ubuntu、Linux Mint、Pop!_OS、Kali、Raspbian → `debian`；
- Fedora、RHEL、CentOS、Rocky、AlmaLinux、Oracle Linux → `fedora`；
- openSUSE Leap/Tumbleweed、SLES、SLED → `opensuse`。

其他发行版不会猜测执行，而是失败关闭。Linux 统一升级暂未实现；请继续使用发行版自身的升级流程。

## 日常添加 / 删除一个包

使用仓库提供的 **`mise run install` / `mise run uninstall`**。这里的 `install` 是本仓库 task；不要省略 `run`，`mise install` 是 mise 自带的开发工具安装命令，并不是这个入口。

在仓库根目录运行，例如：

```bash
# 先预览：不会安装、卸载或写入清单
mise run install --dry-run gitui
mise run uninstall --dry-run gitui

# macOS 默认为 Homebrew formula；Linux 自动选择本机的包管理器
mise run install gitui
mise run uninstall gitui

# Homebrew cask 必须明确指定类型
mise run install --cask zed
mise run uninstall --cask zed

# Arch AUR；先准备 paru 或 yay
mise run install --aur visual-studio-code-bin
mise run uninstall visual-studio-code-bin

# 第三方 Homebrew 包：先审核来源，再明确授权这一个完整包名
mise run install --cask --trust abue-ammar/tinycast/tinycast
```

每次处理**一个准确包名**，先展示原生命令与清单 diff；输入屏幕上显示的 `INSTALL 包名` 或 `UNINSTALL 包名` 后才执行，其他输入取消。保留原生包管理器自身的依赖检查和确认，不自动传递 `--yes`。请用普通用户运行，不要 `sudo mise`；需要提权的 Linux 原生命令会自行调用 `sudo`。

| 检测到的平台 | 安装 / 卸载入口 | 成功后更新 |
|---|---|---|
| macOS | `brew install/uninstall --formula` 或 `--cask` | `packages/macos/Brewfile`；第三方包还维护精确信任清单 |
| Arch 系 | `pacman -S --needed --asexplicit` / `pacman -R`；AUR 安装使用 paru/yay | `packages/linux/arch/mise.toml` 中的 `pacman:` / `aur:` |
| Debian 系 | `apt-get install` / `apt-get remove` | `packages/linux/debian/mise.toml` 中的 `apt:` |
| Fedora 系 | `dnf install` / `dnf remove` | `packages/linux/fedora/mise.toml` 中的 `dnf:` |
| openSUSE 系 | `zypper install` / `zypper remove` | `packages/linux/opensuse/mise.toml` |

工作规则：

- **成功后才改清单**：同时检查原生命令退出状态与实际安装状态。失败、取消、返回成功但目标仍未达到，都不会提交计划中的配置修改。包已卸载但仍有声明时，确认后可以仅移除该声明。
- 只记录你点名的根包，不把安装时顺带拉取的依赖写进清单。不会顺便运行 `system-sync`、`cleanup`、`autoremove` 或卸载 tap。
- Homebrew 关闭隐式升级、安装后 cleanup 和卸载后 autoremove；APT 安装使用 `--no-upgrade --no-remove`，卸载关闭 autoremove/purge。Arch 不执行 `-Sy`、`-Rns`；DNF/zypper 卸载关闭依赖自动清理。升级仍走各平台正常流程，孤立依赖以后用 `mise run dry-run` 单独审阅。
- 卸载目标必须通过现有 `protected-packages.txt` 和内置关键包检查。APT 预演如果将连带删除其他包就停止；RPM 系先用 `rpm -e --test` 检查反向依赖，检查失败也停止。不要为绕过保护而添加 force/ignore-dependencies 参数。原生事务若建议额外删除或替换包，请取消并人工处理；脚本会对比操作前后状态，发现额外移除时报错并保留审计。
- 第三方 Homebrew 包使用准确的 `owner/tap/name`；首次添加必须 `--trust`，不是全 tap 信任。成功后自动补充 `tap`、完整的 `brew/cask ..., trusted: true` 以及对应的 `trusted-formulae.txt` / `trusted-casks.txt`。卸载时删除对应声明和信任清单项，保留 tap。已有精确信任的完整声明再次安装无需重复 `--trust`。
- 修改会保留其他条目、注释和支持的单行选项。例如已有 `brew "ruby", link: false` 会保留该选项，并向安装命令传递 `--skip-link`。

**支持边界：**需要 Python 3.9+。Homebrew 自动编辑器支持本仓库当前的静态单行 `tap` / `brew` / `cask` 声明及 `trusted`、`link` 布尔选项，不执行或猜测动态 Ruby。Linux TOML 由 `mise config get --file` 的官方解析器校验（不依赖特定 status JSON 或 Python `tomllib`），然后仅编辑 `[bootstrap.packages]` 下的单行条目；目标安装声明限 `"latest"` 或 `{ version = "latest" }`。多行包声明、其他选项、版本固定、虚拟包、包组、URL/本地文件、通配符或任意原生参数不由此入口处理，遇到不支持格式会停止。必要的元数据查询可能读取网络或刷新包管理器自身缓存，但 `--dry-run` 不运行原生安装/卸载命令，也不修改仓库清单或真实信任库。

直接执行 `brew install`、`apt install`、`pacman -S` 等原生命令**不会被监听**，它们的卸载命令也不会自动删清单。本方案没有修改 shell 启动文件、注册 hook 或后台轮询。以后从统一 mise 入口操作，或继续手工编辑清单后运行 `system-sync`；不要在两种习惯间切换时忘记同步声明。Linux 初次迁移的 baseline 导入仍不可省略，单包入口不会自动接管其余已安装软件。

### 中断与写入失败的恢复

确认后，操作前后的清单副本和事务阶段保存在 `.system-sync/history/<时间>-package-<随机值>/`：`*.before` 是原文件，`*.after` 是候选结果，`transaction.json` 记录命令与状态。该目录被 Git 忽略。

清单采用临时文件原子替换，也会检测操作期间的人工编辑并拒绝覆盖。`install` / `uninstall`、整机 `system-sync`、macOS 升级和直接调用的平台写操作共用 `.system-sync/operation.lock`，冲突时立即停止。多个文件无法与系统包事务组成一个跨文件/跨进程的原子事务，突然断电或写盘失败仍可能需要人工恢复。手工包管理命令、直接运行的 mise bootstrap 命令和编辑器不会参与此锁，仍不要与这些入口并行操作。

如果原生安装/卸载已经发生，但配置写入失败，脚本不会擅自反向安装或删除软件。先检查包管理器实际状态、审计副本和 `git diff`，解决权限/格式/并发修改问题后再用相同入口重试；必要时人工合并 `*.after`。在声明和实际状态尚未对齐前，不要直接运行整机 `system-sync`。Git 只能恢复配置文件，不能回滚已经执行的系统包操作。

测试（使用替身包管理器，不修改本机软件）：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests -v
```

## 从当前系统反向校正清单漂移

`system-sync` 的方向是“声明 → 系统”。如果希望保留当前机器的软件状态，应先按下面的命令导出“系统 → 声明”的候选，再核对差异；这些导出命令不安装或卸载软件，也不直接覆盖受管清单。请在仓库根目录执行，且不要与包操作并行运行。

macOS 使用 Homebrew 自己的快照命令，仅导出本项目管理的 formula、cask 和 tap：

```bash
(
  set -euo pipefail
  mkdir -p .system-sync/capture
  HOMEBREW_NO_AUTO_UPDATE=1 brew bundle dump \
    --file=.system-sync/capture/Brewfile --force \
    --formula --cask --tap --no-describe --no-restart
  diff -u packages/macos/Brewfile .system-sync/capture/Brewfile
)
```

`diff` 返回 1 表示存在差异。核对后将新增、删除项合并到 `packages/macos/Brewfile`，保留仍然需要的 `link: false` 等选项。第三方包必须使用完整名称，并同步核对 `trusted-formulae.txt` / `trusted-casks.txt`；不要把快照里未知的全 tap 信任直接带入配置。已声明但未安装的软件可能是未来安装意图，是否删除应逐项决定。Homebrew 的 requested 标记也可能包含手工重装的依赖，导出后仍应核对根包意图。

例如，当前已安装且确实要保留的 `android-cli`、`homebrew-app` 和 `android/tap`，应加入对应声明，避免下次 cleanup 将它们列为移除对象。这里的 `--force` 只覆盖 `.system-sync/capture/Brewfile` 快照，不能加到 cleanup 命令上。

Linux 按本机发行版导出手工根包，生成包含 manager 限定的候选配置：

Arch / Manjaro（foreign 包须逐项确认来自 AUR，本地构建包需人工处理）：

```bash
(
  set -euo pipefail
  mkdir -p .system-sync/capture
  pacman -Qqen | LC_ALL=C sort -u > .system-sync/capture/arch-native.txt
  pacman -Qqem | LC_ALL=C sort -u > .system-sync/capture/arch-foreign.txt
  {
    printf '[settings]\nsystem_packages.managers = ["pacman", "aur"]\n\n[bootstrap.packages]\n'
    awk '{ printf "\"pacman:%s\" = \"latest\"\n", $0 }' .system-sync/capture/arch-native.txt
  } > .system-sync/capture/arch.toml
)
```

Debian / Ubuntu：

```bash
(
  set -euo pipefail
  mkdir -p .system-sync/capture
  apt-mark showmanual | LC_ALL=C sort -u > .system-sync/capture/debian-roots.txt
  {
    printf '[settings]\nsystem_packages.managers = ["apt"]\n\n[bootstrap.packages]\n'
    awk '{ printf "\"apt:%s\" = \"latest\"\n", $0 }' .system-sync/capture/debian-roots.txt
  } > .system-sync/capture/debian.toml
)
```

Fedora / RHEL（DNF4 需要发行版提供的 repoquery 插件）：

```bash
(
  set -euo pipefail
  mkdir -p .system-sync/capture
  dnf repoquery --installed --userinstalled --queryformat '%{name}' \
    | LC_ALL=C sort -u > .system-sync/capture/fedora-roots.txt
  {
    printf '[settings]\nsystem_packages.managers = ["dnf"]\n\n[bootstrap.packages]\n'
    awk '{ printf "\"dnf:%s\" = \"latest\"\n", $0 }' .system-sync/capture/fedora-roots.txt
  } > .system-sync/capture/fedora.toml
)
```

openSUSE / SLE：

```bash
(
  set -euo pipefail
  mkdir -p .system-sync/capture
  zypper --xmlout --no-refresh packages --userinstalled \
    | ./scripts/parse-zypper-xml.py solvables - \
    | LC_ALL=C sort -u > .system-sync/capture/opensuse-roots.txt
  {
    printf '[settings]\nsystem_packages.managers = ["zypper"]\n\n[bootstrap.packages]\n'
    awk '{ printf "\"zypper:%s\" = \"latest\"\n", $0 }' .system-sync/capture/opensuse-roots.txt
  } > .system-sync/capture/opensuse.toml
)
```

**只执行本机对应的一段**。每段通过独立子 shell 的 `set -euo pipefail` 在查询失败时停止，不将失败输出作为完整 baseline；失败后不要使用以前遗留的候选。用 `diff -u packages/linux/<profile>/mise.toml .system-sync/capture/<profile>.toml` 核对后，将候选合并回对应 profile；保留必要的根包、版本选项和 `protected-packages.txt`。Arch 审阅 foreign 列表后，再按需补充 `aur:` 声明。

更新清单后运行：

```bash
git diff -- packages
mise run status
mise run dry-run
```

确认没有意外的补装、降级或删除项后，再决定是否运行 `mise run system-sync`。官方 `mise bootstrap packages import` 当前仅导入 Homebrew formula，本项目 macOS 使用 Brewfile，因此不能用它同时导入本项目的 cask、tap 和 Linux profile。

## 首次迁移 Linux baseline

所有 Linux profile 都需要先准备 Bash、Python 3.9+、mise 2026.10.11+ 和对应的包管理器。Python 是共享锁、版本预检和审计的前置依赖，不能等本仓库的安装阶段再补装。为了支撑公共 shell 与 Claude Hook，各 Linux profile 还声明了 `zsh` 和 `jq`，并将 `zsh` 加入保护清单。

导入后要逐项审阅 `protected-packages.txt`，根据本机的内核、引导、磁盘/加密、网络、远程救援和显卡环境补充保护项。

### Arch / Manjaro

导出官方仓库和 foreign 的显式安装包：

```bash
pacman -Qqen | LC_ALL=C sort -u > native-explicit.txt
pacman -Qqem | LC_ALL=C sort -u > foreign-explicit.txt
```

生成 `packages/linux/arch/mise.toml` 的片段：

```bash
awk '{ printf "\"pacman:%s\" = \"latest\"\n", $0 }' native-explicit.txt
awk '{ printf "\"aur:%s\" = \"latest\"\n", $0 }' foreign-explicit.txt
```

foreign 包可能来自 AUR，也可能是本地构建或 `pacman -U`，不能不经审核全部当成 AUR。最终配置必须保留 `pacman:base`。

### Debian / Ubuntu

导出 APT 的 manual 根包：

```bash
apt-mark showmanual | LC_ALL=C sort -u > apt-manual.txt
awk '{ printf "\"apt:%s\" = \"latest\"\n", $0 }' apt-manual.txt
```

用生成结果替换 `packages/linux/debian/mise.toml` 的示例项，并确保保留 `apt:base-files`。

### Fedora / RHEL 系

导出 DNF 认定的 user-installed 根包：

```bash
dnf repoquery --installed --userinstalled --queryformat '%{name}' \
  | LC_ALL=C sort -u > dnf-user-installed.txt
awk '{ printf "\"dnf:%s\" = \"latest\"\n", $0 }' dnf-user-installed.txt
```

DNF4 上若没有 `repoquery`，需先安装发行版提供的 `dnf-plugins-core`。用生成结果替换 `packages/linux/fedora/mise.toml` 的示例项，并确保保留 `dnf:filesystem`。

### openSUSE / SLE

使用 mise 原生 `zypper:` 声明；用仓库自带的 XML 解析器导出 user-installed 包：

```bash
zypper --xmlout --no-refresh packages --userinstalled \
  | ./scripts/parse-zypper-xml.py solvables - \
  | LC_ALL=C sort -u > zypper-user-installed.txt
awk '{ printf "\"zypper:%s\" = \"latest\"\n", $0 }' zypper-user-installed.txt
```

将结果审阅后写入 `packages/linux/opensuse/mise.toml` 的 `[bootstrap.packages]`，保留 `zypper:filesystem` 和 manager 限定。这个 profile 只管理 package 根，不管理 pattern、product 或 repository 配置。

### baseline 验收

不要立即 apply。先重复运行：

```bash
git diff
mise run status
mise run dry-run
```

直到“未声明根包”、“降级”和“移除事务”中只有你确实想删的内容，再执行 `mise run system-sync`。

## Linux 收敛语义与安全保护

### Arch

1. 先校验声明、保护项与变更阈值，经精确确认并保存安装前状态后，再由 mise 安装缺失的 pacman/AUR 声明。
2. 脚本只比较 `pacman -Qqe` 的 explicit 包，不把整棵依赖树当成根包。
3. 先用 `pacman -D --asexplicit` 保护声明/保护根，再用 `--asdeps` 降级未声明根。
4. 重新查询 `pacman -Qdtq`，用 `pacman -Rsp` 预演完整递归事务。
5. 事务不触及声明/保护包时，才会在再次确认后执行 `pacman -Rs`。

默认不使用 `-Rns`，因此不会额外按 `-n` 语义删除配置备份。

### Debian / Fedora

APT 用 `apt-mark manual/auto`，DNF5 用 `dnf mark user/dependency`，DNF4 用对应的 `dnf mark install/remove`。两者都会：

1. 只把 manual/user-installed 包与 mise 声明比较；
2. 前置校验和精确确认后，保存安装前状态，由 mise 安装缺失声明并重新计算计划，再提升声明和保护根、降级未声明根；
3. 在安装原因变更后重新计算 autoremove；
4. 若事务触及声明或保护包，立即停止；
5. 最后交给 APT/DNF 显示自身事务和确认。

APT 使用 `apt-get autoremove` 且不传 `--purge`。

### openSUSE

声明来自 `packages/linux/opensuse/mise.toml` 的 `[bootstrap.packages]`。缺失项由 `mise bootstrap packages apply --manager zypper` 安装，安装前后都通过 mise 状态和 RPM 数据库核对完整声明。原生 manager 的版本、退出码和平台边界见 [mise zypper 文档](https://mise.jdx.dev/bootstrap/packages/zypper.html)；不适用于依赖 `transactional-update` 的只读根文件系统（例如 MicroOS / Aeon）。

zypper 能查询 user-installed 和 unneeded，但本方案没有一个与 `apt-mark auto` / `dnf mark dependency` 等价且经验证的通用降级接口。因此它会把未声明 user-installed 包作为直接移除目标，然后：

1. 加入 zypper 当前认定的 unneeded 包；
2. 用 `zypper --xmlout ... remove --dry-run --clean-deps` 解析完整事务；
3. 校验事务不触及声明/保护包；
4. 再次精确确认后，由交互式 `zypper --no-refresh remove --clean-deps` 在与预演相同的缓存仓库元数据上执行。

脚本故意不自动删除 `--orphaned` 包，因为它们可能是本地 RPM 或已移除仓库的必需软件。openSUSE 是四个 Linux profile 中最需人工审核 dry-run 的一个。

### 通用失败关闭条件

以下任一情况会停止，不进入卸载：

- 声明为空，或缺少 `base` / `base-files` / `filesystem` 必备根；
- mise JSON 或 zypper XML 结构无法可靠解析；
- mise manager 不可用、条目被跳过、状态未知或声明为 `absent`；根清单只接受需要保留的包，删除通过单包入口或审阅后的根包收敛完成；
- 包名含不安全字符，或 virtual provider 无法映射到具体已安装包；
- 移除事务触及声明包、硬保护包、profile allowlist 或动态保护的内核/引导包；
- 一次降级/移除超过数量或比例阈值，且没有显式传入 `--allow-large-change`；
- `apply` 不在交互式终端中运行，或确认短语不匹配。

确认 baseline 完整且大范围变更确属预期时，可直接运行：

```bash
./scripts/system-sync.sh --apply --allow-large-change
```

## macOS Homebrew

`packages/macos/Brewfile` 是当前 Mac 经过审核的 formula 根意图、cask 和第三方 tap baseline。它只声明真正想保留的根 formula，不把 Homebrew 自动维护的整棵依赖树写入配置。Brewfile 本身是当前清单的唯一真实来源，README 不复制容易过期的数量和名称。

macOS 不把现有 formula/cask 强行转移给 mise 所有权。Homebrew 继续管理它们，mise 只提供统一 task 入口。

### 日常修改

在 Brewfile 中新增：

```ruby
brew "jq"
cask "visual-studio-code"
```

删除软件时，删除对应 `brew` / `cask` 行，然后：

```bash
git diff -- packages/macos/Brewfile
mise run dry-run
mise run system-sync
```

`dry-run` 会显示 Brewfile 声明、缺失项、未声明的 requested formula/cask/tap、`brew bundle check` 和 Homebrew 自己计算的 cleanup 精确预演。预演会断开标准输入，不会回答 Homebrew 的确认。

“未声明 requested formula”不等于“一定会删除”。例如某个库因手工重装而被 Homebrew 记成 requested，但它仍是 Brewfile 根包的依赖，cleanup 就会保留它。是否真正移除以“Homebrew cleanup 精确预演”为准。

`apply` 的顺序是：

1. `brew bundle install --no-upgrade`；
2. `brew bundle check --no-upgrade`；
3. 重新计算 cleanup 预演并校验保护项；
4. 交互式 `brew bundle cleanup --formula --cask --tap`。

脚本不向 cleanup 传 `--force`，不自动回答 Homebrew 确认，也不使用 cask `--zap`。它不管理 MAS、Cargo/npm/uv 工具、VS Code 扩展等其他 Brew Bundle 类型。

如果手工运行 Brew Bundle，必须指定这个仓库的 Brewfile：

```bash
brew bundle cleanup \
  --file="/Users/sbwoan/Documents/系统配置管理/system_sync/packages/macos/Brewfile"
```

不指定 `--file` 时，Homebrew 只会在当前目录寻找默认 `Brewfile`。不建议手工加 `--force` 跳过确认；日常应使用本仓库的 `mise run` 入口，因为它额外执行保护校验和审计。

### 保护项和第三方 formula / cask 信任

macOS 保护清单位于 `packages/macos/protected-packages.txt`：

```text
formula:git
formula:mise
cask:some-critical-app
tap:owner/repository
```

保护项必须同时存在于 Brewfile；只从 Brewfile 删除时，apply 会失败关闭。确实要删除保护项时，两个文件要同时修改。

`packages/macos/trusted-formulae.txt` 只列出已审核的完整第三方 formula 名。它必须与 Brewfile 中的 `trusted: true` 精确对应；不会将整个 tap 泛化为受信。

第三方 cask 同样需要在 `packages/macos/trusted-casks.txt` 中列出完整名称，并在 Brewfile 中使用对应的精确信任声明。例如 TinyCast：

```ruby
tap "abue-ammar/tinycast"
cask "abue-ammar/tinycast/tinycast", trusted: true
```

`trusted-casks.txt` 中对应一行 `abue-ammar/tinycast/tinycast`。两个清单中的项目必须在 Brewfile 中声明；信任行采用示例所示的独立一行格式。`brew bundle list --cask` 虽然返回短名称，临时信任仍使用完整名称。

cleanup 预演使用独立的临时信任库，每次都初始化 formula 和 cask 信任，覆盖安装前、安装后两次预演。仅在真实 Homebrew 信任库中执行 `brew trust` 不会影响这个临时库。若日志显示安装已成功、随后预演失败，先修正对应信任清单，再重新运行 `mise run dry-run`；已安装且满足声明的软件无需重装。

如果 Homebrew cleanup 报告 formula 依赖图循环，并且同时提出移除受管项目，脚本会要求额外的 `ALLOW HOMEBREW GRAPH WARNING` 精确确认。应先审阅事务，必要时修复 Homebrew 旧 keg 收据后再继续。

单独升级 Brewfile 声明项、不 cleanup：

```bash
mise run macos-upgrade
```

## 跨平台开发环境和应用配置

`dotfiles/` 已重构为公共模块和两个平台适配器：

- `dotfiles/common/`：已启用的 espanso、工具版本、uv、zsh、ChatGPT 和 VS Code 通用配置，以及候选 Claude 全局说明和 Hook；
- `dotfiles/macos/`：espanso 的 macOS 专用片段、Homebrew 相关路径、macOS Android SDK、Toolbox 和 Claude 当前 Mac 模板；
- `dotfiles/linux/`：Linux Android SDK、可选 Linuxbrew，以及不含 macOS 外部 Hook 的 Claude 模板；
- `mise.macos.toml` 与 `mise.linux.toml`：相同 `config-*` 命令背后的两个平台适配器。

仓库和未来的全局 mise 配置都通过 `auto_env` 自动选择 `macos` 或 `linux`。公共模块不需要知道当前平台，平台路径也不会散落在公共调用入口中。

全局 `~/.config/mise/miserc.toml` 使用 `template` 模式部署，以规避 mise 2026.10.15 在 dotfiles dry-run 中将该文件按普通 `mise.toml` 解析、误报未知字段的问题。`auto_env` 和 `env_conf_d` 保留在这个文件中，符合[官方 miserc 配置规则](https://mise.jdx.dev/configuration.html#miserc)。修改来源后运行 `mise run config-sync`；dry-run 不渲染模板，查看真实内容差异用 `mise bootstrap dotfiles diff ~/.config/mise/miserc.toml`。

目前启用 espanso、全局 mise 配置、macOS Rust 参数、zsh、ChatGPT 和 VS Code 的映射。工具 `[tools]` 与 uv 所需的两个环境变量已启用；zsh 负责现有 locale、Android 和 Shell PATH。mise 配置中的其他 `[env]` / PATH、密钥文件和输出脱敏规则仍是候选。`.miserc.toml` 只控制配置发现；Claude 尚未启用。

### Espanso（已启用）

已将 `Snippets 2026-09-11 10.19.05.json` 中的 18 条片段转换为 Espanso YAML。触发词统一使用英文冒号 `:` 前缀和全小写，以原 `name` 作为搜索标签，展开内容保持不变：

- `dotfiles/common/espanso/match/snippets.yml`：12 条公共片段。
- `dotfiles/macos/espanso/match/macos.yml`：4 条 macOS 菜单栏间距/内边距命令，不部署到 Linux。
- `dotfiles/common/espanso/private.local/match/private-snippets.yml`：2 条含调试口令的片段，文件权限为 `0600`，所在 `private.local` 目录被 Git 忽略。通过同样被忽略的根目录 `mise.local.toml` 单独映射。来源和目标都保留 `.yml` 扩展名，避免 Espanso 在解析链接来源时跳过文件。

这些命令片段只展开为文字，导入过程和触发片段本身都不会通过 Espanso shell 扩展执行它们。输入到终端后按回车仍会执行，请先核对内容。

所有受管触发词均不设置 `left_word`、`right_word` 或 `word`，与 `:date` 一致，不依赖前面的空格或换行，也不需要输入后再按空格。用冒号前缀区分普通文字，例如 `:yst` 会展开，而 `system` 不会误触发。旧的无前缀触发词不再保留；冒号使用英文半角 `:`，不是中文全角 `：`。

触发词变更对照：

| 原触发词 | 新触发词 |
| --- | --- |
| `getrnip` | `:getrnip` |
| `topAct` | `:act` |
| `clsdsc` | `:clsdsc` |
| `coli` | `:coli` |
| `topactcn` | `:topactcn` |
| `adbRNFix` | `:adbrnfix` |
| `yst` | `:yst` |
| `setDebugApp` | `:setdebugapp` |
| `clearDebugApp` | `:cleardebugapp` |
| `Tbs` | `:tbs` |
| `Tbp` | `:tbp` |
| `RstTbs` | `:rsttbs` |
| `RstTbp` | `:rsttbp` |
| `pdkit`（本机私有） | `:pdkit` |
| `ddkit`（本机私有） | `:ddkit` |

`:date`、`:cursor`、`:rnlang` 及默认示例 `:espanso`、`:shell` 已符合格式，保持不变。新增触发词时应检查重复和前缀冲突：例如已有 `:act` 时不要再添加 `:actcn`，否则输入到 `:act` 就会提前展开；国区片段因此保留为 `:topactcn`。

原占位符已转换为 Espanso 原生语法：`{date}` → 本地日期 `YYYY-MM-DD`，`{datetime}` → 本地日期时间 `YYYY-MM-DD HH:mm:ss`，`{cursor}` → 光标标记 `$|$`。因此 `:date` 输出日期时间，`:clsdsc` 中的日期动态生成；单独的 `:cursor` 会移除触发词并将光标留在原位。默认 `base.yml` 中重复的 `:date` 示例已移除，`:espanso` 和 `:shell` 仍保留。详见 [Espanso 日期扩展](https://espanso.org/docs/matches/extensions/#date-extension)和[光标标记](https://espanso.org/docs/matches/basics/#cursor-hints)。

原有 `config/default.yml` 的配置选项保持不变；与处理过日期冲突的 `match/base.yml` 一起交给 mise。公共目录使用 `symlink-each`，只接管来源中列出的文件，保留本机的其他片段和 `match/packages/`。公共目录映射明确排除 `*.local` 与 `*.example.toml`，私有文件由本机映射单独链接。

目标位置：macOS 为 `~/Library/Application Support/espanso`，Linux 为 `~/.config/espanso`。如果使用自定义 `XDG_CONFIG_HOME` 或 `ESPANSO_CONFIG_DIR`，先运行 `espanso path`，再将平台适配器和私有映射的目标改成实际路径。跨系统配置目录的依据见 [Espanso 官方入门文档](https://espanso.org/docs/get-started/#configuration)。

后续编辑仓库中的 YAML，已建立的链接会直接指向修改后的内容：

```bash
mise run config-status
mise run config-dry-run
mise run config-sync
espanso match list --only-triggers

# 如果修改链接来源后没有自动重载，可手动重启
espanso service restart
```

首次接管已有普通文件时，mise 会报告冲突。先备份 Espanso 配置并检查 `mise bootstrap dotfiles apply --dry-run --force`；确实需要替换时，只对 `espanso path` 对应的目标执行 `apply --force`，不要顺带强制覆盖其他应用。此次迁移的原文件备份位于 `.system-sync/history/*-espanso-*/`。mise 的链接/冲突行为见[官方 dotfiles 文档](https://mise.jdx.dev/dotfiles.html)。

新的机器只从 Git 取得 16 条非私密片段（Linux 使用其中 12 条），没有私有来源也不会影响公共配置部署。需要私有片段时，通过安全渠道复制 `private.local/match/private-snippets.yml` 并设置 `chmod 600`，再将 `dotfiles/common/espanso/mise.local.example.toml` **合并**到仓库根 `mise.local.toml`。不要把原始 JSON 或含口令的 YAML 加入 Git；`0600` 和 Git 忽略都不是加密，也不能代替私有备份。

当前 Mac 已安装的 `espanso` cask 已加入 Brewfile，仅记录声明，本次未安装或升级软件。其他 Linux 机器仍需按发行版准备 Espanso；这里的配置部署不安装应用，也不授予 macOS 辅助功能权限。若原片段管理器仍在运行且启用了同名关键词，请手动停用重叠片段，避免两边同时展开。

### 开发工具版本（已启用）

2026-10-19 重新核对本机实际版本后，由 `dotfiles/common/mise/config.toml` 固定全局默认值：

| 工具 | 默认版本 | 迁移方式和保留项 |
|---|---|---|
| Node | 24.15.0 | `mise sync node --nvm` 复用现有安装；22.15.0 和全局 npm 工具保留，停用 nvm 初始化 |
| Python | 3.12.12 | 复用原 uv 安装的解释器；按用户选择切换默认版本，Apple 3.9.6 保留 |
| Ruby | 4.0.7 | 使用 mise 安装；Homebrew Ruby 仅作为 CocoaPods 依赖保留 |
| Java | jetbrains-21.0.6-b895.109 | 用 `mise link` 注册现有 JBR，保留其他 IDE JDK，不重复下载 |
| Rust | 1.99.0 | mise 内置 rustup 后端；保留 Cargo CLI、rust-src 和原有交叉编译目标 |
| Bun | 1.4.3 | mise 安装，移除 Homebrew 的直接管理 |
| uv | 0.13.1 | mise 安装；uv 继续负责 Python 项目依赖和虚拟环境 |
| Yarn Classic | 1.22.22 | 使用 `npm:yarn` 后端，移除 Homebrew 的直接管理；不另启用 Corepack Yarn |

这些默认值已针对当前 Mac 核对；真实 Linux 主机仍须验证。macOS 的 Rust 额外组件和目标在 `dotfiles/macos/mise/config.toml` 中声明，避免将 Apple 目标强加给 Linux。

现有 Node 和 Python 安装通过 mise 官方同步命令复用，Java 使用官方 `mise link`。这些注册依赖原目录，不能删除 `~/.nvm/versions`、原 uv Python 目录或已注册的 JBR。以后通过 mise 选择和升级版本；首次在新机器上安装时，mise 会按版本声明下载。参见 [Node 迁移](https://mise.jdx.dev/lang/node.html#migrating-from-nvm-nodenv-or-homebrew)、[Python 迁移](https://mise.jdx.dev/lang/python.html#migrating-from-pyenv-or-uv)和[已有 JDK 接入](https://mise.jdx.dev/lang/java.html#use-a-jdk-installed-elsewhere)。

Python 的 `UV_PYTHON` 指向 mise 选择的解释器，`UV_PYTHON_DOWNLOADS=never` 禁止 uv 再下载另一套 Python。项目需要其他 Python 时，在项目 mise 配置或已启用识别的 `.python-version` 中声明，再运行 `mise install`；项目版本覆盖全局默认。Apple Python 的原有用户库继续留在其 3.9 环境中，不自动混入 3.12；旧脚本需要它时显式使用 `/usr/bin/python3`，新项目通过 `uv sync` 安装自己的依赖。

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

版本升级通过修改受管配置、`mise install` 和逐项验证完成；`mise use -g` 会修改受管的全局配置来源，提交前须检查 Git diff。工具完全迁出 Homebrew 的声明后，不再用 `mise run install` 为同一工具新增系统包声明。

Flutter 3.47.2 的本地工作树有改动，因此本次保留原安装，不交给 mise。

### zsh（已启用）

2026-10-20 对比本机实际配置后接管以下五项，全部使用 `symlink`：

| 目标 | 来源 |
|---|---|
| `~/.zshenv` | `dotfiles/common/zsh/zshenv` |
| `~/.zshrc` | `dotfiles/common/zsh/zshrc` |
| `~/.p10k.zsh` | `dotfiles/common/zsh/p10k.zsh` |
| `~/.zprofile` | 当前平台的 `dotfiles/macos/zsh/zprofile` 或 `dotfiles/linux/zsh/zprofile` |
| `~/.config/zsh/platform.zsh` | 当前平台的 `platform.zsh` |

所有 zsh 实例先读取 `.zshenv`；登录 Shell 还读取 `.zprofile`，交互 Shell 读取 `.zshrc`。参见 [zsh 启动文件顺序](https://zsh.sourceforge.io/Doc/Release/Files.html#Startup_002fShutdown-Files)。

- `.zshenv` 保持轻量，只在目录实际存在时添加用户命令、Gem、Cargo 和 Flutter 路径，不加载插件或运行 mise。
- `.zprofile` 准备当前平台的 Homebrew、Android SDK 和 JetBrains Toolbox 路径，并执行 `mise activate zsh --shims`，让登录但非交互的 Shell 也能找到受管工具。
- `.zshrc` 加载平台模块、原有 Zinit 插件、Powerlevel10k、私有文件和可选 OpenClaw 补全，最后执行一次 `mise activate zsh`。git-ai 和 `.local/bin` 保持原有命令优先级，PATH 用 zsh 唯一化数组去重。

macOS 平台模块保留现有 locale，Android SDK 使用 `~/Library/Android/sdk`；Linux 使用 `~/Android/Sdk` 和可选 Linuxbrew，不照搬 macOS locale。不存在的 Android `tools` 目录不会加入 PATH。当前 Mac 的 Powerlevel10k 配置按原文件导入，保留原有外观；Zinit 已下载的插件、历史文件、补全缓存和 instant-prompt 缓存仍由本机维护，不纳入 Git。

原 `.zshrc` 中的 `CASE_SUBMITTER`、`DASHBOARD_HOST` 已移到仓库外的 `~/.zshrc.local`，权限为 `0600`。该文件不由 mise 映射；新的机器没有它时会正常跳过。需要同步私有值时，通过安全渠道复制到新机器并设置权限；不要将实际值粘贴到公共 zsh 文件。

首次接管已有普通文件前，先备份并预览这五个目标；需要替换时只对这五项执行 `mise bootstrap dotfiles apply --force`。当前 Mac 的原文件、私有设置和启动前后校验记录位于 `.system-sync/history/*-zsh-*/`。恢复时先对这五项执行定向 `unapply`，再从审计目录的 `home.before` 恢复原文件。

以后直接编辑仓库来源文件，链接会反映修改；用 `mise run config-status` 检查映射，重新打开终端加载新配置。当前 Mac 使用真实登录终端验证，公共启动逻辑另有隔离测试；Linux 的平台文件通过语法检查，尚未在真实 Linux 主机验证。

### ChatGPT（已启用）

2026-10-20 核对的本机应用为 `/Applications/ChatGPT.app`，版本 `26.1007.71326`，bundle id 为 `com.openai.codex`。它使用 `~/.codex` 配置目录，与 Codex CLI / IDE 扩展共享用户配置，见 [OpenAI 官方配置说明](https://learn.chatgpt.com/docs/config-file/config-basic)。

| 目标 | 来源 | 管理方式 |
|---|---|---|
| `~/.codex/config.toml` | `dotfiles/common/chatgpt/config.toml` | `merge = true`，只管理来源中的键 |
| `~/.codex/AGENTS.md` | `dotfiles/common/chatgpt/AGENTS.md` | `copy`，保留可由应用编辑的普通文件 |
| `~/.codex/keybindings.json` | `dotfiles/common/chatgpt/keybindings.json` | `copy`，导入当前快捷键 |

共享偏好包括当前模型、推理强度、服务层级、两项功能开关，以及对话详情、队列、语音屏幕上下文和外观设置。全局协作规则与当前禁用头像快捷键的配置按原内容导入。首次接管保持本机有效设置不变。

`config.toml` 中的私有环境变量、通知命令、Hook、MCP、项目路径与信任、插件及技能安装状态、引导状态和头像标识继续留在本机。认证文件、会话、数据库、浏览器数据和 macOS 应用状态也不纳入映射。这里使用 mise 原生[按键合并](https://mise.jdx.dev/dotfiles/edits.html#set-keys-in-a-config-file)，没有增加应用不支持的 `config.local.toml`。

### VS Code（已启用）

本机 VS Code `1.142.0` 使用默认 User 目录，现有 18 项设置中有 16 项通用偏好。已按本机值导入 `dotfiles/common/vscode/settings.json`，包括原候选缺少的 `workbench.tree.indent = 16`。两个平台从同一个公共来源合并：

| 平台 | 目标 |
|---|---|
| macOS | `~/Library/Application Support/Code/User/settings.json` |
| Linux | `~/.config/Code/User/settings.json` |

本机的 `dart.flutterSdkPath`、`git.path` 和以后新增的未声明设置保留在目标文件中。旧的整份覆盖模板已由公共合并来源替代。Profiles、扩展安装、工作区配置、凭据、`argv.json` 和全局存储继续由本机 VS Code 管理；现有 Settings Sync 配置保持原状。VS Code 的目录与配置作用域见[官方说明](https://code.visualstudio.com/docs/configure/settings)。

当前 `settings.json` 是标准 JSON，符合 mise 合并器要求。VS Code 允许的 JSONC 注释不受当前 mise 合并器支持；文件包含注释、尾逗号或无效内容时，mise 会报错并保留原文件，不会自动剥离注释再覆盖。

### 应用配置的日常同步

编辑上述公共来源后，运行 `mise run config-status`、`mise run config-dry-run`，确认后运行 `mise run config-sync`。`/shared` 是 mise 的编辑条目标识，不是实际目录。应用界面修改的共享值可与来源产生差异；私有键的变化不会被视为共享配置漂移。

合并会递归设置来源拥有的键；删除来源中的键只会停止管理它，目标中的原值保留。取消管理也保留合并过的键。含私有项的目标和差异输出只在本机查看，不将整个 `~/.codex`、VS Code User 目录或其完整文件加入 Git。当前 Mac 的原文件、路径及权限记录保存在 `.system-sync/history/*-app-configs/`，权限受限且被 Git 忽略。恢复时取消相应映射，再从该目录的 `home.before` 恢复文件。

隔离测试覆盖本机私有值、应用新增键、重复同步和 JSONC 拒绝写入；当前 Mac 对比接管前后的完整配置，确认设置值未变。Linux 使用同一公共来源，尚未在真实 Linux 主机验证。

### Claude（候选）

Claude 的 `CLAUDE.md` 与自有 Hook 放在 `common`。Hook 运行环境会自动识别：

- Apple Silicon Homebrew：`/opt/homebrew/bin`；
- Linuxbrew：`/home/linuxbrew/.linuxbrew/bin`；
- 系统 `/usr/bin`、`/bin` 和 `/usr/local/bin`；
- macOS `shasum` 或 Linux `sha1sum`。

macOS Claude 模板保留当前 PromLight、SBBars、git-ai 和项目权限；Linux 模板只保留可跨平台的 Claude Hook 与 Flutter/Rust 命令，不伪造 macOS 应用路径。认证令牌和私有服务地址仍只允许放在仓库外的 `~/.config/mise/secrets.env`。

Claude 候选使用 mise `template` 模式，将 `{{ env.HOME }}` 渲染为目标机器的真实 HOME。`config-dry-run` 不执行模板渲染，只会把模板标为可能变更；需要查看渲染后的真实差异时使用 `mise bootstrap dotfiles diff`。

### 环境变量和 PATH

mise 环境配置分为三个文件；目前启用工具与 uv 解释器选择，其他环境候选仍保持注释。现有 Shell 环境由已接管的 zsh 文件提供：

- `dotfiles/common/mise/config.toml`：已启用的工具版本和 uv 设置，以及候选密钥文件入口和输出脱敏规则；
- `dotfiles/macos/mise/config.toml`：已启用的 Rust 平台参数，以及候选 macOS locale、Android 和 Toolbox；
- `dotfiles/linux/mise/config.toml`：Linux Android 和 Toolbox 常见位置。

Linux locale 不会照搬 macOS。应先用发行版工具确认已经生成的 locale，再取消注释 `LANG` / `LC_ALL`。

真实密钥文件和 `*.local` 继续被 Git 忽略。`redactions` 只能降低输出泄漏风险，不是加密。

### 后续逐项接管其他配置

1. 审阅待接管的配置以及当前平台差异；全局 mise 工具配置已经启用。
2. 在根 `mise.toml` 中启用所需公共映射，再在当前平台的 `mise.macos.toml` 或 `mise.linux.toml` 中启用对应映射。
3. 运行 `mise run config-status` 和 `mise run config-dry-run`，确认目标和来源都属于当前平台。
4. 处理已有真实文件与 symlink 的首次接管冲突后，再运行 `mise run config-sync`。
5. zsh 已接管并启用 mise Shell 激活；接管其他应用时继续保留这一个交互激活入口，不重复初始化旧管理器。

从 `[dotfiles]` 删除条目不会自动删除已经应用的目标。取消管理时先执行 `mise bootstrap dotfiles unapply --dry-run`，确认后执行 `mise bootstrap dotfiles unapply`，再删除映射。

## 风险、取消和恢复

- dry-run 只能精确显示当前状态下的 orphan。Linux 包的安装原因降级后，可能新出现 orphan，所以 apply 会重新计算并第二次确认。
- 在第二次确认处取消时，pacman/APT/DNF 的安装原因可能已经变更，但包尚未卸载。查看当次 `.system-sync/history/.../demoted.txt` 和 `promoted.txt` 恢复需要保留的根包。
- Homebrew 在 cleanup 最后确认前取消时，前面的缺失软件安装可能已完成，但不会继续清理。
- 误删后，从 Git 恢复声明并重新安装。Git 保存软件清单和受管配置，不是已安装包文件、应用数据或完整系统备份。

macOS 恢复 Brewfile 示例：

```bash
git restore packages/macos/Brewfile
brew bundle install --no-upgrade --file=packages/macos/Brewfile
```

## Git 管理

建议提交 `.miserc.toml`、`mise*.toml`、`dotfiles/`、`packages/`、`scripts/` 和 `README.md`。每次修改声明先查看 diff 和 dry-run：

```bash
git diff
mise run dry-run
```

将这个仓库用到另一台机器时，不要直接 apply 原机器的完整 baseline。先根据新机器导出根包，合并差异，并将两台机器确实不同的部分拆成分支或额外 profile，不要靠长期忽略 dry-run 差异维持。

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
- [mise zypper manager](https://mise.jdx.dev/bootstrap/packages/zypper.html)
- [mise Environments](https://mise.jdx.dev/environments/)
- [mise Configuration Environments](https://mise.jdx.dev/configuration/environments.html)
- [mise Dotfiles](https://mise.jdx.dev/dotfiles.html)
- [mise config trust](https://mise.jdx.dev/cli/trust.html)
- [mise monorepo tasks](https://mise.jdx.dev/tasks/monorepo.html)
- [Homebrew Bundle](https://docs.brew.sh/Brew-Bundle-and-Brewfile)
- [ArchWiki: pacman tips and tricks](https://wiki.archlinux.org/title/Pacman/Tips_and_tricks)
- [Debian apt-mark](https://manpages.debian.org/testing/apt/apt-mark.8.en.html)
- [Debian apt-get](https://manpages.debian.org/bookworm/apt/apt-get.8.en.html)
- [DNF command reference](https://dnf.readthedocs.io/en/stable/command_ref.html)
- [DNF5 mark](https://dnf5.readthedocs.io/en/stable/commands/mark.8.html)
- [openSUSE zypper manual](https://manpages.opensuse.org/Tumbleweed/zypper/zypper.8.en.html)
