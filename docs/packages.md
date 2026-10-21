# 系统软件包

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。系统软件声明放在 `packages/`；开发工具版本及项目依赖的分工见[开发环境](development-environment.md)。

## 平台与声明

| 平台 / profile | 声明来源 | 收敛方式 |
|---|---|---|
| macOS | `packages/macos/Brewfile` | Homebrew Bundle 安装和 cleanup |
| Arch / Manjaro 系 | `packages/linux/arch/mise.toml` | pacman/AUR 安装，再降级未声明 explicit 包并清理 orphan |
| Debian / Ubuntu 系 | `packages/linux/debian/mise.toml` | APT 安装，再调整 manual/auto 并 autoremove |
| Fedora / RHEL 系 | `packages/linux/fedora/mise.toml` | DNF 安装，再调整 user/dependency 原因并 autoremove |
| openSUSE / SLE | `packages/linux/opensuse/mise.toml` | mise 原生 zypper 安装，脚本校验后 `remove --clean-deps` |

mise 原生支持 `apt`、`dnf`、`pacman`、`aur` 和 `zypper`，负责读取声明与安装缺失项；本仓库的 Linux 脚本补充未声明根包的收敛、保护校验和移除事务审阅。

> **重要：** Linux 下的清单只是结构示例，不是你已有系统的 baseline。首次 `apply` 前必须按本文导入本机的手工根包。

## 执行顺序与审计

统一入口按查看、预览、确认后应用的顺序使用：

```bash
mise run status
mise run dry-run
mise run system-sync
```

`status` 只读地显示声明、当前根包和差异；`dry-run` 同时预览缺失包安装和收敛计划；`system-sync` 经交互确认后安装缺失项，再单独审阅删除阶段。

本仓库脚本的确认统一输入小写 `yes`；直接回车或其他输入取消。安装与删除仍分阶段审阅，每个确认点分别输入 `yes`，不会自动回答包管理器自身的事务确认。

平台脚本负责自己的完整预检和写操作保护。macOS 统一入口直接转交 Homebrew 脚本；Linux 的 `status` / `apply` 直接转交对应平台脚本，状态中的原生 JSON 只查询一次。Linux `dry-run` 会先显示原生安装预览，因此在这一步之前仍保留外层预检，再进入平台清理预览。

所有包写入口共用操作锁。Linux apply 的顺序是：**终端/版本预检 → 加锁并建立审计 → 读取声明和当前状态 → 校验保护项及变更阈值 → 输入 `yes` 确认 → 保存安装前状态 → 原生 mise 安装 → 重新校验 → 调整安装原因及再次审阅删除事务 → 执行删除**。直接运行平台脚本同样经过保护；mise 原生安装阶段可能使用非交互参数，因此本仓库的 `yes` 确认始终在它之前。

整机操作和升级的 `transaction.json` 记录 `preflight`、`installing` / `upgrading`、`marking`、`removing` 等阶段，以及最终 `complete` / `cancelled` / `failed` / `interrupted` 状态。安装失败也会保留 `.before` 清单和安装前包列表；包装层要求平台脚本明确记录完成，不能只凭退出码 0 判定成功。审计不是系统事务回滚，失败后应先检查 `last_phase` 与实际包状态。

Linux 使用 `/etc/os-release` 自动选择 profile。当前映射为：

- Arch、Manjaro、EndeavourOS、CachyOS → `arch`；
- Debian、Ubuntu、Linux Mint、Pop!_OS、Kali、Raspbian → `debian`；
- Fedora、RHEL、CentOS、Rocky、AlmaLinux、Oracle Linux → `fedora`；
- openSUSE Leap/Tumbleweed、SLES、SLED → `opensuse`。

其他发行版不会猜测执行，而是失败关闭。Linux 统一升级暂未实现；请继续使用发行版自身的升级流程。

## 日常添加 / 删除一个包

使用仓库提供的 **`mise run package-install` / `mise run package-uninstall`** 安装或卸载系统软件，并同步项目软件清单。`mise install` / `mise uninstall` 是 mise 原生命令，管理开发工具版本，与这两个 task 分开使用。

在仓库根目录运行，例如：

```bash
# 先预览：不会安装、卸载或写入清单
mise run package-install --dry-run gitui
mise run package-uninstall --dry-run gitui

# macOS 默认为 Homebrew formula；Linux 自动选择本机的包管理器
mise run package-install gitui
mise run package-uninstall gitui

# Homebrew cask 必须明确指定类型
mise run package-install --cask zed
mise run package-uninstall --cask zed

# Arch AUR；先准备 paru 或 yay
mise run package-install --aur visual-studio-code-bin
mise run package-uninstall visual-studio-code-bin

# 第三方 Homebrew 包：先审核来源，再明确授权这一个完整包名
mise run package-install --cask --trust abue-ammar/tinycast/tinycast
```

每次处理**一个准确包名**，先展示原生命令与清单 diff；输入 `yes` 后才执行，直接回车或其他输入取消。保留原生包管理器自身的依赖检查和确认，不自动传递 `--yes`。请用普通用户运行，不要 `sudo mise`；需要提权的 Linux 原生命令会自行调用 `sudo`。

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

**支持边界：**由预编译 Go helper 执行，无需 Python。Homebrew 自动编辑器支持本仓库当前的静态单行 `tap` / `brew` / `cask` 声明及 `trusted`、`link` 布尔选项，不执行或猜测动态 Ruby。Linux TOML 由 `mise config get --file` 的官方解析器校验，然后仅编辑 `[bootstrap.packages]` 下的单行条目；目标安装声明限 `"latest"` 或 `{ version = "latest" }`。多行包声明、其他选项、版本固定、虚拟包、包组、URL/本地文件、通配符或任意原生参数不由此入口处理，遇到不支持格式会停止。必要的元数据查询可能读取网络或刷新包管理器自身缓存，但 `--dry-run` 不运行原生安装/卸载命令，也不修改仓库清单或真实信任库。

直接执行 `brew install`、`apt install`、`pacman -S` 等原生命令**不会被监听**，它们的卸载命令也不会自动删清单。单包入口不修改 Shell 启动文件，也不注册 hook 或后台轮询。以后从统一 mise 入口操作，或继续手工编辑清单后运行 `system-sync`；不要在两种习惯间切换时忘记同步声明。Linux 初次迁移的 baseline 导入仍不可省略，单包入口不会自动接管其余已安装软件。


中断、清单写入失败及恢复步骤见[恢复流程](recovery.md#中断与写入失败的恢复)。

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
    | ./helper/scripts/run.sh parse-zypper solvables - \
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

所有 Linux profile 都需要先准备 Bash、mise 2026.10.11+、tar、SHA256 校验工具及对应包管理器。[Go helper](../helper/README.md) 的四平台归档随仓库保存，首次调用时自动校验并解压，也可提前运行 `mise run helper-install`。共享锁、版本预检、JSON/XML 解析、整机审计及单包清单编辑均使用预编译工具，日常执行不要求 Go 编译器或 Python。各 Linux profile 还声明了 `zsh` 和 `jq`，并将 `zsh` 加入保护清单。

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
  | ./helper/scripts/run.sh parse-zypper solvables - \
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

1. 先校验声明、保护项与变更阈值，经 `yes` 确认并保存安装前状态后，再由 mise 安装缺失的 pacman/AUR 声明。
2. 脚本只比较 `pacman -Qqe` 的 explicit 包，不把整棵依赖树当成根包。
3. 先用 `pacman -D --asexplicit` 保护声明/保护根，再用 `--asdeps` 降级未声明根。
4. 重新查询 `pacman -Qdtq`，用 `pacman -Rsp` 预演完整递归事务。
5. 事务不触及声明/保护包时，才会在再次确认后执行 `pacman -Rs`。

默认不使用 `-Rns`，因此不会额外按 `-n` 语义删除配置备份。

### Debian / Fedora

APT 用 `apt-mark manual/auto`，DNF5 用 `dnf mark user/dependency`，DNF4 用对应的 `dnf mark install/remove`。两者都会：

1. 只把 manual/user-installed 包与 mise 声明比较；
2. 前置校验和 `yes` 确认后，保存安装前状态，由 mise 安装缺失声明并重新计算计划，再提升声明和保护根、降级未声明根；
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
4. 再次输入 `yes` 确认后，由交互式 `zypper --no-refresh remove --clean-deps` 在与预演相同的缓存仓库元数据上执行。

脚本故意不自动删除 `--orphaned` 包，因为它们可能是本地 RPM 或已移除仓库的必需软件。openSUSE 是四个 Linux profile 中最需人工审核 dry-run 的一个。

### 通用失败关闭条件

以下任一情况会停止，不进入卸载：

- 声明为空，或缺少 `base` / `base-files` / `filesystem` 必备根；
- mise JSON 或 zypper XML 结构无法可靠解析；
- mise manager 不可用、条目被跳过、状态未知或声明为 `absent`；根清单只接受需要保留的包，删除通过单包入口或审阅后的根包收敛完成；
- 包名含不安全字符，或 virtual provider 无法映射到具体已安装包；
- 移除事务触及声明包、硬保护包、profile allowlist 或动态保护的内核/引导包；
- 一次降级/移除超过数量或比例阈值，且没有显式传入 `--allow-large-change`；
- `apply` 不在交互式终端中运行，或未输入 `yes` 确认。

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

如果 Homebrew cleanup 报告 formula 依赖图循环，并且同时提出移除受管项目，脚本会显示警告并要求额外输入 `yes` 确认。应先审阅事务，必要时修复 Homebrew 旧 keg 收据后再继续。

单独升级 Brewfile 声明项、不 cleanup：

```bash
mise run macos-upgrade
```

## 隔离测试

完整检查会显式构建本机 Go helper，运行 Go race 检查和 Python 测试；测试使用替身包管理器，不修改本机软件：

```bash
mise run check
```

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
- [mise zypper manager](https://mise.jdx.dev/bootstrap/packages/zypper.html)
- [Homebrew Bundle](https://docs.brew.sh/Brew-Bundle-and-Brewfile)
- [ArchWiki: pacman tips and tricks](https://wiki.archlinux.org/title/Pacman/Tips_and_tricks)
- [Debian apt-mark](https://manpages.debian.org/testing/apt/apt-mark.8.en.html)
- [Debian apt-get](https://manpages.debian.org/bookworm/apt/apt-get.8.en.html)
- [DNF command reference](https://dnf.readthedocs.io/en/stable/command_ref.html)
- [DNF5 mark](https://dnf5.readthedocs.io/en/stable/commands/mark.8.html)
- [openSUSE zypper manual](https://manpages.opensuse.org/Tumbleweed/zypper/zypper.8.en.html)
