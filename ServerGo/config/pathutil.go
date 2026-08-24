package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// 路径解析工具（自旧工程 main.go 提取）

func GetExecutableDir() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %v", err)
	}
	// 获取符号链接的实际路径
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		// 如果 EvalSymlinks 失败，使用原始路径的目录
		return filepath.Dir(execPath), nil
	}
	return filepath.Dir(realPath), nil
}

// resolvePath 解析路径，如果是相对路径则基于可执行文件目录解析
func ResolvePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	execDir, err := GetExecutableDir()
	if err != nil {
		return path, err // 失败时返回原路径
	}
	return filepath.Join(execDir, path), nil
}
