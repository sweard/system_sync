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
├── .gitignore
├── README.md
├── mise.toml                         # 公共 task 入口
├── dotfiles/                         # 当前电脑的脱敏候选快照；映射尚未启用
│   ├── mise/
│   │   ├── config.toml               # 已注释的 tools / env / PATH 候选
│   │   └── secrets.env.example
│   ├── zsh/
│   │   ├── zshenv
│   │   ├── zprofile
│   │   └── zshrc
│   ├── claude/
│   │   ├── CLAUDE.md
│   │   ├── hooks/
│   │   └── settings.json             # 已移除认证令牌
│   └── vscode/
│       └── settings.json
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

`mise.toml` 设置了 `monorepo_root = true`，并在 `[monorepo].config_roots` 中显式列出 Arch、Debian 和 Fedora 的子配置。这样在对应 profile 目录中运行 mise 时，它会同时读取根配置的 task 和子目录的包声明，不再需要根目录的 `mise.linux.toml` 或 `.miserc.toml`，也不依赖已废弃的自动目录扫描。

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

当前根配置中的全部 `[dotfiles]` 映射都被注释，因此这三个命令不会接管或修改任何用户文件。它们是为以后逐项启用映射预留的入口。

Linux 使用 `/etc/os-release` 自动选择 profile。当前映射为：

- Arch、Manjaro、EndeavourOS、CachyOS → `arch`；
- Debian、Ubuntu、Linux Mint、Pop!_OS、Kali、Raspbian → `debian`；
- Fedora、RHEL、CentOS、Rocky、AlmaLinux、Oracle Linux → `fedora`；
- openSUSE Leap/Tumbleweed、SLES、SLED → `opensuse`。

其他发行版不会猜测执行，而是失败关闭。Linux 统一升级暂未实现；请继续使用发行版自身的升级流程。

## 首次迁移 Linux baseline

所有 Linux profile 都需要 Bash、Python 3、mise 和对应的包管理器。APT/DNF/Arch 可先由 mise 安装 profile 中缺失的 Python；openSUSE 脚本自身需要 Python 解析 zypper XML，因此首次运行前必须已安装 `python3`。

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

## 开发环境和应用配置候选快照

`dotfiles/` 保存了 2026-09-04 从当前 Mac 盘点得到的候选配置，但目前没有任何文件映射处于启用状态，也没有启用全局 `[tools]`、`[env]` 或 `_.path`。本次纳入 Git 不会执行工具安装，不会修改 `~/.zshrc`、Claude、VS Code 或 `~/.config/mise/config.toml`。

### 当前工具基线

| 工具 | 当前来源 | 盘点版本 | 候选处理 |
|---|---|---:|---|
| Node | nvm 0.40.7 | 24.15.0 | 已写入注释的 `[tools]`；启用时停用 nvm |
| Python | Apple / Xcode | 3.9.6 | 仅记录当前版本，正式迁移前应重新选择版本 |
| Ruby | Homebrew | 4.0.6 | 已写入注释的 `[tools]`；启用时从 Brewfile 和手工 PATH 移交 |
| Java | 本机 JDK，默认 JetBrains Runtime | 21.0.6 | 候选声明为 Java 21；mise 安装的发行版可能不同 |
| Rust | rustup stable | 1.98.0 | 已写入注释的 `[tools]`；启用时决定是否继续保留 rustup |
| Bun | Homebrew | 1.4.0 | 候选已注释；不得与 Brewfile 同时拥有 |
| uv | Homebrew | 0.12.9 | 候选已注释；不得与 Brewfile 同时拥有 |
| Yarn | Homebrew | 1.22.22 | 候选已注释；不得与 Brewfile 同时拥有 |
| Flutter | 本地 stable checkout | 3.47.2，工作树有改动 | 只保留现有 PATH，不交给 mise，避免覆盖本地改动 |

当前 zsh 中还有 pyenv 初始化片段，但盘点时 `~/.pyenv/bin/pyenv` 不存在，所以没有把 pyenv 当成现有工具基线。Java 18、17 和 11 的已安装 JDK 也没有纳入全局默认工具声明；项目需要时应在各自的 `mise.toml` 中声明。

### 环境变量和 PATH

候选全局配置位于 `dotfiles/mise/config.toml`，其中所有有效行都以 `#` 注释。它记录：

- `LANG=zh`、`LC_ALL=en_US.UTF-8`、`ANDROID_HOME`；
- Homebrew Ruby 当前使用的 `LDFLAGS` 和 `CPPFLAGS`，以及现有但暂时无可执行文件的 `PYENV_ROOT`；
- `.local/bin`、git-ai、Homebrew Ruby、pyenv、Cargo、Flutter、Gem、JetBrains Toolbox 和 Android SDK 路径；
- 当前 Node、Python、Ruby、Java、Rust、Bun、uv、Yarn 版本。

PATH 清单只取自持久的 zsh 配置，已经排除 Codex 会话临时目录、系统自动注入目录和重复项。启用某个 mise 工具时，应同时删除 nvm、rustup、Homebrew Ruby 等对应的手工初始化或 PATH，避免同一个工具存在两个所有者。

### zsh、Claude 和 VS Code

- `dotfiles/zsh/` 是当前三个 shell 入口文件的候选快照。个人姓名和内网地址没有进入 Git，未来通过 `~/.zshrc.local` 本机私有文件加载。Powerlevel10k 的 `~/.p10k.zsh` 暂未纳管。
- `dotfiles/claude/` 保留当前 Claude Code 的全局说明、分支缓存/工作日志 Hook、模型、权限和插件设置，但从 `settings.json` 明确删除了 `ANTHROPIC_AUTH_TOKEN` 和私有 `ANTHROPIC_BASE_URL`。部分 Hook 仍指向本机安装的 PromLight、SBBars 和 git-ai，迁移到其他机器前要检查这些路径。
- `dotfiles/vscode/settings.json` 是当前 macOS VS Code 用户设置快照。若继续使用 VS Code Settings Sync，不应同时启用这个文件的 mise copy 映射。
- Claude 和 VS Code 使用 `copy` 候选模式，因为应用可能主动改写 JSON；zsh 和全局 mise 配置使用 `symlink` 候选模式。

真实密钥只应放在仓库外的 `~/.config/mise/secrets.env`。仓库提供 `dotfiles/mise/secrets.env.example`，并忽略任意 `dotfiles/**/secrets.env` 和 `dotfiles/**/*.local`。`redactions` 只能减少 mise 输出泄漏，不能代替加密。

### 将来逐项启用

不要一次打开全部映射。推荐顺序：

1. 审阅 `dotfiles/mise/config.toml`，先只取消注释 `LANG`、`LC_ALL`、`ANDROID_HOME` 和没有冲突的 PATH。
2. 在根 `mise.toml` 中只取消注释 `~/.config/mise/config.toml` 映射，运行 `mise run config-dry-run`；确认后才运行 `mise run config-sync`。
3. 在现有 `~/.zshrc` 中手工加入 `eval "$(mise activate zsh)"`，重新打开终端并验证。
4. 每次只迁移一个工具。先启用对应 `[tools]`，验证版本，再从 nvm、Homebrew、rustup 或手工 PATH 移除旧所有权。
5. 最后分别考虑 zsh、Claude 和 VS Code 映射。启用 whole-file 映射前必须先看 dry-run；mise 默认会拒绝覆盖未管理的现有文件，不要为了省事直接使用 `--force`。

从 `[dotfiles]` 删除条目不会自动删除已经应用的目标文件。如果将来需要取消管理，应先运行 `mise bootstrap dotfiles unapply --dry-run`，确认后执行 `mise bootstrap dotfiles unapply`，再删除映射。

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

建议提交 `mise.toml`、`platforms/`、`scripts/` 和 `README.md`。每次修改声明先查看 diff 和 dry-run：

```bash
git diff
mise run dry-run
```

将这个仓库用到另一台机器时，不要直接 apply 原机器的完整 baseline。先根据新机器导出根包，合并差异，并将两台机器确实不同的部分拆成分支或额外 profile，不要靠长期忽略 dry-run 差异维持。

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
- [mise Environments](https://mise.jdx.dev/environments/)
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
