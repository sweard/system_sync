# Arch Linux + macOS 声明式系统包同步

这个仓库用一组统一的 mise task 管理两套平台，但保留各自最可靠的包管理语义：

| 平台 | 声明来源 | 收敛方式 |
|---|---|---|
| Arch Linux | `mise.toml` 中的 `pacman:` / `aur:` 根包 | 降级未声明 explicit 包，再用 `pacman -Qdtq` + `pacman -Rs` 清理 orphan |
| macOS | `platforms/macos/Brewfile` | `brew bundle install` 安装，再由 `brew bundle cleanup` 计算 formula/cask/tap 事务 |

macOS 不把既有 cask 强行转移给 mise。Homebrew 继续拥有和管理现有 formula/cask；mise 只是统一任务入口。这样从 Brewfile 删除 cask 后，Homebrew 能可靠预演和卸载，而不是依赖 mise 对既有 cask 的有限 prune 支持。

## 文件结构

```text
system_sync/
├── .gitignore
├── README.md
├── mise.toml
├── config/
│   ├── homebrew-protected.txt
│   └── protected-packages.txt
├── platforms/
│   └── macos/
│       ├── Brewfile
│       └── trusted-formulae.txt
└── scripts/
    ├── extract-mise-packages.py
    ├── homebrew-converge.sh
    ├── pacman-converge.sh
    └── system-sync.sh
```

运行时审计清单保存在 `.system-sync/history/<UTC 时间>/`，该目录已被 Git 忽略。

## 统一入口

安装并信任 mise 后，两端命令相同：

```bash
mise run status
mise run dry-run
mise run system-sync
```

`scripts/system-sync.sh` 根据 `uname` 自动选择 Homebrew 或 pacman 流程。脚本本身默认 dry-run，因此也可以直接运行：

```bash
./scripts/system-sync.sh
./scripts/system-sync.sh --status
```

## macOS：当前机器基线

`platforms/macos/Brewfile` 已在 2026-09-04 根据当前机器手工核对：

- 28 个当前主动安装的 formula 根包；
- 12 个当前 Homebrew cask；
- 2 个当前第三方 tap；
- 另外加入当前尚未安装的 `mise`，作为本仓库启动依赖。

因此 Brewfile 当前共有 29 个 formula 声明。Homebrew 自动安装的库依赖没有被误写为根包。

当前 formula 根包：

```text
bun                 cmake          cocoapods       colima
docker              fastfetch      fd              gh
git                 git-lfs        gitui           herdr
htop                librsvg        llama.cpp       lsd
memo                neovim         nvm             openclaw-cli
poppler             ruby           scrcpy          sevenzip
tlrc                uv             yarn            zeroclaw
```

`mise` 是新增的启动依赖，不在上面的原始 28 项中。

当前 `openclaw-cli` 和 `ruby` 均已安装但未链接，Brewfile 用 `link: false` 保留这一状态。当前根 formula 没有非默认安装选项，Homebrew service 也没有正在运行的项目，因此基线没有写入额外 `args` 或 `restart_service`。

当前 cask：

```text
codex               firefox              font-maple-mono
font-maple-mono-nf  font-maple-mono-nf-cn ghostty
google-chrome       iterm2               keka
openclaw            raycast              zed
```

当前 tap：

```text
antoniorodr/memo
oven-sh/bun
```

`bun` 和 `memo` 的 Homebrew 安装收据均标记为 `installed_on_request: true`。Homebrew 6 因这两个 tap 尚未受信，普通查询会漏掉它们；本 Brewfile 明确保留两项，并仅用 `trusted: true` 信任两个完整 formula 名，不授予整个 tap 的信任。

`platforms/macos/trusted-formulae.txt` 保存相同的两个完整名称，供 cleanup 预演创建一次性的临时信任库。临时库位于系统临时目录，不读取、不覆盖用户的真实 Homebrew 信任库，脚本结束即删除。如果以后新增受信第三方 formula，需要同时更新 Brewfile 的 `trusted: true` 条目和这份清单；两边不一致时脚本失败关闭。

## macOS：首次启用

当前机器尚未安装 mise。先运行只读预览，此命令不依赖 mise：

```bash
./scripts/system-sync.sh --dry-run
```

然后只安装 Brewfile 中缺失的项目。这个步骤不会删除未声明软件，也不会主动升级已安装软件：

```bash
brew bundle install \
  --no-upgrade \
  --file=platforms/macos/Brewfile
```

这会安装 `mise`，并应用 Brewfile 中对 `bun`、`memo` 两个具体 formula 的最小信任声明。随后：

```bash
mise trust
mise run status
mise run dry-run
```

逐项确认输出后再执行：

```bash
mise run system-sync
```

实际 apply 至少有三层防护：

1. mise task 先询问是否进入修改流程；
2. 脚本要求输入精确短语 `APPLY HOMEBREW`；
3. 安装完成后，Homebrew 自己再次列出完整 cleanup 内容并询问。

如果 Homebrew 同时报告依赖图循环并提出移除受管项目，在 Homebrew 最终确认前还必须输入 `ALLOW HOMEBREW GRAPH WARNING`。

脚本不向 cleanup 传 `--force`，不自动回答 Homebrew 的确认，也不使用 `--zap`。

## macOS：status、dry-run 和 apply 的范围

脚本只管理以下三类：

- formula；
- cask；
- tap。

它明确不会收敛 MAS 应用、Cargo/npm/uv 工具、VS Code 扩展、Go 包等其他 Brew Bundle 类型。

`--status` 和 `--dry-run` 会显示：

- Brewfile 声明；
- 尚未安装的 formula/cask/tap；
- 可识别的未声明根 formula、cask 和 tap；
- `brew bundle check` 结果；
- Homebrew 自己计算的精确 cleanup 预演。

cleanup 预演将标准输入断开，因此 Homebrew 无法获得确认，只会打印 `Would uninstall` / `Would untap`，不会修改系统。

若 Homebrew 同时报告 formula 依赖图循环，并提出移除 formula、cask 或 tap，脚本会显示醒目警告，并要求输入额外的精确确认短语。这样不会因无关的旧依赖收据永久阻断收敛，同时仍保留 cleanup 精确预演、专用确认和 Homebrew 自身确认。若预演包含循环中的包或其他非预期项目，应取消操作并先修复 Homebrew 元数据。

`--apply` 顺序为：

1. `brew bundle install --no-upgrade`；
2. `brew bundle check --no-upgrade`；
3. 安装后重新生成一次 cleanup 精确预演并重新执行安全检查；
4. 交互式 `brew bundle cleanup --formula --cask --tap`。

## macOS：日常维护

新增 formula：

```ruby
brew "jq"
```

新增 cask：

```ruby
cask "visual-studio-code"
```

编辑 `platforms/macos/Brewfile` 后：

```bash
mise run dry-run
mise run system-sync
```

删除软件时，从 Brewfile 删除对应行，先看 dry-run，再运行 system-sync。不要直接把所有 `brew list --formula` 输出写回 Brewfile；其中大多数是依赖，不是根包。

保护项放在 `config/homebrew-protected.txt`，格式为：

```text
formula:git
formula:mise
cask:some-critical-app
tap:owner/repository
```

保护项必须同时存在于 Brewfile。若只从 Brewfile 删除，apply 会失败关闭；真正删除保护项时必须同时修改两个文件。

升级与收敛分开。只升级 Brewfile 声明项、不执行 cleanup：

```bash
mise run macos-upgrade
```

它还会要求输入 `UPGRADE HOMEBREW`。

## macOS：风险和恢复

Homebrew cleanup 的 dry-run 输出是最终判断依据。它会根据 Brewfile 保留声明 formula、cask 及其依赖，并清理其他受管项目。执行 cleanup 还会：

- 把 Homebrew 全局信任存储重置为 Brewfile 中声明的信任；
- 在接受确认后运行 Homebrew 自身 cleanup；
- 删除 cask 应用本体，但本项目不使用 `--zap`，不会主动要求删除其全部用户数据。

每次 apply/upgrade 前，脚本保存 Brewfile、formula、cask、tap 和 cleanup 预演。取消 Homebrew 最后一层 cleanup 时，前面的缺失软件安装可能已经完成，但不会继续清理。

误删后先从 Git 恢复 Brewfile 条目，再重新同步：

```bash
git restore platforms/macos/Brewfile
brew bundle install --no-upgrade --file=platforms/macos/Brewfile
```

若 Brewfile 的删除本身已经提交，应恢复对应历史版本或手工加回条目。Git 管理的是软件清单，不是应用数据备份；重要应用数据仍需独立备份。

## macOS：在其他机器使用

不要直接把本机完整基线应用到另一台 Mac。先复制 Brewfile，再按机器需求删减，尤其检查：

- 专用开发工具和大型模型工具；
- 浏览器、终端和编辑器是否需要全部安装；
- 第三方 tap 及其具体 formula 是否愿意信任；
- Intel 与 Apple Silicon 的兼容性。

先运行 `brew bundle check` 和脚本 dry-run，确认 cleanup 预演为空或完全符合预期，再 apply。

## Arch Linux：建立 baseline

Arch 仍以 `mise.toml` 中的 `pacman:` / `aur:` 项为显式根包。首次使用前先导出：

```bash
pacman -Qqen | LC_ALL=C sort -u > native-explicit.txt
pacman -Qqem | LC_ALL=C sort -u > foreign-explicit.txt
```

- `native-explicit.txt` 对应 `pacman:`；
- `foreign-explicit.txt` 通常是 AUR 候选，但也可能包含本地构建或 `pacman -U` 包，必须逐项确认。

生成 TOML 片段：

```bash
awk '{ printf "\"pacman:%s\" = { version = \"latest\", os = \"linux\" }\n", $0 }' native-explicit.txt
awk '{ printf "\"aur:%s\" = { version = \"latest\", os = \"linux\" }\n", $0 }' foreign-explicit.txt
```

最终配置必须保留 `pacman:base`。再按本机启动、磁盘、内核、网络和显卡环境审阅 `config/protected-packages.txt`。

## Arch Linux：收敛语义

Linux 分支执行：

1. mise 安装声明但缺失的软件；
2. `pacman-converge.sh` 从 `mise bootstrap packages status --json` 读取最终合并声明；
3. 只比较 `pacman -Qqe` 显式包；
4. 未声明且未保护的 explicit 包先用 `pacman -D --asdeps` 降级；
5. 声明/保护包提升为 explicit root；
6. 再用 `pacman -Qdtq` 找 orphan；
7. 预演完整 `pacman -Rs` 事务并再次确认。

它默认 dry-run，不使用 `-Rns`。空声明、缺失 `base`、JSON 结构变化、virtual provider 无法映射、事务触及保护包等情况都会失败关闭。详细恢复记录同样保存在 `.system-sync/history/`。

若拟降级超过安全阈值，脚本会停止。确认 Arch baseline 完整后才可直接运行：

```bash
./scripts/pacman-converge.sh --apply --allow-large-change
```

## Git 管理

建议提交：

- `mise.toml`；
- `platforms/macos/Brewfile`；
- `config/`；
- `scripts/`；
- `README.md`。

`.system-sync/` 和任意目录下的 `.DS_Store` 已忽略。

每次改动包清单都先查看 diff，再 dry-run：

```bash
git diff
mise run dry-run
```

不要把 Git 中的软件清单当作系统或用户数据备份。

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
- [mise Brew manager](https://mise.jdx.dev/bootstrap/packages/brew.html)
- [Homebrew Bundle](https://docs.brew.sh/Brew-Bundle-and-Brewfile)
- [Homebrew Manpage](https://docs.brew.sh/Manpage)
- [ArchWiki：pacman Tips and tricks](https://wiki.archlinux.org/title/Pacman/Tips_and_tricks)
