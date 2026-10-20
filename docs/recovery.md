# 取消、冲突与恢复

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。系统包流程见[系统软件包](packages.md)，应用配置接管与取消管理见[配置接管](dotfiles.md)。

## 中断与写入失败的恢复

确认后，操作前后的清单副本和事务阶段保存在 `.system-sync/history/<时间>-package-<随机值>/`：`*.before` 是原文件，`*.after` 是候选结果，`transaction.json` 记录命令与状态。该目录被 Git 忽略。

清单采用临时文件原子替换，也会检测操作期间的人工编辑并拒绝覆盖。`package-install` / `package-uninstall`、整机 `system-sync`、macOS 升级和直接调用的平台写操作共用 `.system-sync/operation.lock`，冲突时立即停止。多个文件无法与系统包事务组成一个跨文件/跨进程的原子事务，突然断电或写盘失败仍可能需要人工恢复。手工包管理命令、直接运行的 mise bootstrap 命令和编辑器不会参与此锁，仍不要与这些入口并行操作。

如果原生安装/卸载已经发生，但配置写入失败，脚本不会擅自反向安装或删除软件。先检查包管理器实际状态、审计副本和 `git diff`，解决权限/格式/并发修改问题后再用相同入口重试；必要时人工合并 `*.after`。在声明和实际状态尚未对齐前，不要直接运行整机 `system-sync`。Git 只能恢复配置文件，不能回滚已经执行的系统包操作。


## 系统包取消与恢复

- dry-run 只能精确显示当前状态下的 orphan。Linux 包的安装原因降级后，可能新出现 orphan，所以 apply 会重新计算并第二次确认。
- 在第二次确认处取消时，pacman/APT/DNF 的安装原因可能已经变更，但包尚未卸载。查看当次 `.system-sync/history/.../demoted.txt` 和 `promoted.txt` 恢复需要保留的根包。
- Homebrew 在 cleanup 最后确认前取消时，前面的缺失软件安装可能已完成，但不会继续清理。
- 误删后，从 Git 恢复声明并重新安装。Git 保存软件清单和受管配置，不是已安装包文件、应用数据或完整系统备份。

macOS 恢复 Brewfile 示例：

```bash
git restore packages/macos/Brewfile
brew bundle install --no-upgrade --file=packages/macos/Brewfile
```


## 配置文件与来源链接

应用配置使用 copy、template 或按键合并时，恢复方式见[应用配置](dotfiles.md)；zsh 与全局环境的原文件记录见[开发环境](development-environment.md)。从映射中删除条目不会自动移除已部署文件，先预览 unapply 的目标再取消管理。

共享 PATH 来源调整为 `dotfiles/common/shell/path.sh` 后，检查 `~/.config/zsh/path.sh` 指向当前仓库版本中的来源。回退仓库版本时先恢复对应来源和部署映射，再预览、更新该目标；目标仍指向旧目录时不要删除它所依赖的来源文件。本次目录整理的链接元数据、来源备份和 Shell 环境对比保存在 `.system-sync/history/*-shell-layout/`，不包含明文私有变量。

## Git 管理

建议提交 `.miserc.toml`、`mise*.toml`、`dotfiles/`、`packages/`、`scripts/`、`tests/`、`docs/` 和 `README.md`。每次修改声明先查看 diff 和 dry-run：

```bash
git diff
mise run dry-run
```

将这个仓库用到另一台机器时，不要直接 apply 原机器的完整 baseline。先根据新机器导出根包，合并差异，并将两台机器确实不同的部分拆成分支或额外 profile，不要靠长期忽略 dry-run 差异维持。
