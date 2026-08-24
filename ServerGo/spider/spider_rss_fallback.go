package spider

// ==================== v2.0.20：RSS / Atom Feed 自动 fallback（通用反爬兜底） ====================
//
// 背景：问题分析报告_20260630_150225 §五指出机器之心（jiqizhixin.com）已改付费墙，
// 任何浏览器爬虫 / 移动端 UA / captcha 兜底均无法绕过 WAF + login_wall。
// 但同一站点几乎都会保留公开 RSS / Atom feed 作为机器可读入口。
//
// 设计目标：
//   - 在反爬 / login_wall / paywall 命中且所有浏览器兜底（移动端 UA、desktop retry
//     循环）均失败时，自动尝试从已知 RSS/Atom feed 抓取文章列表，
//     把每条 item 转成「等价的 SpiderWebDataResponse」，让 Agent 拿到结构化内容
//     而不是空响应。
//   - 不依赖 Chrome，纯标准库 http.Get / xml.Unmarshal；任何站点只要配置了
//     RSSFeedCandidates（host → feed 路径列表）即可启用。
//   - Agent 可通过 request 字段 `fallback_strategy=rss_first|auto|none` 显式控制
//     默认行为：auto 时仅在浏览器尝试耗尽后启用；rss_first 时直接先 fetch RSS
//     （适合「已知该站付费墙、Agent 不需要再浏览器 retry」场景）。
//   - 整体对其他接口的影响：仅在 /SpiderWebData handler 末尾「所有浏览器尝试耗尽
//     + login_wall/anti_bot 失败」分支触发；新文件不修改现有 retry 逻辑。
//
// 已知站点 RSS 配置：
//   - 基于问题分析报告_20260630_150225 §5.1 + LoginWallAlternativeHints 已
//     枚举的 host（jiqizhixin.com / 36kr.com / huxiu.com / qbitai.com 等）。
//   - 通用兜底：探测 /rss / /feed / /atom.xml 三个常见路径。

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	system "github.com/lishimeng/LsmTokensServer/system"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ==================== 类型 ====================

// RSSItem 单条 RSS / Atom item 解析结果（与 SpiderWebDataResponse.Elements
// 类型兼容；本文件不直接耦合 SpiderWebDataResponse 结构，由调用方按需转换）。
type RSSItem struct {
	Title       string    `json:"title"`                  // 文章标题
	URL         string    `json:"url"`                    // 文章链接（绝对化）
	Summary     string    `json:"summary,omitempty"`      // 摘要（description / summary / content 摘前 200 字）
	Author      string    `json:"author,omitempty"`       // 作者
	PublishedAt time.Time `json:"published_at,omitempty"` // 发布时间
	GUID        string    `json:"guid,omitempty"`         // 唯一 ID（dedupe 用）
	Source      string    `json:"source,omitempty"`       // feed 源 URL（多 feed 聚合时便于回溯）
	Tags        []string  `json:"tags,omitempty"`         // 分类/category
}

// RSSFetchResult RSS 抓取结果
type RSSFetchResult struct {
	Success    bool      `json:"success"`               // 是否成功抓取到任意 feed
	Items      []RSSItem `json:"items"`                 // 解析出的条目（去重 + 按时间倒序）
	SourceURL  string    `json:"source_url,omitempty"`  // 实际抓取的 feed URL（成功时填充）
	TriedURLs  []string  `json:"tried_urls"`            // 试过的 feed URL 列表，便于 Agent 诊断
	ErrorType  string    `json:"error_type,omitempty"`  // 空 / not_found / parse_error / network_error
	ErrorMsg   string    `json:"error_msg,omitempty"`   // 详细错误信息
	FetchedAt  time.Time `json:"fetched_at"`            // 抓取时间
	HTTPStatus int       `json:"http_status,omitempty"` // 选中 feed 的 HTTP 状态码
}

// RSSFallbackSource 已知的 RSS / Atom feed 候选（来自站点配置 + 通用探测）
type RSSFallbackSource struct {
	Candidates      []string // feed URL 候选，按优先级排序
	Known           bool     // 是否来自内置站点表（true 时跳过 404 自动探测）
	AggregatorFeeds []string // 第三方 RSS 聚合（仅在主候选全部失败时启用；v2.0.21）
}

// ==================== 已知站点 RSS 配置 ====================

// knownRSSFeedCandidates 内置已知站点的 RSS feed URL 列表（基于 LoginWallAlternativeHints
// 已枚举 host 进一步扩展；优先级从前往后，第一个 200 即用）。
// 设计原则：
//   - 必须支持 https（绝大多数站点都强制）
//   - 一站多条候选以容错（站点改版后路径漂移）
//   - 通用兜底（/rss、/feed、/atom.xml）由通用 fallback 路径负责，避免污染已知表
var knownRSSFeedCandidates = map[string][]string{
	// 机器之心 — 公开 RSS
	"www.jiqizhixin.com": {
		"https://www.jiqizhixin.com/rss",
		"https://www.jiqizhixin.com/feed",
	},
	"jiqizhixin.com": {
		"https://www.jiqizhixin.com/rss",
		"https://www.jiqizhixin.com/feed",
	},
	// 36 氪
	"36kr.com": {
		"https://36kr.com/feed",
		"https://36kr.com/feed-newsflash",
		"https://www.36kr.com/feed",
	},
	"www.36kr.com": {
		"https://www.36kr.com/feed",
		"https://36kr.com/feed",
	},
	// 虎嗅
	"huxiu.com": {
		"https://www.huxiu.com/rss/",
		"https://www.huxiu.com/rss.xml",
		"https://www.huxiu.com/feed",
	},
	"www.huxiu.com": {
		"https://www.huxiu.com/rss/",
		"https://www.huxiu.com/rss.xml",
		"https://www.huxiu.com/feed",
	},
	// 量子位
	"qbitai.com": {
		"https://www.qbitai.com/feed",
		"https://www.qbitai.com/rss",
	},
	"www.qbitai.com": {
		"https://www.qbitai.com/feed",
		"https://www.qbitai.com/rss",
	},
	// InfoQ 中文
	"infoq.cn": {
		"https://www.infoq.cn/feed.xml",
		"https://feed.infoq.cn",
	},
	"www.infoq.cn": {
		"https://www.infoq.cn/feed.xml",
		"https://feed.infoq.cn",
	},
	// 新智元
	"ai-topics.com": {
		"https://ai-topics.com/feed",
		"https://www.ai-topics.com/feed",
	},
	// 智东西
	"zhidxcom.com": {
		"https://www.zhidxcom.com/feed",
	},
	"www.zhidxcom.com": {
		"https://www.zhidxcom.com/feed",
	},
	// 钛媒体
	"tmtpost.com": {
		"https://www.tmtpost.com/feed",
	},
	// 极客公园
	"geekpark.net": {
		"https://www.geekpark.net/feed",
	},
	// 少数派
	"sspai.com": {
		"https://sspai.com/feed",
	},
	// SegmentFault
	"segmentfault.com": {
		"https://segmentfault.com/feeds",
	},
	// OSChina
	"oschina.net": {
		"https://www.oschina.net/news/rss",
		"https://www.oschina.net/rss",
	},
	// 掘金
	"juejin.cn": {
		"https://api.juejin.cn/recommend_api/v1/article/rank?type=0&from=rss",
	},
	// 知乎
	"zhihu.com": {
		"https://www.zhihu.com/rss",
		"https://www.zhuanlan.zhihu.com/rss",
	},
	"zhuanlan.zhihu.com": {
		"https://www.zhuanlan.zhihu.com/rss",
	},
	// 爱范儿
	"ifanr.com": {
		"https://www.ifanr.com/feed",
	},
	// 雷锋网
	"leiphone.com": {
		"https://www.leiphone.com/feed",
	},
	// 极客之家
	"geekhome.org": {
		"https://geekhome.org/feed",
	},
	// v2.0.24：TechCrunch（基于 spider_report_data_source_6_2026-07-02 §4 假设 A+D）
	// 浏览器爬虫反复 CDP timeout（JS / 广告 / 第三方脚本使 domcontentloaded /
	// load 长时间不触发）。但官方 RSS feed 稳定可读（标准 RSS 2.0 + 全文摘要在
	// <description>），避免浏览器渲染的不稳定性。/feed/ 是主 feed；
	// /feed/?lang=zh 暂无、保留空；分类 feed 留作 v2.0.24 patch2 扩展。
	"techcrunch.com": {
		"https://techcrunch.com/feed/",
		"https://techcrunch.com/feed",
	},
	"www.techcrunch.com": {
		"https://techcrunch.com/feed/",
		"https://techcrunch.com/feed",
	},
	// v2.0.43: 英文国际站 RSS 兜底（2026-07-14 早报 timeout 数据源）
	"technologyreview.com": {
		"https://www.technologyreview.com/feed/",
		"https://www.technologyreview.com/feed",
	},
	"www.technologyreview.com": {
		"https://www.technologyreview.com/feed/",
		"https://www.technologyreview.com/feed",
	},
	"wired.com": {
		"https://www.wired.com/feed/",
		"https://www.wired.com/feed",
	},
	"www.wired.com": {
		"https://www.wired.com/feed/",
		"https://www.wired.com/feed",
	},
	"marktechpost.com": {
		"https://www.marktechpost.com/feed/",
		"https://www.marktechpost.com/feed",
	},
	"www.marktechpost.com": {
		"https://www.marktechpost.com/feed/",
		"https://www.marktechpost.com/feed",
	},
	"deeplearning.ai": {
		"https://www.deeplearning.ai/the-batch/rss/",
		"https://www.deeplearning.ai/the-batch/feed/",
	},
	"www.deeplearning.ai": {
		"https://www.deeplearning.ai/the-batch/rss/",
		"https://www.deeplearning.ai/the-batch/feed/",
	},
	"therundown.ai": {
		"https://www.therundown.ai/rss",
		"https://www.therundown.ai/feed",
	},
	"www.therundown.ai": {
		"https://www.therundown.ai/rss",
		"https://www.therundown.ai/feed",
	},
	"openreview.net": {
		"https://openreview.net/rss",
		"https://openreview.net/feed",
	},
	"www.openreview.net": {
		"https://openreview.net/rss",
		"https://openreview.net/feed",
	},
}

// ==================== v2.0.21：第三方 RSS 聚合（机器之心兜底） ====================
//
// 背景（问题分析报告_20260630_220512 §3.4 + 实际探测）：
// 机器之心已商业化改版，官方 /rss 自身 302 跳到 /data-service（HTML 推广页），
// 浏览器爬虫 + 移动端 UA + captcha 兜底全部失效。
// 第三方 RSS 聚合站（基于 RSSHub / injahow / feeddd 等）把机器之心当 source
// 重新分发，可作为最后兜底。
//
// 启用条件：当目标 URL 命中已知表，但官方候选全部失败（HTTPStatus != 200 或
// 解析 0 item）时，自动追加下面的第三方候选。

// thirdPartyRSSAggregators 第三方 RSS 聚合站候选（每条按 host pattern 过滤）
// 模板规则：把目标 host 替换成下面 key 的 host 即可。
// 例：jiqizhixin.com 触发 rsshub.app/jiqizhixin
type thirdPartyRSSRule struct {
	Pattern string   // 匹配目标 host 的子串
	Sources []string // 候选 URL 模板（用 {host} 占位）
	Notes   string   // 调试日志
}

var thirdPartyRSSAggregators = []thirdPartyRSSRule{
	{
		// rsshub.app — 开源 RSSHub 公共实例，国内 AI 媒体覆盖较全
		// 注意：rsshub.app 公共实例偶发 502，不保证稳定；仅作为最后兜底
		Pattern: "jiqizhixin.com",
		Sources: []string{
			"https://rsshub.app/jiqizhixin",
			"https://rss.injahow.cn/jiqizhixin",
		},
		Notes: "机器之心第三方聚合",
	},
	{
		Pattern: "qbitai.com",
		Sources: []string{
			"https://rsshub.app/qbitai",
		},
	},
	{
		Pattern: "huxiu.com",
		Sources: []string{
			"https://rsshub.app/huxiu",
		},
	},
	{
		Pattern: "36kr.com",
		Sources: []string{
			"https://rsshub.app/36kr",
		},
	},
	{
		Pattern: "infoq.cn",
		Sources: []string{
			"https://rsshub.app/infoq",
		},
	},
	{
		Pattern: "zhidxcom.com",
		Sources: []string{
			"https://rsshub.app/zhidxcom",
		},
	},
	// v2.0.24：TechCrunch 第三方 RSS 兜底（spider_report_data_source_6_2026-07-02 §5.2 建议）
	// 浏览器反复 CDP timeout 但官方 RSS 通常可用；rsshub.app 的 /techcrunch 转发
	// 作为最后兜底。注意：rsshub 公共实例偶发 502，不保证稳定；仅作为最后兜底。
	{
		Pattern: "techcrunch.com",
		Sources: []string{
			"https://rsshub.app/techcrunch",
		},
		Notes: "TechCrunch 第三方聚合",
	},
}

// ==================== 核心 API ====================

// LookupRSSFallbackSources 根据目标 URL 返回 RSS fallback 候选 URL 列表
// 查找策略：
//  1. 解析 URL，取 host（去掉 www. 前缀）
//  2. 查 knownRSSFeedCandidates（带 www. 与不带两份都查）
//  3. 找不到时返回通用兜底（/rss / /feed / /atom.xml）
//
// 返回 (sources, known)：
//   - sources: 候选 URL 列表
//   - known: 是否命中已知站点表（true 时调用方可跳过 404 探测，直接顺序试）；
//     false 时调通用探测流程，避免对未知站点硬编码导致大批量 404 噪音
func LookupRSSFallbackSources(targetURL string) (RSSFallbackSource, error) {
	if strings.TrimSpace(targetURL) == "" {
		return RSSFallbackSource{}, fmt.Errorf("LookupRSSFallbackSources: targetURL is empty")
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return RSSFallbackSource{}, fmt.Errorf("LookupRSSFallbackSources: invalid URL %q: %v", targetURL, err)
	}
	host := strings.ToLower(u.Host)
	hostNoWWW := strings.TrimPrefix(host, "www.")

	// 主表 + 兼容 www. 前缀
	if cands, ok := knownRSSFeedCandidates[host]; ok {
		return RSSFallbackSource{
			Candidates:      cands,
			Known:           true,
			AggregatorFeeds: matchAggregatorFeeds(host + " " + hostNoWWW),
		}, nil
	}
	if cands, ok := knownRSSFeedCandidates[hostNoWWW]; ok {
		return RSSFallbackSource{
			Candidates:      cands,
			Known:           true,
			AggregatorFeeds: matchAggregatorFeeds(host + " " + hostNoWWW),
		}, nil
	}

	// 通用兜底：保留 path（多数 RSS feed 全站统一），加三个常见路径
	u2 := *u
	if strings.Contains(u2.Path, "/rss") || strings.Contains(u2.Path, "/feed") || strings.Contains(u2.Path, "/atom") {
		// URL 路径里已经包含 rss / feed / atom 关键字，直接作为候选
		u2.Path = strings.TrimRight(u2.Path, "/")
		feedPath := u2.Path
		return RSSFallbackSource{
			Candidates: []string{u2.Scheme + "://" + u2.Host + feedPath},
			Known:      true,
		}, nil
	}
	// 推断 base path（去掉尾部 /articles 之类的文章路径，拿到 site root）
	rootPath := "/"
	if idx := strings.Index(u2.Path, "/articles"); idx > 0 {
		rootPath = u2.Path[:idx]
	} else if idx := strings.Index(u2.Path, "/article/"); idx > 0 {
		rootPath = u2.Path[:idx]
	} else if idx := strings.Index(u2.Path, "/news/"); idx > 0 {
		rootPath = u2.Path[:idx]
	}
	scheme := u2.Scheme
	if scheme == "" {
		scheme = "https"
	}
	rootURL := scheme + "://" + u2.Host + rootPath
	return RSSFallbackSource{
		Candidates: []string{
			rootURL + "rss",
			rootURL + "feed",
			rootURL + "atom.xml",
			// 保留原始 URL 自身（万一是 feed 直接命中）
			targetURL,
		},
		Known:           false,
		AggregatorFeeds: matchAggregatorFeeds(host + " " + hostNoWWW),
	}, nil
}

// matchAggregatorFeeds 命中第三方 RSS 聚合候选（v2.0.21 新增）
// 输入：host 字符串（可能含空格分隔的 www./裸 host）
// 输出：聚合站 RSS URL 列表（按规则顺序）
//
// 触发条件：机器之心等已商业化改版的站点，官方 /rss 自身 302 跳到登录墙，
// 此时需要第三方 RSSHub 聚合作为最后兜底。
func matchAggregatorFeeds(host string) []string {
	if host == "" {
		return nil
	}
	var out []string
	for _, rule := range thirdPartyRSSAggregators {
		if strings.Contains(host, rule.Pattern) {
			out = append(out, rule.Sources...)
		}
	}
	return out
}

// FetchRSSTries 顺序尝试候选 URL，返回首个能解析为 RSS/Atom 的 feed 内容与条目列表
// 参数：
//   - sources: 候选 URL 列表（按优先级）
//   - httpClient: 复用调用方的 http.Client（nil 时用默认 8s 超时 client）
//   - maxItems: 单 feed 最大返回条目数（0 或负数默认 50）
//
// 返回：解析结果（含成功/失败明细 + TriedURLs）；调用方按 Success 字段判断是否继续
func FetchRSSTries(ctx context.Context, sources RSSFallbackSource, httpClient *http.Client, maxItems int) RSSFetchResult {
	if maxItems <= 0 {
		maxItems = 50
	}
	// v2.0.43: RSS fallback 超时改为可配置（默认 15s），提升国际慢网命中。
	// 调用方传自定义 httpClient 时不覆盖其 Timeout。
	rssTimeout := getSpiderRSSFetchTimeout()
	if rssTimeout <= 0 {
		rssTimeout = 15 * time.Second
	}
	client := httpClient
	if client == nil {
		client = &http.Client{
			Timeout: rssTimeout,
			// 不要跟随 HTTP→HTTPS 重定向到登录墙；以 https 为首选避免此场景
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}
	}
	result := RSSFetchResult{
		Items:     []RSSItem{},
		TriedURLs: []string{},
		FetchedAt: time.Now().UTC(),
	}
	// v2.0.21: 把主候选 + 第三方聚合候选拼成有序列表，依次尝试。
	// 第三方聚合仅作为最后兜底（前序全失败才轮到），但成功时仍归一化返回。
	allCandidates := append([]string{}, sources.Candidates...)
	allCandidates = append(allCandidates, sources.AggregatorFeeds...)
	for _, url := range allCandidates {
		result.TriedURLs = append(result.TriedURLs, url)
		reqCtx, cancel := context.WithTimeout(ctx, rssTimeout)
		req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
		if err != nil {
			cancel()
			result.ErrorType = "build_request_error"
			result.ErrorMsg = err.Error()
			continue
		}
		req.Header.Set("User-Agent", "LsmTokensServer-RSS-Fallback/2.0")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.8")
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			result.ErrorType = "network_error"
			result.ErrorMsg = err.Error()
			continue
		}
		if resp.StatusCode != 200 {
			result.HTTPStatus = resp.StatusCode
			resp.Body.Close()
			result.ErrorType = "not_found"
			result.ErrorMsg = fmt.Sprintf("HTTP %d for %s", resp.StatusCode, url)
			// 对未认证的登录墙重定向（403 / 401）也直接下一个
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB 上限
		resp.Body.Close()
		if readErr != nil {
			result.ErrorType = "read_error"
			result.ErrorMsg = readErr.Error()
			continue
		}
		items, parseErr := parseRSSOrAtom(string(body), resp.Header.Get("Content-Type"))
		if parseErr != nil {
			result.ErrorType = "parse_error"
			result.ErrorMsg = parseErr.Error()
			continue
		}
		if len(items) == 0 {
			// 解析成功但零条目（罕见），继续尝试下一个候选
			result.ErrorType = "empty_feed"
			result.ErrorMsg = "parsed 0 items from " + url
			continue
		}
		// 成功：填充 SourceURL + 截断 + 去重
		result.HTTPStatus = resp.StatusCode
		result.SourceURL = url
		seen := make(map[string]struct{}, len(items))
		uniq := make([]RSSItem, 0, len(items))
		for _, it := range items {
			if it.URL == "" && it.GUID == "" {
				continue
			}
			key := it.URL
			if key == "" {
				key = it.GUID
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			uniq = append(uniq, it)
			if len(uniq) >= maxItems {
				break
			}
		}
		// 按发布时间倒序（无发布时间按原始顺序）
		sortRSSItemsByDateDesc(uniq)
		result.Items = uniq
		result.Success = true
		result.ErrorType = ""
		result.ErrorMsg = ""
		return result
	}
	// 全部试过都失败
	if result.ErrorType == "" {
		result.ErrorType = "unknown"
		result.ErrorMsg = fmt.Sprintf("all %d candidates failed", len(result.TriedURLs))
	}
	return result
}

// ==================== RSS / Atom 解析器 ====================

// rssFeed RSS 2.0 schema 子集
type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}
type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Author      string `xml:"author"`
	Category    string `xml:"category"`
	DCCreator   string `xml:"http://purl.org/dc/elements/1.1/ creator"`
}

// atomFeed Atom 1.0 schema 子集
type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}
type atomEntry struct {
	Title     string     `xml:"title"`
	ID        string     `xml:"id"`
	Link      atomLink   `xml:"link"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Updated   string     `xml:"updated"`
	Published string     `xml:"published"`
	Author    atomAuthor `xml:"author"`
	Cats      []atomCat  `xml:"category"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
}
type atomAuthor struct {
	Name string `xml:"name"`
}
type atomCat struct {
	Term string `xml:"term,attr"`
}

// parseRSSOrAtom 解析 RSS 2.0 或 Atom 1.0 feed
// 容错策略：先按 RSS 解析（含 xmlns 默认 ns）；失败再按 Atom 解析；二者都失败时返回 error
func parseRSSOrAtom(body, contentType string) ([]RSSItem, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("empty body")
	}
	// 检测 doctype 后再决定解析路径
	isAtom := strings.Contains(contentType, "atom") ||
		strings.Contains(body, "<feed ") ||
		strings.Contains(body, "<feed>")
	isRSS := strings.Contains(contentType, "rss") ||
		strings.Contains(body, "<rss ") ||
		strings.Contains(body, "<rss>")

	var items []RSSItem
	switch {
	case isAtom && !isRSS:
		var feed atomFeed
		if err := xml.Unmarshal([]byte(body), &feed); err != nil {
			return nil, fmt.Errorf("atom parse error: %w", err)
		}
		for _, e := range feed.Entries {
			items = append(items, atomEntryToRSS(e))
		}
	case isRSS:
		var feed rssFeed
		if err := xml.Unmarshal([]byte(body), &feed); err != nil {
			// 有些站输出 RSS 2.0 但 prefix 是 dc:；兜底用更宽松解析
			items, err2 := parseRSSLoose(body)
			if err2 != nil {
				return nil, fmt.Errorf("rss parse error: %w", err)
			}
			return items, nil
		}
		for _, it := range feed.Channel.Items {
			items = append(items, rssItemToRSS(it))
		}
	default:
		// 未识别：先按 RSS 再按 Atom 各试一次
		var rssFeedObj rssFeed
		if err := xml.Unmarshal([]byte(body), &rssFeedObj); err == nil && len(rssFeedObj.Channel.Items) > 0 {
			for _, it := range rssFeedObj.Channel.Items {
				items = append(items, rssItemToRSS(it))
			}
			return items, nil
		}
		var atomFeedObj atomFeed
		if err := xml.Unmarshal([]byte(body), &atomFeedObj); err == nil && len(atomFeedObj.Entries) > 0 {
			for _, e := range atomFeedObj.Entries {
				items = append(items, atomEntryToRSS(e))
			}
			return items, nil
		}
		return nil, fmt.Errorf("unknown feed format (no <rss> or <feed> root)")
	}
	return items, nil
}

// parseRSSLoose 宽松 RSS 解析（处理 xmlns 命名空间夹杂的情况）
func parseRSSLoose(body string) ([]RSSItem, error) {
	// 极简实现：按 <item>…</item> 块切分，每个块独立处理
	type looseItem struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		GUID        string `xml:"guid"`
		Description string `xml:"description"`
		PubDate     string `xml:"pubDate"`
	}
	var looseFeed struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Items []looseItem `xml:"channel>item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(body), &looseFeed); err != nil {
		return nil, fmt.Errorf("loose rss parse error: %w", err)
	}
	var items []RSSItem
	for _, it := range looseFeed.Channel.Items {
		items = append(items, RSSItem{
			Title:       strings.TrimSpace(it.Title),
			URL:         strings.TrimSpace(it.Link),
			GUID:        strings.TrimSpace(it.GUID),
			Summary:     truncateRunes(strings.TrimSpace(system.StripHTMLTags(it.Description)), 200),
			PublishedAt: parseFlexibleDate(it.PubDate),
		})
	}
	return items, nil
}

// system.stripHTMLTags 复用 ai_api_connectivity.go 中的同名函数（极简 regex 替换版）

// rssItemToRSS RSS item → 通用 RSSItem
func rssItemToRSS(it rssItem) RSSItem {
	author := it.Author
	if author == "" {
		author = it.DCCreator
	}
	tags := []string{}
	if it.Category != "" {
		tags = append(tags, it.Category)
	}
	return RSSItem{
		Title:       strings.TrimSpace(it.Title),
		URL:         strings.TrimSpace(it.Link),
		GUID:        strings.TrimSpace(it.GUID),
		Summary:     truncateRunes(strings.TrimSpace(system.StripHTMLTags(it.Description)), 200),
		Author:      strings.TrimSpace(author),
		PublishedAt: parseFlexibleDate(it.PubDate),
		Tags:        tags,
	}
}

// atomEntryToRSS Atom entry → 通用 RSSItem
func atomEntryToRSS(e atomEntry) RSSItem {
	tags := make([]string, 0, len(e.Cats))
	for _, c := range e.Cats {
		if c.Term != "" {
			tags = append(tags, c.Term)
		}
	}
	summary := e.Summary
	if summary == "" {
		summary = e.Content
	}
	date := e.Published
	if date == "" {
		date = e.Updated
	}
	return RSSItem{
		Title:       strings.TrimSpace(e.Title),
		URL:         strings.TrimSpace(e.Link.Href),
		GUID:        strings.TrimSpace(e.ID),
		Summary:     truncateRunes(strings.TrimSpace(system.StripHTMLTags(summary)), 200),
		Author:      strings.TrimSpace(e.Author.Name),
		PublishedAt: parseFlexibleDate(date),
		Tags:        tags,
	}
}

// parseFlexibleDate 解析多种 feed 日期格式（容错）
// 支持：RFC1123Z / RFC1123 / RFC3339 / RFC3339Nano / RFC822 / RFC850 / RFC1036 / unix 时间戳
// 失败时返回零值（Agent 据此当作排序末位）
func parseFlexibleDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	// 数字时间戳
	if n, err := json.Number(s).Int64(); err == nil && n > 0 {
		// 10 位 vs 13 位
		if n < 10000000000 {
			return time.Unix(n, 0).UTC()
		}
		return time.UnixMilli(n).UTC()
	}
	formats := []string{
		time.RFC1123Z,                   // Mon, 02 Jan 2006 15:04:05 -0700
		time.RFC1123,                    // Mon, 02 Jan 2006 15:04:05 MST
		time.RFC3339,                    // 2006-01-02T15:04:05Z07:00
		time.RFC3339Nano,                // 2006-01-02T15:04:05.999999999Z07:00
		time.RFC822,                     // 02 Jan 06 15:04 MST
		time.RFC822Z,                    // 02 Jan 06 15:04 -0700
		time.RFC850,                     // Monday, 02-Jan-06 15:04:05 MST
		"Mon, 02 Jan 06 15:04:05 -0700", // RFC1036 等价 layout
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC()
		}
	}
	// 一些奇怪格式（如 "Mon, 30 Jun 2026 14:00:00 +0800"，CST 等）
	// 兜底：再用 time.Parse 配合 varargs layout
	if t, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// sortRSSItemsByDateDesc 按 PublishedAt 倒序排序（无时间的下沉；zerotime 视作 1970）
// 保持原顺序内的稳定性（用 stable sort）
func sortRSSItemsByDateDesc(items []RSSItem) {
	// 冒泡式稳定排序在元素数 ≤ 100 时性能可接受；超过 100 时代价 O(N^2) 仍可控
	n := len(items)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			ti, tj := items[i].PublishedAt, items[j].PublishedAt
			if !ti.IsZero() && !tj.IsZero() {
				if ti.Before(tj) {
					items[i], items[j] = items[j], items[i]
				}
			} else if !tj.IsZero() && ti.IsZero() {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

// rssFetchResultToElements 把 RSSFetchResult 转成 SpiderWebDataResponse.Elements 兼容的
// 结构（links + headings + articles），便于 Agent 沿用 elements 解析逻辑。
//
// 输出字段：
//   - Links: 每条 RSS item 一条 link（URL + text）
//   - Headings: 每条 RSS item 一条 heading（level=2 + title + URL）
//   - Articles: 每条 RSS item 一条 article 卡片（title + URL + summary + position）
//
// 注意：WebElementArticle / WebElementHeading / WebElementLink 字段定义见
// mcp_interface_common.go；这里只填充 JSON tag 中已声明的字段。
// 未声明的（Author / PublishedAt / Tags）以「@author (YYYY-MM-DD) [tag,...]」
// 格式追加到 Summary 末尾，便于 Agent 在不增加新结构体的情况下读到作者/时间信息。
//
// 注意：本函数为转换层；调用方负责把 Elements 挂回 SpiderWebDataResponse。
func rssFetchResultToElements(r *RSSFetchResult) (links []WebElementLink, headings []WebElementHeading, articles []WebElementArticle) {
	if r == nil || len(r.Items) == 0 {
		return []WebElementLink{}, []WebElementHeading{}, []WebElementArticle{}
	}
	links = make([]WebElementLink, 0, len(r.Items))
	headings = make([]WebElementHeading, 0, len(r.Items))
	articles = make([]WebElementArticle, 0, len(r.Items))
	for idx, it := range r.Items {
		if it.URL != "" {
			links = append(links, WebElementLink{
				URL:   it.URL,
				Text:  it.Title,
				Scope: "rss",
			})
			headings = append(headings, WebElementHeading{
				Level: 2,
				Text:  it.Title,
				URL:   it.URL,
			})
		}
		// 摘要补 metadata（作者 / 时间 / 标签），保留在 Summary 内便于 Agent 一次读到
		summary := it.Summary
		if it.Author != "" || !it.PublishedAt.IsZero() || len(it.Tags) > 0 {
			meta := ""
			if it.Author != "" {
				meta += " @" + it.Author
			}
			if !it.PublishedAt.IsZero() {
				meta += " (" + it.PublishedAt.Format("2006-01-02") + ")"
			}
			if len(it.Tags) > 0 {
				meta += " [" + strings.Join(it.Tags, ",") + "]"
			}
			summary = summary + meta
		}
		articles = append(articles, WebElementArticle{
			Title:    it.Title,
			URL:      it.URL,
			Summary:  summary,
			Position: idx,
		})
	}
	return links, headings, articles
}

// truncateRunes 在 n runes 处截断字符串（多字节 UTF-8 安全）
// 复用 mcp_interface_common.go 里的 truncateRunes（避免重复实现）。
// 注：本文件是 v2.0.20 新增，避免循环依赖，统一从 mcp_interface_common.go 调用。

// ==================== v2.0.21：从原始 HTML 中抽取文章列表（终极兜底） ====================
//
// 背景（问题分析报告_20260630_220512 §3.2-§3.3）：机器之心把整个 body 替换为
// 推广页时，浏览器抓到的 RawHTML 里仍可能含有 <a href="/articles/123">…</a>
// 形式的 SSR 渲染链接（外链、推荐阅读、底部推荐）。这些链接能帮助 Agent
// 至少知道该站还有哪些文章存在。
//
// 调用场景：RSS / Atom / 第三方聚合全部失败时，从 lastResult.RawHTML 中
// 提取"形似文章 URL"的链接，转成等价 RSSItem，组装为最小可用响应。
//
// 设计原则：
//   - 启发式规则识别：URL 含 /article(s)?/、/news/、/post/、/blog/ 等
//   - 仅当链接 text 长度 >= 6 字符时采纳（避免拿"登录""注册"等无效文字）
//   - 同一 host 范围内去重，最多 50 条
//   - 不修改原 RawHTML 字段，避免污染 Agent 已有内容

// looksLikeArticleURL 启发式判断 URL 是否像文章路径
// 规则：必须含 /article/ /articles/ /news/ /post/ /blog/ /p/ /archives/
// 或 .html / .htm 后缀之一
func looksLikeArticleURL(u string) bool {
	lu := strings.ToLower(u)
	if lu == "" || strings.HasPrefix(lu, "javascript:") || strings.HasPrefix(lu, "mailto:") || strings.HasPrefix(lu, "#") {
		return false
	}
	if strings.HasPrefix(lu, "data:") {
		return false
	}
	// 常见文章路径
	// v2.0.25：追加 "/search?" 模式以识别搜索结果页 URL（如机器之心商业化改版后，
	// 未登录用户访问 /articles 会被 302 重定向到 /search?q=xxx，URL 形态为搜索结果页）。
	// 注意：仅接受含查询参数的 /search?... 形式；裸 /search 是搜索入口视口，不算文章。
	patterns := []string{"/article/", "/articles/", "/news/", "/post/", "/posts/", "/blog/", "/p/", "/archives/", "/story/", "/stories/", "/column/", "/search?"}
	for _, p := range patterns {
		if strings.Contains(lu, p) {
			return true
		}
	}
	// 静态页后缀
	if strings.HasSuffix(lu, ".html") || strings.HasSuffix(lu, ".htm") {
		// 排除首页（很短路径）
		path := lu
		if idx := strings.Index(path, "?"); idx >= 0 {
			path = path[:idx]
		}
		// /index.html / /default.html 视为非文章
		if strings.HasSuffix(path, "/index.html") || strings.HasSuffix(path, "/default.html") {
			return false
		}
		return true
	}
	return false
}

// ExtractArticleURLsFromHTML 终极兜底：直接从原始 HTML 中提取"形似文章"的 URL
// 输入：原始 HTML、基础 URL（用于相对路径解析）
// 输出：RSSItem 列表（Title 暂用链接 text，无发布时间）
//
// 与 extractArticleCards（v2.0.18 已有）的关系：
//   - extractArticleCards 要求 <li>/<article> 卡片 + h2/h3 标题；
//     captcha 替换 body 后通常不再有完整卡片结构，命中 0
//   - 本函数放宽要求，只要 URL 像文章路径 + text 长度足够就采纳
func ExtractArticleURLsFromHTML(html, baseURL string) []RSSItem {
	if strings.TrimSpace(html) == "" {
		return nil
	}
	// 复用 mcp_interface_common.go 的 reAllAnchor / parseAttrs
	// 跨包访问：同 package 直接用
	matches := reAllAnchor.FindAllStringSubmatchIndex(html, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]RSSItem, 0, 16)
	seen := make(map[string]struct{}, 16)
	for _, m := range matches {
		if len(m) < 6 {
			continue
		}
		// v2.0.34：submatch 索引统一走 safeSubmatchSlice，杜绝负数索引 panic
		attrStr := safeSubmatchSlice(html, m, 1)
		innerStr := safeSubmatchSlice(html, m, 2)
		href, _, _ := parseAttrs(attrStr)
		href = strings.TrimSpace(htmlUnescapeSimple(href))
		if !looksLikeArticleURL(href) {
			continue
		}
		text := cleanText(removeHTMLTagsSimple(innerStr))
		if utf8RuneCount(text) < 6 {
			continue
		}
		abs := resolveURL(baseURL, href)
		if abs == "" {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, RSSItem{
			Title:  truncateRunes(text, 200),
			URL:    abs,
			Source: "html-fallback",
		})
		if len(out) >= 50 {
			break
		}
	}
	return out
}

// utf8RuneCount 计算字符串 rune 数
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
