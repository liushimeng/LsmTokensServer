package system

import (
	"os"
	"path/filepath"
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