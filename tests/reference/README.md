# Python 行为参考

[返回项目首页](../../README.md)

此目录保留 Go 迁移前的 Python 实现，供行为对照、回归测试及共享锁兼容性测试使用。日常管理入口使用 `mise run` 调用 Go helper 或 mise 原生命令。

| 文件 | 保留用途 |
|---|---|
| `audit.py` | 审计单元测试，以及 Go/Python 完整报告对照 |
| `extract-mise-packages.py`、`parse-zypper-xml.py` | JSON/XML 安全解析的参考测试 |
| `package-command.py` | 单包编辑、保护规则、原子写入和失败场景的参考测试 |
| `config-detach.py` | 配置快照、链接物化和事务回滚的参考测试 |
| `vscode-extensions.py` | 扩展声明校验与进程超时清理的参考测试 |
| `sync_runtime.py` | `check` 的 Python 3.11+ 前置检查，以及参考实现和 Go 的共享锁兼容性测试 |

需要仓库路径的参考实现通过 `parents[2]` 定位根目录，隔离测试中的副本也保持 `tests/reference/` 层级。相互引用的模块从本目录导入。原有未被执行或测试引用的 `config-history.py` 已移除；本机历史测试继续直接验收 Go 入口。

完整验证仍使用 `mise run check`。后续迁移相应测试后，再移除对应参考实现；这些文件不作为日常管理或恢复入口维护。
