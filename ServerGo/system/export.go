package system

// 导出包装（阶段5 路由挂载：api 包的通用信息接口需要跨包调用这些函数）
// GetGitRepoInfo 获取本地仓库 git 信息
func GetGitRepoInfo(maxCommits int) (*GitRepoInfo, error) {
	return getGitRepoInfo(maxCommits)
}

// GetSystemInfo 获取系统运行信息
func GetSystemInfo() (*SystemInfo, error) {
	return getSystemInfo()
}

// GetSourceCodeStats 获取源码统计信息
func GetSourceCodeStats() (*SourceCodeStats, error) {
	return getSourceCodeStats()
}

// GetProjectDir 获取项目目录（可执行文件所在目录）
func GetProjectDir() string {
	return getProjectDir()
}
