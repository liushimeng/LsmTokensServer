package system

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// getProjectDir 获取项目目录（可执行文件所在目录）
func getProjectDir() string {
	execPath, err := os.Executable()
	if err == nil {
		realPath, err := filepath.EvalSymlinks(execPath)
		if err == nil {
			return filepath.Dir(realPath)
		}
		return filepath.Dir(execPath)
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}

// SourceFileInfo 单个源码文件信息
type SourceFileInfo struct {
	Name      string `json:"name"`
	Lines     int    `json:"lines"`
	Size      int64  `json:"size"`
	SizeHuman string `json:"size_human"`
}

// SourceCodeStats 源码统计信息
type SourceCodeStats struct {
	Files      []SourceFileInfo `json:"files"`
	TotalFiles int              `json:"total_files"`
	TotalLines int              `json:"total_lines"`
	TotalSize  int64            `json:"total_size"`
	SizeHuman  string           `json:"size_human"`
}

// getSourceCodeStats 获取当前目录下所有 .go 文件的统计信息
func getSourceCodeStats() (*SourceCodeStats, error) {
	var files []SourceFileInfo
	var totalLines int
	var totalSize int64

	err := filepath.Walk(getProjectDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}
		if info.IsDir() {
			// 跳过隐藏目录和 vendor
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor" || info.Name() == "go-web-debug-tool" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		lines, err := countFileLines(path)
		if err != nil {
			return nil
		}

		// 转换为相对路径显示
		relPath, _ := filepath.Rel(getProjectDir(), path)
		if relPath == "" {
			relPath = path
		}
		files = append(files, SourceFileInfo{
			Name:      relPath,
			Lines:     lines,
			Size:      info.Size(),
			SizeHuman: formatBytes(uint64(info.Size())),
		})
		totalLines += lines
		totalSize += info.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	// 按文件名排序
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	return &SourceCodeStats{
		Files:      files,
		TotalFiles: len(files),
		TotalLines: totalLines,
		TotalSize:  totalSize,
		SizeHuman:  formatBytes(uint64(totalSize)),
	}, nil
}

// countFileLines 统计文件行数
func countFileLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, nil
}
