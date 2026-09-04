# macOS + 多发行版 Linux 声明式系统配置同步

这个仓库用统一的 mise task 作为入口，把各平台的系统包收敛逻辑分开，并保存可逐项启用的开发环境与应用配置候选快照。

| 平台 / profile | 声明来源 | 收敛方式 |
|---|---|---|
| macOS | `platforms/macos/Brewfile` | Homebrew Bundle 安装和 cleanup |
| Arch / Manjaro 系 | `platforms/linux/arch/mise.toml` | pacman/AUR 安装，再降级未声明 explicit 包并清理 orphan |
| Debian / Ubuntu 系 | `platforms/linux/debian/mise.toml` | APT 安装，再调整 manual/auto 并 autoremove |
| Fedora / RHEL 系 | `platforms/linux/fedora/mise.toml` | DNF 安装，再调整 user/dependency 原因并 autoremove |
| openSUSE / SLE | `platforms/linux/opensuse/packages.txt` | zypper 查询 user-installed/unneeded，预演后 `remove --clean-deps` |

mise 当前原生支持 `apt`、`dnf`、`pacman` 和 `aur`，但没有内置 `zypper` manager；因此 openSUSE 使用单独的文本清单和 zypper 脚本。

> **重要：** Linux 下的清单只是结构示例，不是你已有系统的 baseline。首次 `apply` 前必须按本文导入本机的手工根包。

## 文件结构

```text
system_sync/
├── .miserc.toml                     # 在仓库内自动选择 macOS / Linux 配置环境
├── .gitignore
├── README.md
├── mise.toml                         # 公共 task 入口
├── mise.macos.toml                   # macOS dotfiles 适配器；映射尚未启用
├── mise.linux.toml                   # Linux dotfiles 适配器；映射尚未启用
├── dotfiles/
│   ├── common/                       # 两个平台共用
│   │   ├── mise/
│   │   │   ├── config.toml           # 已注释的跨平台 tools 候选
│   │   │   ├── miserc.toml
│   │   │   └── secrets.env.example
│   │   ├── zsh/
│   │   │   ├── zshenv
│   │   │   └── zshrc
│   │   └── claude/
│   │       ├── CLAUDE.md
│   │       └── hooks/
│   ├── macos/                        # 当前 Mac 脱敏快照与平台路径
│   │   ├── mise/config.macos.toml
│   │   ├── zsh/
│   │   │   ├── zprofile
│   │   │   └── platform.zsh
│   │   ├── claude/settings.json.tmpl
│   │   └── vscode/settings.json.tmpl
│   └── linux/                        # 安全的 Linux 候选模板
│       ├── mise/config.linux.toml
│       ├── zsh/
│       │   ├── zprofile
│       │   └── platform.zsh
│       ├── claude/settings.json.tmpl
│       └── vscode/settings.json.tmpl
├── platforms/
│   ├── macos/
│   │   ├── Brewfile
│   │   ├── protected-packages.txt
│   │   └── trusted-formulae.txt
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
│           ├── packages.txt
│           └── protected-packages.txt
└── scripts/
    ├── system-sync.sh               # 统一调度入口
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

`mise.toml` 设置了 `monorepo_root = true`，并在 `[monorepo].config_roots` 中显式列出 Arch、Debian 和 Fedora 的包 profile。`.miserc.toml` 只负责启用 mise 的 `auto_env`：在 macOS 自动叠加 `mise.macos.toml`，在 Linux 自动叠加 `mise.linux.toml`。包 profile 与 dotfiles 平台适配器是两条独立配置链，不依赖自动目录扫描。

运行时审计清单保存在 `.system-sync/history/<UTC 时间>/`；该目录和任意层级的 `.DS_Store` 都已被 Git 忽略。

## 统一使用方法

先在仓库根目录信任根配置和 Linux 子配置：

```bash
mise trust --all
```

然后使用同一组命令：

```bash
mise run status
mise run dry-run
mise run system-sync
```

- `status`：只读地显示声明、当前根包和差异。
- `dry-run`：同时预览缺失包安装和收敛计划，不修改系统。
- `system-sync`：经 mise 确认、脚本精确短语确认和包管理器自身确认后才修改系统。

应用配置还提供三个独立入口：

```bash
mise run config-status
mise run config-dry-run
mise run config-sync
```

当前公共配置和两个平台适配器中的全部 `[dotfiles]` 映射都被注释，因此这三个命令不会接管或修改任何用户文件。启用后，命令接口保持不变，mise 会在内部选择当前平台适配器。

Linux 使用 `/etc/os-release` 自动选择 profile。当前映射为：

- Arch、Manjaro、EndeavourOS、CachyOS → `arch`；
- Debian、Ubuntu、Linux Mint、Pop!_OS、Kali、Raspbian → `debian`；
- Fedora、RHEL、CentOS、Rocky、AlmaLinux、Oracle Linux → `fedora`；
- openSUSE Leap/Tumbleweed、SLES、SLED → `opensuse`。

其他发行版不会猜测执行，而是失败关闭。Linux 统一升级暂未实现；请继续使用发行版自身的升级流程。

## 首次迁移 Linux baseline

所有 Linux profile 都需要 Bash、Python 3、mise 和对应的包管理器。APT/DNF/Arch 可先由 mise 安装 profile 中缺失的 Python；openSUSE 脚本自身需要 Python 解析 zypper XML，因此首次运行前必须已安装 `python3`。为了支撑公共 shell 与 Claude Hook，各 Linux profile 还声明了 `zsh` 和 `jq`，并将 `zsh` 加入保护清单。

导入后要逐项审阅 `protected-packages.txt`，根据本机的内核、引导、磁盘/加密、网络、远程救援和显卡环境补充保护项。

### Arch / Manjaro

导出官方仓库和 foreign 的显式安装包：

```bash
pacman -Qqen | LC_ALL=C sort -u > native-explicit.txt
pacman -Qqem | LC_ALL=C sort -u > foreign-explicit.txt
```

生成 `platforms/linux/arch/mise.toml` 的片段：

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

用生成结果替换 `platforms/linux/debian/mise.toml` 的示例项，并确保保留 `apt:base-files`。

### Fedora / RHEL 系

导出 DNF 认定的 user-installed 根包：

```bash
dnf repoquery --installed --userinstalled --queryformat '%{name}' \
  | LC_ALL=C sort -u > dnf-user-installed.txt
awk '{ printf "\"dnf:%s\" = \"latest\"\n", $0 }' dnf-user-installed.txt
```

DNF4 上若没有 `repoquery`，需先安装发行版提供的 `dnf-plugins-core`。用生成结果替换 `platforms/linux/fedora/mise.toml` 的示例项，并确保保留 `dnf:filesystem`。

### openSUSE / SLE

mise 没有内置 zypper manager。用仓库自带的 XML 解析器导出 user-installed 包：

```bash
zypper --xmlout --no-refresh packages --userinstalled \
  | ./scripts/parse-zypper-xml.py solvables - \
  | LC_ALL=C sort -u > zypper-user-installed.txt
```

将结果审阅后写入 `platforms/linux/opensuse/packages.txt`，每行一个包，并保留 `filesystem`。这个 profile 只管理 package 根，不管理 pattern、product 或 repository 配置。

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

1. mise 安装缺失的 pacman/AUR 声明。
2. 脚本只比较 `pacman -Qqe` 的 explicit 包，不把整棵依赖树当成根包。
3. 先用 `pacman -D --asexplicit` 保护声明/保护根，再用 `--asdeps` 降级未声明根。
4. 重新查询 `pacman -Qdtq`，用 `pacman -Rsp` 预演完整递归事务。
5. 事务不触及声明/保护包时，才会在再次确认后执行 `pacman -Rs`。

默认不使用 `-Rns`，因此不会额外按 `-n` 语义删除配置备份。

### Debian / Fedora

APT 用 `apt-mark manual/auto`，DNF5 用 `dnf mark user/dependency`，DNF4 用对应的 `dnf mark install/remove`。两者都会：

1. 只把 manual/user-installed 包与 mise 声明比较；
2. 先提升声明和保护根，再降级未声明根；
3. 在安装原因变更后重新计算 autoremove；
4. 若事务触及声明或保护包，立即停止；
5. 最后交给 APT/DNF 显示自身事务和确认。

APT 使用 `apt-get autoremove` 且不传 `--purge`。

### openSUSE

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
- 包名含不安全字符，或 virtual provider 无法映射到具体已安装包；
- 移除事务触及声明包、硬保护包、profile allowlist 或动态保护的内核/引导包；
- 一次降级/移除超过数量或比例阈值，且没有显式传入 `--allow-large-change`；
- `apply` 不在交互式终端中运行，或确认短语不匹配。

确认 baseline 完整且大范围变更确属预期时，可直接运行：

```bash
./scripts/system-sync.sh --apply --allow-large-change
```

## macOS Homebrew

`platforms/macos/Brewfile` 是当前 Mac 经过审核的 formula 根意图、cask 和第三方 tap baseline。它只声明真正想保留的根 formula，不把 Homebrew 自动维护的整棵依赖树写入配置。Brewfile 本身是当前清单的唯一真实来源，README 不复制容易过期的数量和名称。

macOS 不把现有 formula/cask 强行转移给 mise 所有权。Homebrew 继续管理它们，mise 只提供统一 task 入口。

### 日常修改

在 Brewfile 中新增：

```ruby
brew "jq"
cask "visual-studio-code"
```

删除软件时，删除对应 `brew` / `cask` 行，然后：

```bash
git diff -- platforms/macos/Brewfile
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
  --file="/Users/sbwoan/Documents/系统配置管理/system_sync/platforms/macos/Brewfile"
```

不指定 `--file` 时，Homebrew 只会在当前目录寻找默认 `Brewfile`。不建议手工加 `--force` 跳过确认；日常应使用本仓库的 `mise run` 入口，因为它额外执行保护校验和审计。

### 保护项和第三方 formula 信任

macOS 保护清单位于 `platforms/macos/protected-packages.txt`：

```text
formula:git
formula:mise
cask:some-critical-app
tap:owner/repository
```

保护项必须同时存在于 Brewfile；只从 Brewfile 删除时，apply 会失败关闭。确实要删除保护项时，两个文件要同时修改。

`platforms/macos/trusted-formulae.txt` 只列出已审核的完整第三方 formula 名。它必须与 Brewfile 中的 `trusted: true` 精确对应；不会将整个 tap 泛化为受信。

如果 Homebrew cleanup 报告 formula 依赖图循环，并且同时提出移除受管项目，脚本会要求额外的 `ALLOW HOMEBREW GRAPH WARNING` 精确确认。应先审阅事务，必要时修复 Homebrew 旧 keg 收据后再继续。

单独升级 Brewfile 声明项、不 cleanup：

```bash
mise run macos-upgrade
```

## 跨平台开发环境和应用配置

`dotfiles/` 已重构为公共模块和两个平台适配器：

- `dotfiles/common/`：跨平台工具候选、zsh 主配置、Claude 全局说明和 Hook；
- `dotfiles/macos/`：Homebrew、macOS Android SDK、Toolbox、Claude/VS Code 当前 Mac 模板；
- `dotfiles/linux/`：Linux Android SDK、可选 Linuxbrew，以及不含 macOS 外部 Hook 的 Claude/VS Code 模板；
- `mise.macos.toml` 与 `mise.linux.toml`：相同 `config-*` 命令背后的两个平台适配器。

仓库和未来的全局 mise 配置都通过 `auto_env` 自动选择 `macos` 或 `linux`。公共模块不需要知道当前平台，平台路径也不会散落在公共调用入口中。

目前所有 `[dotfiles]`、`[tools]`、`[env]` 和 `_.path` 仍被注释。`.miserc.toml` 只改变本仓库内的平台配置发现，不会安装工具、写入 HOME 或激活 shell。

### 工具候选

公共 `dotfiles/common/mise/config.toml` 记录以下候选版本：

| 工具 | 版本 | 当前来源 / 注意事项 |
|---|---:|---|
| Node | 24.15.0 | 当前 Mac 由 nvm 管理；启用 mise 后停用 nvm |
| Python | 3.9.6 | 来自 Apple/Xcode，较旧；Linux 和 macOS 启用前都应重新评估 |
| Ruby | 4.0.6 | 当前 Mac 由 Homebrew 管理；迁移后从 Brewfile 和手工 PATH 移交 |
| Java | 21 | 当前 Mac 默认 JBR 21.0.6；mise 选择的发行版可能不同 |
| Rust | 1.98.0 | 当前由 rustup 管理；迁移时决定是否保留 rustup |
| Bun | 1.4.0 | 当前在 Brewfile 中；不得同时由 Homebrew 和 mise 拥有 |
| uv | 0.12.9 | 当前在 Brewfile 中；不得同时由 Homebrew 和 mise 拥有 |
| Yarn | 1.22.22 | 当前在 Brewfile 中；不得同时由 Homebrew 和 mise 拥有 |

这些版本来自当前 Mac，仅作为跨平台候选，不代表已经在真实 Linux 主机验证。Flutter 3.47.2 的本地工作树有改动，因此仍只保留 PATH 候选，不交给 mise。

### zsh

`dotfiles/common/zsh/zshrc` 现在只包含两个平台都能使用的 Zinit、Powerlevel10k、通用插件、OpenClaw 补全和本机私有配置入口。它会按需加载：

```text
~/.config/zsh/platform.zsh
```

两个平台分别提供这个文件：

- macOS：Homebrew nvm、Homebrew Ruby、pyenv 和当前 locale；
- Linux：存在时兼容 `~/.nvm` 与 `~/.pyenv`，不假定安装 Homebrew。

`zprofile` 也按平台拆分：macOS 使用 `/opt/homebrew`、`~/Library/Android/sdk` 和 macOS Toolbox 路径；Linux 使用可选 Linuxbrew、`~/Android/Sdk` 和 Linux Toolbox 路径。所有用户目录都改用 `$HOME`，不再把 `/Users/sbwoan` 写进公共 shell 配置。

公共 `zshenv` 只在目录真实存在时加入 `.local/bin`、git-ai、Gem、Cargo 和 Flutter 路径，并用 zsh 的唯一化数组消除重复 PATH。

### Claude 和 VS Code

Claude 的 `CLAUDE.md` 与自有 Hook 放在 `common`。Hook 运行环境会自动识别：

- Apple Silicon Homebrew：`/opt/homebrew/bin`；
- Linuxbrew：`/home/linuxbrew/.linuxbrew/bin`；
- 系统 `/usr/bin`、`/bin` 和 `/usr/local/bin`；
- macOS `shasum` 或 Linux `sha1sum`。

macOS Claude 模板保留当前 PromLight、SBBars、git-ai 和项目权限；Linux 模板只保留可跨平台的 Claude Hook 与 Flutter/Rust 命令，不伪造 macOS 应用路径。认证令牌和私有服务地址仍只允许放在仓库外的 `~/.config/mise/secrets.env`。

VS Code 分别映射到：

- macOS：`~/Library/Application Support/Code/User/settings.json`；
- Linux：`~/.config/Code/User/settings.json`。

Claude 和 VS Code 使用 mise `template` 模式，将 `{{ env.HOME }}` 渲染为目标机器的真实 HOME。`config-dry-run` 不执行模板渲染，只会把模板标为可能变更；需要查看渲染后的真实差异时使用 `mise bootstrap dotfiles diff`。

### 环境变量和 PATH

配置分为三个文件：

- `dotfiles/common/mise/config.toml`：工具版本、密钥文件入口和输出脱敏规则；
- `dotfiles/macos/mise/config.macos.toml`：macOS locale、Homebrew Ruby、Android 和 Toolbox；
- `dotfiles/linux/mise/config.linux.toml`：Linux Android 和 Toolbox 常见位置。

Linux locale 不会照搬 macOS。应先用发行版工具确认已经生成的 locale，再取消注释 `LANG` / `LC_ALL`。

真实密钥文件和 `*.local` 继续被 Git 忽略。`redactions` 只能降低输出泄漏风险，不是加密。

### 将来逐项启用

1. 审阅公共工具版本以及当前平台的 mise 配置。
2. 在根 `mise.toml` 中只启用公共映射，再在当前平台的 `mise.macos.toml` 或 `mise.linux.toml` 中启用对应映射。
3. 运行 `mise run config-status` 和 `mise run config-dry-run`，确认目标和来源都属于当前平台。
4. 处理已有真实文件与 symlink 的首次接管冲突后，再运行 `mise run config-sync`。
5. 最后启用 `eval "$(mise activate zsh)"`，并且每次只把一个工具从 nvm、Homebrew、rustup 或其他旧管理器迁给 mise。

从 `[dotfiles]` 删除条目不会自动删除已经应用的目标。取消管理时先执行 `mise bootstrap dotfiles unapply --dry-run`，确认后执行 `mise bootstrap dotfiles unapply`，再删除映射。

## 风险、取消和恢复

- dry-run 只能精确显示当前状态下的 orphan。Linux 包的安装原因降级后，可能新出现 orphan，所以 apply 会重新计算并第二次确认。
- 在第二次确认处取消时，pacman/APT/DNF 的安装原因可能已经变更，但包尚未卸载。查看当次 `.system-sync/history/.../demoted.txt` 和 `promoted.txt` 恢复需要保留的根包。
- Homebrew 在 cleanup 最后确认前取消时，前面的缺失软件安装可能已完成，但不会继续清理。
- 误删后，从 Git 恢复声明并重新安装。Git 只保存软件清单，不是包文件、应用数据或系统备份。

macOS 恢复 Brewfile 示例：

```bash
git restore platforms/macos/Brewfile
brew bundle install --no-upgrade --file=platforms/macos/Brewfile
```

## Git 管理

建议提交 `.miserc.toml`、`mise*.toml`、`dotfiles/`、`platforms/`、`scripts/` 和 `README.md`。每次修改声明先查看 diff 和 dry-run：

```bash
git diff
mise run dry-run
```

将这个仓库用到另一台机器时，不要直接 apply 原机器的完整 baseline。先根据新机器导出根包，合并差异，并将两台机器确实不同的部分拆成分支或额外 profile，不要靠长期忽略 dry-run 差异维持。

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
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
