# 后续规划

[返回项目首页](../README.md)

以下内容记录核对时的状态和待办，不代表实时状态。日常操作见[项目首页](../README.md)，接管其他应用的步骤见[配置接管](dotfiles.md#后续逐项接管其他配置)。

## 核对结论与后续推进顺序（2026-10-20）

以下是 2026-10-20 当前 Mac 配置核对时的快照和后续建议；待办项尚未实施，后续完成时应更新其状态。

- 16 项 dotfiles 映射全部同步，8 项开发工具均已安装；Espanso、zsh、环境变量与 PATH、ChatGPT 和 VS Code 公共配置已启用。
- Homebrew 声明的 21 个 formula、12 个 cask 和 2 个 tap 均已满足；缺失项、未声明项及保护项声明差异均为 0。
- 仓库已有 86 个测试用例。该次配置核对没有重复执行全量测试，Linux 的真实主机验收也尚未完成。
- 没有发现已声明但漏部署的配置。显式保留的未启用候选主要是 Linux locale，其余待办集中在验证、重建能力和可选功能。

建议按下表顺序推进：

| 顺序 | 工作项 | 当前状态 | 完成标准 |
|---|---|---|---|
| 1 | 对齐检查入口的 Python 要求 | `scripts/sync_runtime.py` 允许 Python 3.9+，但 `tests/test_environment.py` 直接导入 `tomllib`；本机系统 Python 3.9.6 没有该模块，当前 mise Python 3.15.0 不受影响。 | 选择保留 3.9 兼容性或明确提高检查入口要求，同步预检与文档，并在声明的最低版本下运行检查。 |
| 2 | 增加自动化验收与 CI | 仓库尚未提供 CI 工作流，现有测试主要通过本机手动执行。 | 增加 macOS/Linux 自动检查，覆盖最低 Python 版本；保持隔离 HOME 和模拟包管理器，避免测试修改宿主系统软件。 |
| 3 | 补齐新机重建能力 | Java 通过 mise 链接到本机外部 JBR；仓库、全局配置及公共来源旁均没有 `mise.lock`。 | 明确 JDK 的可重装来源或前置安装与注册步骤，为适用工具生成并部署全局锁文件，再在干净环境验证公共配置和工具重建、私有文件缺失时正常跳过。 |
| 4 | 完成 Flutter 版本管理 | Flutter 仍由原目录维护，位于 `stable` 分支，核对时有 1 项本地改动。 | 先核对并保存本地改动，再迁入 mise；确认 Flutter/Dart 命令来源、版本和现有项目工作流正常。 |
| 5 | 接管 VS Code 扩展清单 | 本机安装 19 个扩展，仓库只管理通用设置；没有独立快捷键文件或 snippets。 | 筛选需要跨机器安装的扩展并建立清单，保留机器专属扩展和认证状态；在干净用户配置下验证安装。无需为空的快捷键和 snippets 补建文件。 |
| 6 | 按需增加配置历史与恢复 | mise 实际跟踪文件数为 0，没有检查点，自动捕获未启用，其独立历史同步来源也未配置。 | 先明确公共与本机历史范围，完成手动保存和恢复演练；含私有键的配置留在本机历史中，再决定是否增加后台捕获与跨机器同步。 |

第 1 项中的 `tomllib` 从 Python 3.11 才进入标准库，见 [Python 官方文档](https://docs.python.org/3/library/tomllib.html)。第 3 项需要区分版本选择与安装来源：mise 链接外部 JDK 后仍原地使用它，安装和更新由原来源负责；全局工具也应按全局配置的锁文件规则处理，参见 [已有 JDK 接入](https://mise.jdx.dev/lang/java.html#use-a-jdk-installed-elsewhere)和[全局锁文件](https://mise.jdx.dev/dev-tools/mise-lock.html#global-lockfiles)。

Linux 另有两项后续工作：真实主机验收和统一升级入口。四个发行版 profile 的软件清单仍是结构示例，首次使用前需要导入对应机器的 baseline；locale 需要确认已由发行版生成后再选择。有 Linux 主机使用计划时，应将 baseline 导入和真实安装、取消及恢复验收提前到第 2 项一起处理。统一升级目前在 `scripts/system-sync.sh` 中明确拒绝执行，可在安装与收敛流程验收后再做。

现有 `.system-sync/history/` 系统包操作审计已经启用，与第 6 项的 dotfiles 文件历史是两套功能。仓库也已有 Git 远端；mise 状态中的 `Repository none` 指其独立历史同步来源尚未配置，并不表示本仓库没有远端。配置历史可先采用手动、本机保存，机制与 `track-local` 的边界见 [mise 官方历史文档](https://mise.jdx.dev/dotfiles/history.html)。
