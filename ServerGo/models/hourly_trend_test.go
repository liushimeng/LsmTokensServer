package models

import (
	"strings"
	"testing"
	"time"

	"github.com/lishimeng/LsmTokensServer/database"
)

// 静态守护：normalizeHourlyTrendHours 的归一化语义（<=0 → 24；>720 → 720）。
func TestNormalizeHourlyTrendHours(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 24},
		{-1, 24},
		{1, 1},
		{24, 24},
		{168, 168},
		{169, 169},
		{720, 720},
		{721, 720},
		{99999, 720},
	}
	for _, c := range cases {
		got := normalizeHourlyTrendHours(c.in)
		if got != c.want {
			t.Errorf("normalizeHourlyTrendHours(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// buildHourlyTrendPoints 应当产生连续零值桶序列，长度符合 hours 语义。
func TestBuildHourlyTrendPoints_Hour(t *testing.T) {
	buckets := map[string]*hourlyTrendBucket{
		nowTruncated().Format("2006-01-02 15:04"): {count: 5, inTokens: 100, outTokens: 50, allTokens: 150},
	}
	points := buildHourlyTrendPoints(buckets, 3, "hour")
	if len(points) != 3 {
		t.Fatalf("len(points) = %d, want 3", len(points))
	}
	// 末桶应为真实数据
	last := points[len(points)-1]
	if last.Count != 5 || last.TokensTotal != 150 {
		t.Errorf("last point should carry real values: %+v", last)
	}
	// 前桶应为零值
	for i := 0; i < len(points)-1; i++ {
		if points[i].Count != 0 || points[i].TokensTotal != 0 {
			t.Errorf("point[%d] should be zero: %+v", i, points[i])
		}
	}
}

// 跨度 >168h 时按天桶：24h 的数据落在「今天」桶里，其它天为零。
func TestBuildHourlyTrendPoints_Day(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	buckets := map[string]*hourlyTrendBucket{
		today: {count: 7, allTokens: 999},
	}
	points := buildHourlyTrendPoints(buckets, 48, "day") // 2 天跨度
	if len(points) != 2 {
		t.Fatalf("len(points) = %d, want 2", len(points))
	}
	if points[0].Date != yesterday {
		t.Errorf("points[0].Date = %s, want %s", points[0].Date, yesterday)
	}
	if points[0].Count != 0 {
		t.Errorf("points[0] should be zero")
	}
	if points[1].Date != today || points[1].Count != 7 {
		t.Errorf("points[1] = %+v", points[1])
	}
}

// 30 天跨度（720h）：应产生 30 个天桶。
func TestBuildHourlyTrendPoints_30Days(t *testing.T) {
	points := buildHourlyTrendPoints(map[string]*hourlyTrendBucket{}, 720, "day")
	if len(points) != 30 {
		t.Errorf("len(points) = %d, want 30", len(points))
	}
	for _, p := range points {
		if !strings.Contains(p.Date, "-") || strings.Contains(p.Date, " ") {
			t.Errorf("day bucket should not contain time: %s", p.Date)
		}
	}
}

// 7 天边界：hours=168 仍按小时桶（threshold=168），hours=169 切天桶。
// 这两个断言在 DB == nil 时也成立（粒度判定早于 DB 访问）。
func TestHourlyTrendGranularityThreshold(t *testing.T) {
	res168, _ := GetHourlyTrendAll(8, 168)
	if res168.Granularity != "hour" {
		t.Errorf("hours=168 should be 'hour', got %s", res168.Granularity)
	}
	res169, _ := GetHourlyTrendAll(8, 169)
	if res169.Granularity != "day" {
		t.Errorf("hours=169 should be 'day', got %s", res169.Granularity)
	}
}

// DB 未初始化时 GetHourlyTrendAll 不应崩溃（返回连续零值桶 + 正确粒度）。
func TestGetHourlyTrendAll_NilDBSafe(t *testing.T) {
	if database.DB == nil {
		res, err := GetHourlyTrendAll(8, 24)
		if err != nil {
			t.Fatalf("expected nil error when DB nil, got %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if res.Granularity != "hour" || len(res.Points) != 24 {
			t.Errorf("got %+v, want hour/24", res)
		}
	}
	// 当 DB 已初始化（连接到测试库）时跳过此 case（无需断言具体数据）。
}

// 用户端空 userName 应当被拒绝。
func TestGetHourlyTrendByUser_RequiresUser(t *testing.T) {
	_, err := GetHourlyTrendByUser("", []string{"m"}, 8, 24)
	if err == nil {
		t.Error("expected error for empty userName")
	}
}

// nowTruncated 测试辅助：截断到整点。
func nowTruncated() time.Time {
	return time.Now().Truncate(time.Hour)
}