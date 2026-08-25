package system

// 导出包装（阶段5 路由挂载：api 包的通用信息接口需要跨包调用这些函数）
// GetGitRepoInfo 获取本地仓库 git 信息
func GetGitRepoInfo(maxCommits int) (*GitRepoInfo, error) {
	return getGitRepoInfo(maxCommits)
}

// GetGitRepoInfoLight 轻量获取仓库信息（提交列表 + 总数，不查文件变更，消除 N+1 git show）
func GetGitRepoInfoLight(maxCommits int) (*GitRepoInfo, error) {
	return getGitRepoInfoLight(maxCommits)
}

// GetGitCommitChanges 获取单次提交的文件变更列表（带 A/M/D/R 状态，前端展开时惰性调用）
func GetGitCommitChanges(hash string) ([]GitFileChange, error) {
	return getCommitFileChanges(hash)
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
