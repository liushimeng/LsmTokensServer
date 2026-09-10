package middleware

import (
	"testing"
	"time"
)

// 阶段BZ：query classifier 单测。
// 覆盖所有已知接口 URL 模式，确保分类稳定；新增接口时只需更新 patterns，本测试不需改动
// ——除非接口落入某条新分类。

func TestClassifyQuery(t *testing.T) {
	cases := []struct {
		path string
		want QueryClass
	}{
		{"/ChatAnalysisInterface", QNormal},
		{"/ChatAnalysisDetailInterface", QDetail},
		{"/ChatAnalysisTotalRangeInterface", QLong},
		{"/ChatAnalysisTotalRangeInterface?stream=1", QStream},
		{"/ChatAnalysisTotalInterface", QLong},
		{"/ChatAnalysisTotalWS", QLong},
		{"/CleanupReportInterface", QLong},
		{"/AIRouteManageInterface", QLong},
		{"/UserAIRouteInterface", QLong},
		{"/ModelInfoInterface", QLong},
		{"/AgentInfoInterface", QLong},
		{"/ProtocolConvertAnalyzerRecords", QLong},
		{"/ProtocolConvertAnalyzerRecordDetail", QDetail},
		{"/ProtocolConvertAnalyzerTest", QDetail},
		{"/ProtocolConvertAnalyzerUsers", QLong},
		{"/TimeSpanConfigInterface", QShort},
		{"/UserInfoInterface", QShort},
		{"/UserModelListInterface", QShort},
		{"/UserModelOptionsInterface", QShort},
		{"/UserManageInterface", QNormal},
		{"/DstEndPointManageInterface", QNormal},
		{"/CaptchaGenerate", QNormal},
		{"/UserLoginInterface", QNormal},
		{"/UserLogoutInterface", QNormal},
	}
	for _, c := range cases {
		got := ClassifyQuery(c.path)
		if got != c.want {
			t.Errorf("ClassifyQuery(%q) = %s, want %s", c.path, got, c.want)
		}
	}
}

func TestQueryClassDeadline(t *testing.T) {
	if QShort.Deadline() != 10*time.Second {
		t.Errorf("QShort deadline should be 10s, got %v", QShort.Deadline())
	}
	if QNormal.Deadline() != 30*time.Second {
		t.Errorf("QNormal deadline should be 30s, got %v", QNormal.Deadline())
	}
	if QDetail.Deadline() != 60*time.Second {
		t.Errorf("QDetail deadline should be 60s, got %v", QDetail.Deadline())
	}
	if QLong.Deadline() != 300*time.Second {
		t.Errorf("QLong deadline should be 300s, got %v", QLong.Deadline())
	}
	if QStream.Deadline() != 0 {
		t.Errorf("QStream deadline should be 0 (no timeout), got %v", QStream.Deadline())
	}
}

func TestClassifyQueryStripsQueryString(t *testing.T) {
	got := ClassifyQuery("/ChatAnalysisTotalRangeInterface?stream=1&user_name=foo")
	if got != QStream {
		t.Errorf("QStream should match even with query string, got %s", got)
	}
}