# Arch Linux + mise 声明式系统包同步

这套配置把 `mise.toml` 中的 `pacman:` / `aur:` 条目当作系统的“显式根包”：

1. mise 安装声明但缺失的包；
2. 脚本读取当前目录中 **最终生效、已合并** 的 mise 包状态；
3. 只把 `pacman -Qqe` 的显式安装包与声明集比较；
4. 未声明且未受保护的显式包先经 `pacman -D --asdeps` 降级，不直接卸载；
5. 声明包和保护包会被标记为 explicit root；
6. 再用 `pacman -Qdtq` 找真正 orphan，经第二次确认后用 `pacman -Rs` 清理。

脚本默认是 dry-run，并且明确不使用 `-Rns`。本项目补上的是“从最终生效配置删掉条目后，让 pacman 的显式根包集合随之收敛”这一层语义。

## 文件结构

```text
system_sync/
├── .gitignore
├── README.md
├── mise.toml
├── config/
│   └── protected-packages.txt
└── scripts/
    ├── extract-mise-packages.py
    └── pacman-converge.sh
```

运行时会把每次 apply 前后的清单保存到 `.system-sync/history/<UTC 时间>/`；该目录已被 Git 忽略。

## 前置条件

- Arch Linux 或兼容且由 pacman 管理的系统；
- `mise`，并支持 `mise bootstrap packages status --json`；
- Bash、Python 3、coreutils、pacman；
- 非 root 用户需要 `sudo`；
- 使用 `aur:` 条目时，`PATH` 中必须已有 `yay` 或 `paru`；helper 缺失时本脚本会失败关闭。

先检查：

```bash
mise --version
python3 --version
pacman --version
command -v yay || command -v paru
```

进入目录后信任配置：

```bash
cd system_sync
mise trust
```

## 首次迁移现有 Arch：建立 baseline

不要直接拿示例包清单执行 apply。先导出现有系统的显式根包：

```bash
pacman -Qqen | LC_ALL=C sort -u > native-explicit.txt
pacman -Qqem | LC_ALL=C sort -u > foreign-explicit.txt
```

- `native-explicit.txt`：来自当前同步仓库的显式安装包，对应 `pacman:`；
- `foreign-explicit.txt`：显式安装的 foreign 包，通常包含 AUR 包，对应候选 `aur:`。

`pacman -Qqem` 的结果不保证都来自 AUR：本地自建包、手工 `pacman -U` 安装的包也会在里面。逐项确认；不能由 yay/paru 重建的包应先放进 `config/protected-packages.txt`，或另建可靠的安装来源，不能盲目写成 `aur:`。

可生成便于粘贴的 TOML 片段：

```bash
awk '{ printf "\"pacman:%s\" = \"latest\"\n", $0 }' native-explicit.txt
awk '{ printf "\"aur:%s\" = \"latest\"\n", $0 }' foreign-explicit.txt
```

把确认后的输出合并到 `mise.toml` 的同一个 `[bootstrap.packages]` 表中，并删除不需要的示例项。必须保留：

```toml
"pacman:base" = "latest"
```

然后逐项审阅 `config/protected-packages.txt`，至少覆盖本机实际使用的：

- 内核、固件、CPU microcode 与 initramfs；
- bootloader；
- 根文件系统、加密、LVM/RAID 工具；
- 网络、Wi-Fi、VPN 和远程救援入口；
- 专有显卡驱动或其他机器启动必需包；
- 暂时无法转成 mise 声明的本地包。

保护清单不是正常期望状态的替代品：长期需要的软件仍应写进 `mise.toml`。保护清单只是一道避免把机器变得不可启动/不可联网的保险丝。

## 首次核对顺序

第一步，只读检查 mise 与 pacman 视图：

```bash
mise run status
```

第二步，预览 mise 会安装什么，以及脚本会调整什么：

```bash
mise run dry-run
```

重点检查这三组输出：

- “未声明、但受保护而保留”：确认这些确实应保护，或把长期需要项移入 `mise.toml`；
- “会从 explicit 降级为 dependency”：baseline 漏项会在这里暴露；
- “真正 orphan 中可清理”：确认没有可选功能、驱动或临时救援工具。

只有输出完全符合预期，才执行：

```bash
mise run system-sync
```

实际流程会先经过 mise task 确认，再要求输入 `APPLY <数量>` 才调整安装原因。调整后脚本重新查询 orphan，用 pacman 的只打印模式展开 `-Rs` 完整递归事务，确认其中没有声明/保护包；随后要求输入 `REMOVE <完整事务包数>`，最后 pacman 还会展示事务并询问一次。脚本会先安装缺失声明包，再进入上述收敛流程。

如果 baseline 明显不完整，脚本会在一次拟降级超过 25 个显式包，或至少 5 个且占显式包 50% 以上时停止。确实需要大范围收敛时，先再次核对清单，再直接运行：

```bash
./scripts/pacman-converge.sh --apply --allow-large-change
```

这不会绕过交互确认。

## 日常使用

新增软件：先把条目加入 `[bootstrap.packages]`，再同步。例如：

```toml
"pacman:jq" = "latest"
"aur:visual-studio-code-bin" = "latest"
```

```bash
mise run dry-run
mise run system-sync
```

删除软件：从 `mise.toml` 删除对应条目，先 dry-run，再 system-sync。它不会立刻对该包执行删除；脚本先把它从 explicit 降级成 dependency，只有 pacman 随后认定它是真正 orphan 才会清理。

只运行脚本而不让 mise 安装缺失包时：

```bash
./scripts/pacman-converge.sh              # 默认 dry-run
./scripts/pacman-converge.sh --status
./scripts/pacman-converge.sh --apply
```

通常更推荐 mise task，因为 `system-sync` 会先确保声明包已安装。

## 最终配置读取与兼容策略

脚本没有 grep TOML，也没有只读本地 `mise.toml`。它在项目目录运行：

```bash
LC_ALL=C mise bootstrap packages status --json
```

这是 mise 已经合并全局、父目录、当前目录及当前环境后的有效包集合。`scripts/extract-mise-packages.py` 支持当前官方结构，以及少量可严格验证的兼容结构。

以下任何情况都会失败关闭，不会把空集合当成“全部删除”：

- JSON 为空、损坏或结构无法识别；
- pacman/AUR manager 被 mise 报告为 unavailable；
- 没有解析到任何 pacman/AUR 包；
- 最终集合没有 `pacman:base`；
- apply 前仍有声明包没有以同名具体包安装；
- 包名含不可安全传给 pacman 的字符；
- 已安装的 virtual package 请求无法映射到同名具体 provider。
- pacman 对 `-Rs` 的只读预演无法完成，或完整递归事务触及声明/保护包。

因此，如果 mise 以后改变 JSON schema，预期结果是脚本报错并停下，而不是误删。升级 mise 后先运行 `mise run status`。

## Git 版本管理与多机同步

适合提交到 Git 的文件：

- `mise.toml`；
- `config/protected-packages.txt`；
- `scripts/`；
- `README.md`。

示例：

```bash
git init
git add mise.toml config scripts README.md .gitignore
git commit -m "add Arch package baseline"
```

在另一台机器 clone 后，不要马上 apply。不同机器的内核、bootloader、显卡、网络和本地/AUR 包可能不同。先为该机器补齐声明或保护清单，依次运行 `mise trust`、`mise run status`、`mise run dry-run`，再决定是否同步。

若多台机器差异较大，建议按机器维护独立分支/环境文件，或把这里只作为公共 baseline；不要依赖保护清单无限累积机器差异。

## 风险、审计与恢复

`pacman -D --asdeps` 只改变安装原因，不会删除文件。若在第二次确认前取消，可从最近日志恢复原来的显式标记：

```bash
latest_log="$(find .system-sync/history -mindepth 1 -maxdepth 1 -type d | LC_ALL=C sort | tail -n 1)"
xargs -r sudo pacman -D --asexplicit < "$latest_log/demoted.txt"
```

也可以只恢复单个包：

```bash
sudo pacman -D --asexplicit package_name
```

一旦 `pacman -Rs` 完成，恢复方式是重新安装。仓库包可用：

```bash
sudo pacman -S package_name
```

AUR 包用 yay/paru 重装。还可查看：

```bash
grep '\[ALPM\] removed' /var/log/pacman.log
ls /var/cache/pacman/pkg/
```

若缓存里仍有对应包，可用 `sudo pacman -U /var/cache/pacman/pkg/<包文件>` 恢复。配置文件、数据库和重要数据仍应使用独立备份；Git 中的软件清单不是系统备份。

`pacman -Rs` 会递归处理变成不再需要的依赖；ArchWiki 也提醒，递归清理可能涉及仍作为其他软件可选依赖的包。因此脚本会先把声明包和保护包提升为 explicit，使用 `pacman -Rsp --print-format '%n'` 取得完整事务并做冲突检查，再让 pacman 在最终事务前展示一次。需要保留的可选功能包应写入声明或保护清单，不要用自动输入绕过最后核对。

## 参考

- [mise Bootstrap Packages](https://mise.jdx.dev/bootstrap/packages/)
- [mise AUR manager](https://mise.jdx.dev/bootstrap/packages/aur.html)
- [mise tasks](https://mise.jdx.dev/tasks/)
- [ArchWiki：pacman Tips and tricks](https://wiki.archlinux.org/title/Pacman/Tips_and_tricks)
- [ArchWiki：迁移到新硬件](https://wiki.archlinux.org/title/Migrate_installation_to_new_hardware)
