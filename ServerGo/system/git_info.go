package system

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitFileChange 单个文件的变更信息
type GitFileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // A=添加, M=修改, D=删除, R=重命名
}

// GitCommitInfo 单条提交记录信息
type GitCommitInfo struct {
	Hash    string          `json:"hash"`
	Author  string          `json:"author"`
	Date    string          `json:"date"`
	Message string          `json:"message"`
	Files   []string        `json:"files"`   // 兼容旧格式
	Changes []GitFileChange `json:"changes"` // 带状态的文件变更列表
}

// GitRepoInfo 仓库信息
type GitRepoInfo struct {
	Branch  string          `json:"branch"`
	Remote  string          `json:"remote"`
	Commits []GitCommitInfo `json:"commits"`
	Count   int             `json:"count"`
}

// getGitRepoInfo 获取本地仓库git信息（使用系统git命令，更轻量可靠）
// maxCommits <= 0 表示获取全部提交
func getGitRepoInfo(maxCommits int) (*GitRepoInfo, error) {
	projectDir := getProjectDir()
	// 获取当前分支
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = projectDir
	branchBytes, err := branchCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("get branch failed: %w", err)
	}
	branch := strings.TrimSpace(string(branchBytes))

	// 获取远程地址
	remoteCmd := exec.Command("git", "remote", "get-url", "origin")
	remoteCmd.Dir = projectDir
	remoteBytes, err := remoteCmd.Output()
	remote := ""
	if err == nil {
		remote = strings.TrimSpace(string(remoteBytes))
	}

	// 获取提交记录: hash|author|date|message
	format := "%H|%an|%ci|%s"
	var logCmd *exec.Cmd
	if maxCommits > 0 {
		logCmd = exec.Command("git", "log", fmt.Sprintf("-%d", maxCommits), fmt.Sprintf("--pretty=format:%s", format))
	} else {
		logCmd = exec.Command("git", "log", fmt.Sprintf("--pretty=format:%s", format))
	}
	logCmd.Dir = projectDir
	logBytes, err := logCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("get log failed: %w", err)
	}

	commits := parseGitLog(string(logBytes))

	// 为每个提交获取修改的文件列表（带状态）
	for i := range commits {
		changes, err := getCommitFileChanges(commits[i].Hash)
		if err == nil {
			commits[i].Changes = changes
			// 同时填充兼容旧格式的 Files 列表
			files := make([]string, 0, len(changes))
			for _, ch := range changes {
				files = append(files, ch.Path)
			}
			commits[i].Files = files
		}
	}

	return &GitRepoInfo{
		Branch:  branch,
		Remote:  remote,
		Commits: commits,
		Count:   len(commits),
	}, nil
}

// getCommitFiles 获取某次提交的文件变更列表（带状态）
func getCommitFiles(hash string) ([]string, error) {
	return getCommitFilesWithDir(hash, getProjectDir())
}

// getCommitFilesWithDir 获取某次提交的文件变更列表（带状态）
func getCommitFilesWithDir(hash string, projectDir string) ([]string, error) {
	cmd := exec.Command("git", "show", "--name-only", "--format=", hash)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// getCommitFileChanges 获取某次提交的文件变更列表（带 A/M/D 状态）
func getCommitFileChanges(hash string) ([]GitFileChange, error) {
	return getCommitFileChangesWithDir(hash, getProjectDir())
}

// getCommitFileChangesWithDir 获取某次提交的文件变更列表（带 A/M/D 状态）
func getCommitFileChangesWithDir(hash string, projectDir string) ([]GitFileChange, error) {
	cmd := exec.Command("git", "show", "--name-status", "--format=", hash)
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var changes []GitFileChange
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) >= 2 {
			changes = append(changes, GitFileChange{
				Status: strings.TrimSpace(parts[0]),
				Path:   strings.TrimSpace(parts[1]),
			})
		}
	}
	return changes, nil
}

// parseGitLog 解析git log输出
func parseGitLog(output string) []GitCommitInfo {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	commits := make([]GitCommitInfo, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}

		// 解析日期并格式化为本地时间
		dateStr := parts[2]
		if t, err := time.Parse("2006-01-02 15:04:05 -0700", dateStr); err == nil {
			dateStr = t.Local().Format("2006-01-02 15:04:05")
		}

		commits = append(commits, GitCommitInfo{
			Hash:    parts[0][:8],
			Author:  parts[1],
			Date:    dateStr,
			Message: parts[3],
		})
	}

	return commits
}
