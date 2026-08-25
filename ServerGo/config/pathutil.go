package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// 路径解析工具（自旧工程 main.go 提取）
//
// 注意：新工程前后端分离，可执行文件位于 ServerGo/ 子目录，而配置文件、证书、
// README、日志等运行时产物统一位于工程根目录（与配置文件同目录）。因此相对路径
// 一律以「配置文件所在目录」为基准解析，仅在配置尚未加载时才回退到可执行文件目录。

// configDir 配置文件所在目录（工程根目录），由 LoadConfig 在加载成功后写入。
var configDir string

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

// GetConfigDir 返回配置文件所在目录（工程根目录）；配置未加载时回退到可执行文件目录。
func GetConfigDir() string {
	if configDir != "" {
		return configDir
	}
	if dir, err := GetExecutableDir(); err == nil {
		return dir
	}
	return "."
}

// SetConfigDirForTest 单测专用：临时覆盖配置目录基准，返回旧值便于 defer 恢复。
func SetConfigDirForTest(dir string) string {
	old := configDir
	configDir = dir
	return old
}

// ResolvePath 解析路径：绝对路径原样返回，相对路径基于配置文件所在目录解析。
func ResolvePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	baseDir := GetConfigDir()
	return filepath.Join(baseDir, path), nil
}
