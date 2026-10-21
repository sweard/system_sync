# Arch 软件清单与 Brewfile 对照

[系统软件包说明](../../../docs/packages.md) · [Arch 配置](mise.toml) · [Brewfile](../../macos/Brewfile)

核对日期：2026-10-21。以 Brewfile 的 33 个软件条目（21 个 formula、12 个 cask）为来源，查询 [Arch Linux 官方包数据库](https://archlinux.org/packages/)；tap 本身不作为软件包映射。先按名称查询，再结合名称差异、描述及上游项目核对。

19 项找到同一软件的官方包，均在 `extra/x86_64`；其中 `git` 原已声明，本次新增 18 项。原有的 8 项基础声明保留，当前共 26 个 `pacman:` 声明。版本沿用 `latest`，由目标机器的软件仓库解析。

本次查询范围是 Arch Linux 官方稳定仓库 `core`、`extra`、`multilib`，不纳入 testing 或 AUR。派生发行版和 Arch Linux ARM 仍需在目标机器核对可用性；此清单也未代替目标机器的 baseline 导入和真实安装验收。

## 已加入的对应关系

| Brewfile 条目 | Arch 官方包 | 说明 |
|---|---|---|
| `brew "cmake"` | [cmake](https://archlinux.org/packages/extra/x86_64/cmake/) | 同名 |
| `brew "docker"` | [docker](https://archlinux.org/packages/extra/x86_64/docker/) | 同一项目；这里只声明包，不启用服务 |
| `brew "fastfetch"` | [fastfetch](https://archlinux.org/packages/extra/x86_64/fastfetch/) | 同名 |
| `brew "fd"` | [fd](https://archlinux.org/packages/extra/x86_64/fd/) | 同名 |
| `brew "gh"` | [github-cli](https://archlinux.org/packages/extra/x86_64/github-cli/) | 包名不同，GitHub CLI |
| `brew "git"` | [git](https://archlinux.org/packages/extra/x86_64/git/) | 原已声明，未重复添加 |
| `brew "git-lfs"` | [git-lfs](https://archlinux.org/packages/extra/x86_64/git-lfs/) | 同名 |
| `brew "htop"` | [htop](https://archlinux.org/packages/extra/x86_64/htop/) | 同名 |
| `brew "librsvg"` | [librsvg](https://archlinux.org/packages/extra/x86_64/librsvg/) | 同名 |
| `brew "llama.cpp"` | [llama-cpp](https://archlinux.org/packages/extra/x86_64/llama-cpp/) | 包名不同，上游为 `ggml-org/llama.cpp` |
| `brew "lsd"` | [lsd](https://archlinux.org/packages/extra/x86_64/lsd/) | 同名 |
| `brew "neovim"` | [neovim](https://archlinux.org/packages/extra/x86_64/neovim/) | 同名 |
| `brew "poppler"` | [poppler](https://archlinux.org/packages/extra/x86_64/poppler/) | 同名 |
| `brew "scrcpy"` | [scrcpy](https://archlinux.org/packages/extra/x86_64/scrcpy/) | 同名 |
| `brew "sevenzip"` | [7zip](https://archlinux.org/packages/extra/x86_64/7zip/) | 包名不同，7-Zip |
| `brew "mise"` | [mise](https://archlinux.org/packages/extra/x86_64/mise/) | 任务运行器纳入系统包声明 |
| `cask "codex"` | [openai-codex](https://archlinux.org/packages/extra/x86_64/openai-codex/) | [Homebrew 此 cask](https://formulae.brew.sh/api/cask/codex.json) 为 Codex CLI |
| `cask "firefox"` | [firefox](https://archlinux.org/packages/extra/x86_64/firefox/) | 同名 |
| `cask "ghostty"` | [ghostty](https://archlinux.org/packages/extra/x86_64/ghostty/) | 同名 |

## 未加入的条目

以下 14 项在本次查询中未找到同一上游软件的官方包，因此没有新增声明。这里不推断其 AUR 可用性；已有 AUR 示例继续保留为注释。

| Brewfile 条目 | 核对依据 |
|---|---|
| `brew "antoniorodr/memo/memo"` | [官方包名查询 memo](https://archlinux.org/packages/search/json/?name=memo) 未命中 |
| `cask "android/tap/android-cli"` | [官方查询 android-cli](https://archlinux.org/packages/search/json/?q=android-cli) 未命中；不以其他 Android 工具代替 |
| `brew "cocoapods"` | [官方查询 cocoapods](https://archlinux.org/packages/search/json/?q=cocoapods) 未命中 |
| `brew "colima"` | [官方查询 colima](https://archlinux.org/packages/search/json/?q=colima) 未命中 |
| `brew "herdr"` | [官方查询 herdr](https://archlinux.org/packages/search/json/?q=herdr) 未命中 |
| `brew "tlrc"` | [官方查询 tlrc](https://archlinux.org/packages/search/json/?q=tlrc) 未命中；见下文客户端区别 |
| `cask "espanso"` | [官方查询 espanso](https://archlinux.org/packages/search/json/?q=espanso) 未命中 |
| `cask "font-maple-mono"` | [官方查询 maple](https://archlinux.org/packages/search/json/?q=maple) 没有 Maple Mono 字体包 |
| `cask "font-maple-mono-nf"` | 同上，未找到该字体变体 |
| `cask "font-maple-mono-nf-cn"` | 同上，未找到该字体变体 |
| `cask "google-chrome"` | [官方包名查询 google-chrome](https://archlinux.org/packages/search/json/?name=google-chrome) 未命中 |
| `cask "iterm2"` | [官方包名查询 iterm2](https://archlinux.org/packages/search/json/?name=iterm2) 未命中 |
| `cask "keka"` | [官方包名查询 keka](https://archlinux.org/packages/search/json/?name=keka) 未命中 |
| `cask "homebrew-app"` | [官方包名查询 homebrew-app](https://archlinux.org/packages/search/json/?name=homebrew-app) 未命中 |

Homebrew 的 [tlrc](https://formulae.brew.sh/formula/tlrc) 是 `tldr-pages/tlrc` Rust 客户端；Arch 官方的 [tldr](https://archlinux.org/packages/extra/any/tldr/) 来自 `tldr-pages/tldr-python-client`，[tealdeer](https://archlinux.org/packages/extra/x86_64/tealdeer/) 则来自 `tealdeer-rs/tealdeer`。它们不是同一个客户端，本次未自动替换。

以上是一次核对快照。Brewfile 后续变化时需重新查询并审阅对应关系，不会自动跨平台同步。
