# 新机器重建与锁文件

[返回项目首页](../README.md)

先准备 Git、Bash、mise、tar、SHA256 校验工具和系统原生包管理器。当前验收使用 mise 2026.10.15；Python 检查入口和尚未迁移的 task 要求 3.11+，部署配置本身可先使用不依赖 Python 的原生命令。审计和整机包流程首次运行时，自动从随 clone 保存的 `prebuilt/<版本>/` 安装 [Go helper](go-helper.md)，无需先安装 Go 或联网下载 helper。系统软件、开发工具和应用配置分别安装，不把 Linux 的示例软件清单直接应用到新机器。

## 部署与安装

在克隆后的仓库根目录执行，已有配置先备份，再审阅差异：

```bash
mise trust --all
# 可选：提前初始化 helper；首次 audit/status 也会自动完成
mise run helper-install
mise bootstrap dotfiles diff
mise bootstrap dotfiles apply
mise install --locked
mise ls --current
mise run audit
```

普通文件冲突会停止部署。只对已经备份、确认接管的具体目标使用 `mise bootstrap dotfiles apply --force <目标>`。系统包清单按[首次导入 baseline](packages.md)流程另行处理。

`mise install --locked` 安装你声明的开发工具，可能包含 Go；这是开发环境的选择，helper 初始化不依赖这一步。仅准备 helper 时执行 `mise run helper-install` 即可，甚至可以直接运行 `./scripts/system-sync-helper.sh --version`。开发机修改源码后才需要构建和完整 `mise run check`；更新源码时还需通过 `mise run helper-bundle` 一同更新仓库内的预编译包。

公共配置中的 `secrets.env` 是可选文件，zsh 私有覆盖也按存在性加载。缺失时可完成公共配置重建，不会生成假凭据。需要的本机变量、应用认证和私有偏好通过安全渠道另行准备，不能追加到公共来源或锁文件。

## 全局锁文件

| 全局目标 | 仓库来源 |
|---|---|
| `~/.config/mise/mise.lock` | `dotfiles/common/mise/mise.lock` |
| `~/.config/mise/mise.macos.lock` | `dotfiles/macos/mise/mise.macos.lock` |

两者随配置部署为链接。在仓库内或仓库外运行 mise 都能读取同一份锁；npm 依赖图的相对路径从锁文件真实位置解析，`dotfiles/common/mise/.mise/locks/` 也必须提交和保留。HOME 中首次生成时可能使用 `locks/`，链接到仓库后由 mise 维护为 `.mise/locks/`，以锁中引用和 `mise lock --global --sidecars` 输出为准。

更新现有版本的下载元数据、补平台锁记录：

```bash
mise lock --global --platform macos-arm64,macos-x64,linux-x64,linux-arm64
mise lock --global --sidecars
git diff -- dotfiles/common/mise dotfiles/macos/mise
```

此命令不会升级固定的版本声明。逐个升级仍使用 `mise upgrade --global --bump --no-prune <工具>`；之后一起审阅配置、锁文件及依赖图的变化。`.local` 配置对应的锁文件和依赖图不提交。规则见 [mise 全局锁文件](https://mise.jdx.dev/dev-tools/mise-lock.html#global-lockfiles)与[锁文件格式](https://mise.jdx.dev/dev-tools/mise-lock-reference.html)。

锁定的版本与下载 URL 是两层保证：Node、Python、Bun、uv、Java 和支持的平台 Ruby / Flutter 记录后端提供的 URL 与校验值；Rust 由 rustup 安装，锁中记录精确版本和组件参数，下载校验由 rustup 完成。`--locked` 不表示离线，也不会为后端补造校验值。Flutter 官方清单当前没有 Linux ARM64 归档，该平台须先选择受支持的 SDK 来源。

## Java 与 Flutter

JBR `jetbrains-21.0.6-b895.109` 已核实在 mise Java 后端可用。新机器执行 `mise install --locked java` 会下载锁中 JetBrains 官方归档，无须复制当前机器的 JDK 路径；当前机器仍复用原 JBR。需要复用其他现有 JDK 时，先核对版本，再按[官方已有 JDK 接入方式](https://mise.jdx.dev/lang/java.html#use-a-jdk-installed-elsewhere)登记。

Flutter 固定到已使用的 3.47.2，Dart 随 Flutter 提供。新机器的 mise HTTP 后端从 Flutter 官方归档安装；当前机器使用 `mise link flutter@3.47.2 <SDK目录>` 复用原 SDK。旧 SDK 的本地补丁和原始文件仅保存在本机备份，重装官方归档不会恢复这些改动；需要时在本机审阅补丁后恢复。不要在链接的 SDK 中直接运行 `flutter upgrade`，以免目录内容与 mise 登记版本不符。参见 [mise HTTP 后端](https://mise.jdx.dev/dev-tools/backends/http.html)。

## 验收范围

本轮在空 HOME、独立 mise 缓存、独立 Cargo / rustup 目录中重装了原有八项工具，均运行版本命令并核对安装路径；包括从官方归档重新安装 JBR。Flutter 使用现有 SDK 验证链接和项目流程，未在此重建环境重复下载整套 SDK。

CI 会在 macOS / Linux 下把完整公共来源部署到临时 HOME，检查重复应用、锁文件链接、npm 依赖图和私有键保留；它不安装宿主系统软件或下载整套全局工具。Linux 真实机器的软件收敛、Android SDK、IDE 和所有开发工具重装仍须按实际机器验收。
