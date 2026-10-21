// Shared report vocabulary. Keep changes compatible with schema_version 1.
package audit

var Sections = []string{"tools", "packages", "path", "config"}

var ConfigCandidates = []string{".gitconfig", ".config/git/config", ".tmux.conf", ".config/starship.toml", ".config/ghostty/config", ".config/nvim/init.lua", ".config/nvim/init.vim", "Library/Application Support/Code/User/keybindings.json", "Library/Application Support/Code/User/snippets", ".config/Code/User/keybindings.json", ".config/Code/User/snippets"}

var Kinds = map[string]string{
	"ok":        "已核对",
	"drift":     "漂移",
	"candidate": "待纳管候选",
	"local":     "本机保留",
	"error":     "未完成核对",
}

var Markers = map[string]string{
	"ok":        "[+]",
	"drift":     "[!]",
	"candidate": "[?]",
	"local":     "[-]",
	"error":     "[x]",
}

var Colors = map[string]string{
	"drift":     "\x1b[31m",
	"candidate": "\x1b[33m",
	"error":     "\x1b[1;31m",
}

var Commands = map[string]string{
	"node":     "node",
	"python":   "python3",
	"ruby":     "ruby",
	"java":     "java",
	"rust":     "rustc",
	"bun":      "bun",
	"uv":       "uv",
	"npm:yarn": "yarn",
	"go":       "go",
	"deno":     "deno",
	"flutter":  "flutter",
}

var PackageGroups = map[string]string{
	"Brewfile formula 根声明":                      "ok",
	"Brewfile cask 声明":                          "ok",
	"Brewfile tap 声明":                           "ok",
	"尚未安装的 formula":                             "drift",
	"尚未安装的 cask":                                "drift",
	"尚未添加的 tap":                                 "drift",
	"Homebrew 可识别的未声明 requested formula":        "candidate",
	"未声明的已安装 cask":                              "candidate",
	"未声明的 tap":                                  "candidate",
	"受保护但未在 Brewfile 声明的项目":                     "drift",
	"mise 最终生效声明":                               "ok",
	"声明但尚未以同名具体包安装":                             "drift",
	"当前显式安装包中未声明、但受保护而保留":                       "local",
	"apply 时会从 dependency 提升为 explicit 的声明/保护包": "drift",
	"apply 时会从 explicit 降级为 dependency 的未声明包":   "candidate",
	"当前真正 orphan 中可清理的包":                        "candidate",
	"当前 orphan 中因声明或保护规则而跳过的包":                  "local",
	"当前手工根包中未声明、但受保护而保留":                        "local",
	"apply 时会提升为手工根包的声明/保护包":                    "drift",
	"apply 时会降级为依赖的未声明手工根包":                     "candidate",
	"当前 autoremove 事务中的可清理包":                    "candidate",
	"当前 autoremove 事务中因声明或保护规则冲突的包":             "drift",
	"声明的根包":                                     "ok",
	"声明但尚未安装":                                   "drift",
	"未声明但受保护的 user-installed 包":                 "local",
	"拟直接移除的未声明 user-installed 包":                "candidate",
	"zypper 当前认定的 unneeded 包":                   "candidate",
	"zypper dry-run 的完整移除事务":                    "candidate",
}

var SystemPaths = map[string]bool{
	"/Library/Apple/usr/bin":          true,
	"/System/Cryptexes/App/usr/bin":   true,
	"/bin":                            true,
	"/home/linuxbrew/.linuxbrew/bin":  true,
	"/home/linuxbrew/.linuxbrew/sbin": true,
	"/opt/homebrew/bin":               true,
	"/opt/homebrew/sbin":              true,
	"/sbin":                           true,
	"/snap/bin":                       true,
	"/usr/bin":                        true,
	"/usr/local/bin":                  true,
	"/usr/local/sbin":                 true,
	"/usr/sbin":                       true,
}

var PackageRequired = [][]string{
	{"Brewfile cask 声明", "Brewfile formula 根声明", "Brewfile tap 声明", "Homebrew 可识别的未声明 requested formula", "受保护但未在 Brewfile 声明的项目", "尚未安装的 cask", "尚未安装的 formula", "尚未添加的 tap", "未声明的 tap", "未声明的已安装 cask"},
	{"apply 时会从 dependency 提升为 explicit 的声明/保护包", "apply 时会从 explicit 降级为 dependency 的未声明包", "mise 最终生效声明", "声明但尚未以同名具体包安装", "当前显式安装包中未声明、但受保护而保留"},
	{"apply 时会提升为手工根包的声明/保护包", "apply 时会降级为依赖的未声明手工根包", "mise 最终生效声明", "声明但尚未以同名具体包安装", "当前手工根包中未声明、但受保护而保留"},
	{"声明但尚未安装", "声明的根包", "拟直接移除的未声明 user-installed 包", "未声明但受保护的 user-installed 包"},
}
