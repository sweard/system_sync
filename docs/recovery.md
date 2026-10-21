# 取消、冲突与恢复

[返回项目首页](../README.md)

本文中的命令默认在仓库根目录执行。系统包流程见[系统软件包](packages.md)，应用配置接管与取消管理见[配置接管](dotfiles.md)。

## 配置迁出：保留本机文件

以后希望改为直接维护本机配置时，使用 `config-detach` 将当前配置保留在原目标路径，转为独立的普通文件或目录。内容采用执行时的本机版本；工具版本、已安装软件及外部 SDK 继续保留，开发工具仍由 mise 管理。

请在删除仓库或卸载 mise 前执行，并先退出可能同时写入设置的应用：

```bash
# 只列出路径和动作，不输出配置内容、不写配置或建立备份
mise run config-detach --dry-run

# 审阅计划后执行，输入 DETACH 确认
mise run config-detach

# 已审阅计划的自动化场景可显式跳过确认
mise run config-detach --yes
```

预览会调用原生状态查询，可能建立 mise 自身的查询锁或缓存；不会应用配置映射。

任务使用 mise 原生状态中的来源，选择当前平台属于本仓库的已启用映射，包括 `mise.local.toml` 中的私有映射：

- 将 `symlink` 和 `symlink-each` 目标中指向本仓库的链接转换为文件或目录，保留实际内容与权限；其他本机文件和外部链接继续保留。
- 已复制或渲染的文件，以及 ChatGPT / VS Code 的合并目标，保留当前内容和私有键，不重新应用来源。
- 将全局 mise 的共享 PATH 脚本引用改为已部署的本机路径，并复制下载锁引用的 npm / Python 依赖目录，保留锁文件与校验值。完成后可以移走仓库，Shell 和工具配置仍有本机来源。
- 取消 `conf.d/system-sync-history.toml` 中本项目的手动历史登记，保留既有本机检查点、私有变量文件和 `.zshrc.local`。自定义登记需先人工核对。

断开的仓库链接、依赖目录冲突、不支持的配置模式、自定义仓库脚本引用、HOME 外目标或目标父目录链接会在替换前阻止操作。已存在的本机配置不会被来源覆盖。迁出复用仓库操作锁；运行期间不要同时执行配置同步、编辑来源或使用其他直接写配置的命令。

待改动目标的原状态、迁出副本和 `transaction.json` 保存到 `~/.local/state/system-sync/config-detach/<时间>-<随机值>/`，设置了 `XDG_STATE_HOME` 时使用其下的 `system-sync/config-detach/`。备份要求位于 HOME 内且在仓库外，目录权限为 `0700`，备份普通文件为 `0600`；原权限记录在事务的 `before_metadata` / `after_metadata` 中，配置正文只保存在本机。

任务先备份并暂存全部内容，再逐项替换。失败时尝试恢复已经替换的目标；若目标被其他程序同时修改，则保留现场并将事务标为 `recovery_required`，按记录中的目标、备份和权限逐项恢复。断电或强制终止后同样先检查事务；这不是跨文件的系统级原子事务。成功后再次执行无需迁出时不会重复建立备份。

重新打开终端加载本机配置。公共来源仍保留在 Git 中；以后再次运行 `config-sync` 会按声明重新接管，普通文件与链接冲突时按[配置接管流程](dotfiles.md#后续逐项接管其他配置)逐项处理。若还要完全卸载 mise，应另行迁移工具安装、版本选择和 Shell 激活。

`mise bootstrap dotfiles unapply` 用于删除可识别的已部署目标，并保留合并键，不能用于这次保留配置的迁出操作，见[官方 unapply 说明](https://mise.jdx.dev/cli/bootstrap/dotfiles/unapply.html)。

## 手动配置检查点（仅本机）

公共来源继续使用仓库 Git 历史。部署后含私有键的 ChatGPT / VS Code 目标文件，以及存在的 `~/.zshrc.local`，使用 mise `track-local` 历史；`secrets.env`、认证文件和整个应用数据目录不纳入范围。

```bash
mise run config-history-init
mise run config-save -d '修改前的说明'
mise run config-history
```

初始化把登记写入 `~/.config/mise/conf.d/system-sync-history.toml`（非默认 XDG / mise 目录遵循对应环境变量），权限 `0600`。登记不部署到其他机器，`autosave = false`，没有启用 watcher 或历史远端。已有人工修改的登记不会被覆盖。新增文件或调整范围后先审阅登记；本机私有内容不进入本项目 Git。

`config-save` 显式列出已登记的现存文件，因此能保存关闭 autosave 的文件；原生不带路径的 `mise dot save` 会跳过这些条目。检查点存于 `$MISE_STATE_DIR/history-local`，默认 `~/.local/state/mise/history-local`，目录权限 `0700`。本机历史是本机磁盘上的明文 Git 存储，丢失磁盘时不能代替离线备份。原理与隔离边界见 [mise 官方本机历史](https://mise.jdx.dev/dotfiles/history.html#local-only-history)。

恢复前选择具体检查点和目标文件，先预览，再执行：

```bash
mise dot --local rollback ~/.codex/config.toml --to <检查点ID> --dry-run
mise dot --local rollback ~/.codex/config.toml --to <检查点ID>
```

原生命令会询问确认，并在回滚前保存当前状态；不要对整个 HOME 使用 `--all`。之后运行 `mise run config-dry-run`，公共键如需恢复到当前声明，再运行 `config-sync`，私有键继续保留。原生 rollback 与 undo 的用法见[官方文档](https://mise.jdx.dev/cli/dotfiles/rollback.html)。

本轮已在临时 HOME 使用合成私有键完成保存、修改、恢复预览、实际回滚和再次合并的演练；共享历史中没有检查点。未对真实私有配置执行回滚。

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
