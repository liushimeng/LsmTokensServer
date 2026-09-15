package models

import (
	"testing"
	"time"
)

func TestParseTimeToSeconds(t *testing.T) {
	cases := []struct {
		in    string
		want  int
		isErr bool
	}{
		{"00:00:00", 0, false},
		{"01:00:00", 3600, false},
		{"24:00:00", 86400, false},
		{"23:59:59", 86399, false},
		{"09:30:45", 34245, false},
		{" 09:30:45 ", 34245, false},
		{"9:30:45", 34245, false},
		{"24:00:01", 0, true},
		{"24:01:00", 0, true},
		{"25:00:00", 0, true},
		{"12:60:00", 0, true},
		{"12:00:60", 0, true},
		{"", 0, true},
		{"abc", 0, true},
		{"12:00", 0, true},
	}
	for _, c := range cases {
		got, err := parseTimeToSeconds(c.in)
		if c.isErr {
			if err == nil {
				t.Errorf("parseTimeToSeconds(%q) 期望错误但未返回", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTimeToSeconds(%q) 不期望错误: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseTimeToSeconds(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseWorkPeriods(t *testing.T) {
	// 空字符串 → 默认全天
	p, err := ParseWorkPeriods("")
	if err != nil || len(p) != 1 || p[0].Start != "00:00:00" || p[0].End != "24:00:00" {
		t.Errorf("空字符串应返回默认全天: %v, err=%v", p, err)
	}
	// 合法多段
	p, err = ParseWorkPeriods(`[{"start":"09:00:00","end":"18:00:00"},{"start":"23:00:00","end":"24:00:00"}]`)
	if err != nil || len(p) != 2 {
		t.Errorf("多段解析失败: %v, err=%v", p, err)
	}
	// 非法：start >= end
	_, err = ParseWorkPeriods(`[{"start":"18:00:00","end":"09:00:00"}]`)
	if err == nil {
		t.Error("start >= end 应返回错误")
	}
	// 非法 JSON
	_, err = ParseWorkPeriods(`not json`)
	if err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}

func TestValidateWorkPeriods(t *testing.T) {
	if err := ValidateWorkPeriods([]WorkPeriod{{Start: "09:00:00", End: "18:00:00"}}); err != nil {
		t.Errorf("合法单段不应报错: %v", err)
	}
	if err := ValidateWorkPeriods(nil); err == nil {
		t.Error("空段应报错")
	}
	if err := ValidateWorkPeriods([]WorkPeriod{{Start: "18:00:00", End: "09:00:00"}}); err == nil {
		t.Error("start >= end 应报错")
	}
	if err := ValidateWorkPeriods([]WorkPeriod{{Start: "25:00:00", End: "26:00:00"}}); err == nil {
		t.Error("超范围时间应报错")
	}
}

func TestIsInWorkPeriods(t *testing.T) {
	periods := []WorkPeriod{{Start: "09:00:00", End: "18:00:00"}}
	// 工作时间段内：12:00:00
	in := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if !IsInWorkPeriods(in, periods) {
		t.Error("12:00 应在 09:00-18:00 内")
	}
	// 工作时间段前：08:59:59
	before := time.Date(2026, 9, 15, 8, 59, 59, 0, time.UTC)
	if IsInWorkPeriods(before, periods) {
		t.Error("08:59:59 不应在 09:00-18:00 内")
	}
	// 工作时间段后：18:00:00（左闭右开，不包含）
	after := time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC)
	if IsInWorkPeriods(after, periods) {
		t.Error("18:00:00 不应在 09:00-18:00 内（右开）")
	}
	// 恰好 start：09:00:00
	start := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	if !IsInWorkPeriods(start, periods) {
		t.Error("09:00:00 应在 09:00-18:00 内（左闭）")
	}
	// 全天段：任意时间都在内
	allDay := DefaultWorkPeriods()
	midnight := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	endOfDay := time.Date(2026, 9, 15, 23, 59, 59, 0, time.UTC)
	if !IsInWorkPeriods(midnight, allDay) || !IsInWorkPeriods(endOfDay, allDay) {
		t.Error("全天段应包含任意时刻")
	}
	// 多段：第二段内
	multi := []WorkPeriod{{Start: "00:00:00", End: "01:00:00"}, {Start: "23:00:00", End: "24:00:00"}}
	late := time.Date(2026, 9, 15, 23, 30, 0, 0, time.UTC)
	if !IsInWorkPeriods(late, multi) {
		t.Error("23:30 应在 23:00-24:00 内")
	}
	// 空段：始终不工作
	if IsInWorkPeriods(in, nil) {
		t.Error("空段应返回 false")
	}
}

func TestFormatWorkPeriods(t *testing.T) {
	p := []WorkPeriod{{Start: "09:00:00", End: "18:00:00"}}
	s := FormatWorkPeriods(p)
	parsed, err := ParseWorkPeriods(s)
	if err != nil || len(parsed) != 1 || parsed[0].Start != "09:00:00" {
		t.Errorf("序列化/反序列化不一致: %s, err=%v", s, err)
	}
	// 空段 → 默认
	if FormatWorkPeriods(nil) != DefaultWorkPeriodsJSON {
		t.Error("空段序列化应为默认全天 JSON")
	}
}

func TestShouldBeEnabledByWorkPeriods(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	// 全天
	enable, allDay := ShouldBeEnabledByWorkPeriods(DefaultWorkPeriodsJSON, now)
	if !enable || !allDay {
		t.Error("全天应返回 enable=true, allDay=true")
	}
	// 非全天，当前在工作时间内
	enable, allDay = ShouldBeEnabledByWorkPeriods(`[{"start":"09:00:00","end":"18:00:00"}]`, now)
	if !enable || allDay {
		t.Error("工作时间段内应 enable=true, allDay=false")
	}
	// 非全天，当前在非工作时间
	night := time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC)
	enable, allDay = ShouldBeEnabledByWorkPeriods(`[{"start":"09:00:00","end":"18:00:00"}]`, night)
	if enable || allDay {
		t.Error("非工作时间应 enable=false, allDay=false")
	}
}

func TestIsAllDayWorkPeriods(t *testing.T) {
	if !isAllDayWorkPeriods(DefaultWorkPeriods()) {
		t.Error("默认全天应识别为全天")
	}
	if isAllDayWorkPeriods([]WorkPeriod{{Start: "00:00:00", End: "12:00:00"}}) {
		t.Error("半天不应识别为全天")
	}
	if isAllDayWorkPeriods([]WorkPeriod{{Start: "09:00:00", End: "18:00:00"}, {Start: "20:00:00", End: "22:00:00"}}) {
		t.Error("多段不应识别为全天")
	}
}
