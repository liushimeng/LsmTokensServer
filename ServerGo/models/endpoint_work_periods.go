package models

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WorkPeriod 表示一个工作时间段（精确到秒）。
// Start/End 使用 "HH:MM:SS" 格式，其中 End 允许 "24:00:00" 表示当天结束（86400 秒）。
// 每段要求 Start < End，且在同一天内（不跨午夜）。
type WorkPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// 全天 24 小时工作时间段的 JSON 表示
const DefaultWorkPeriodsJSON = `[{"start":"00:00:00","end":"24:00:00"}]`

// isAllDayWorkPeriods 判断是否为全天工作（即无需后台任务干预）
// 判定条件：仅一段且 start=00:00:00、end=24:00:00（允许空白差异）
func isAllDayWorkPeriods(periods []WorkPeriod) bool {
	if len(periods) != 1 {
		return false
	}
	return equalTimeString(periods[0].Start, "00:00:00") && equalTimeString(periods[0].End, "24:00:00")
}

// equalTimeString 比较两个时间字符串是否等价（忽略前导零和空白）
func equalTimeString(a, b string) bool {
	na, errA := parseTimeToSeconds(a)
	nb, errB := parseTimeToSeconds(b)
	if errA != nil || errB != nil {
		return false
	}
	return na == nb
}

// parseTimeToSeconds 将 "HH:MM:SS" 或 "H:M:S" 解析为从 00:00:00 开始的秒数。
// 支持 24:00:00（返回 86400）。返回错误当格式非法或超出 [0, 86400] 范围。
func parseTimeToSeconds(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("时间字符串为空")
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("时间格式非法，期望 HH:MM:SS: %q", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 24 {
		return 0, fmt.Errorf("小时非法: %q", parts[0])
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("分钟非法: %q", parts[1])
	}
	sec, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || sec < 0 || sec > 59 {
		return 0, fmt.Errorf("秒非法: %q", parts[2])
	}
	// 24:00:00 是合法的结束时间
	if h == 24 && (m != 0 || sec != 0) {
		return 0, fmt.Errorf("24:xx:xx 仅允许 24:00:00: %q", s)
	}
	if h > 24 {
		return 0, fmt.Errorf("小时超出范围: %q", s)
	}
	total := h*3600 + m*60 + sec
	if total > 86400 {
		return 0, fmt.Errorf("时间超出 24:00:00: %q", s)
	}
	return total, nil
}

// ParseWorkPeriods 解析工作时间段 JSON 字符串。
// 空字符串返回默认全天段；非法 JSON 或校验失败返回错误。
func ParseWorkPeriods(jsonStr string) ([]WorkPeriod, error) {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return DefaultWorkPeriods(), nil
	}
	var periods []WorkPeriod
	if err := json.Unmarshal([]byte(jsonStr), &periods); err != nil {
		return nil, fmt.Errorf("工作时间段 JSON 解析失败: %w", err)
	}
	if err := ValidateWorkPeriods(periods); err != nil {
		return nil, err
	}
	return periods, nil
}

// ValidateWorkPeriods 校验工作时间段列表的合法性：
//   - 至少一段
//   - 每段 start/end 格式合法
//   - 每段 start < end
//   - 段之间不重叠（可选，宽松模式允许）
func ValidateWorkPeriods(periods []WorkPeriod) error {
	if len(periods) == 0 {
		return fmt.Errorf("工作时间段不能为空")
	}
	for i, p := range periods {
		startSec, err := parseTimeToSeconds(p.Start)
		if err != nil {
			return fmt.Errorf("第 %d 段开始时间非法: %w", i+1, err)
		}
		endSec, err := parseTimeToSeconds(p.End)
		if err != nil {
			return fmt.Errorf("第 %d 段结束时间非法: %w", i+1, err)
		}
		if startSec >= endSec {
			return fmt.Errorf("第 %d 段开始时间必须小于结束时间: %s >= %s", i+1, p.Start, p.End)
		}
	}
	return nil
}

// FormatWorkPeriods 将工作时间段序列化为 JSON 字符串。
func FormatWorkPeriods(periods []WorkPeriod) string {
	if len(periods) == 0 {
		return DefaultWorkPeriodsJSON
	}
	b, err := json.Marshal(periods)
	if err != nil {
		return DefaultWorkPeriodsJSON
	}
	return string(b)
}

// DefaultWorkPeriods 返回默认全天工作时间段。
func DefaultWorkPeriods() []WorkPeriod {
	return []WorkPeriod{{Start: "00:00:00", End: "24:00:00"}}
}

// IsInWorkPeriods 判断给定时间是否落在任一工作时间段内（左闭右开 [start, end)）。
// 当 end = 24:00:00（86400）时，表示到当天最后一秒（含 23:59:59）仍算在工作时间内。
// periods 为空时返回 false（无时间段 = 始终不工作）。
func IsInWorkPeriods(now time.Time, periods []WorkPeriod) bool {
	if len(periods) == 0 {
		return false
	}
	// 计算当天已过的秒数
	seconds := now.Hour()*3600 + now.Minute()*60 + now.Second()
	for _, p := range periods {
		startSec, errStart := parseTimeToSeconds(p.Start)
		endSec, errEnd := parseTimeToSeconds(p.End)
		if errStart != nil || errEnd != nil {
			continue
		}
		// 全天段 86400 覆盖到 23:59:59
		if seconds >= startSec && (seconds < endSec || (endSec == 86400 && seconds <= 86400)) {
			return true
		}
	}
	return false
}

// ShouldBeEnabledByWorkPeriods 判断工作时间段是否要求当前源站可用。
// 全天工作返回 true（不干预），非全天则按当前时间计算。
// 返回 (shouldEnable, isAllDay)。
func ShouldBeEnabledByWorkPeriods(jsonStr string, now time.Time) (bool, bool) {
	periods, err := ParseWorkPeriods(jsonStr)
	if err != nil || len(periods) == 0 {
		return true, true // 解析失败默认全天可用
	}
	if isAllDayWorkPeriods(periods) {
		return true, true
	}
	return IsInWorkPeriods(now, periods), false
}
