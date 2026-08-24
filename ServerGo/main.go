// LsmTokensServer - AI Tokens 代理与管理服务（前后端分离架构，后端入口）
//
// 由私有项目 LsmHttpAgent 迁移重构而来，前端见 ../ClientWeb（React + Vite）。
// 各业务模块按阶段迁移中，设计文档见 docs/项目迁移解决方案/。
package main

import (
	"fmt"
	"os"
)

// buildTime 由编译脚本通过 -ldflags 注入
var buildTime = "unknown"

func main() {
	fmt.Printf("LsmTokensServer (build %s) - 模块迁移中，尚不可对外服务\n", buildTime)
	fmt.Println("请参照 docs/项目迁移解决方案/ 了解迁移进度。")
	os.Exit(0)
}
