package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

// Global DB instance
var (
	DB *gorm.DB
)

// StatsQueryTimeout 统计类查询的默认超时。
// v2.0.54: 与前端 AbortController(30s) 对齐并留余量。超时后 context 取消，
// go-sql-driver 会向 MySQL 发送 KILL 中断该查询并归还连接，杜绝连接泄漏。
const StatsQueryTimeout = 25 * time.Second

// newStatsQueryCtx 返回一个带默认统计超时的 context 及其取消函数。
// 用法：ctx, cancel := newStatsQueryCtx(); defer cancel(); DB.WithContext(ctx)...
// v2.0.54: 让「超时」语义下沉到 DB 层——超时时驱动真正中断 SQL 并释放连接，
// 而不是像旧实现那样只是「放弃等待」而底层查询仍占着连接。
func newStatsQueryCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), StatsQueryTimeout)
}

// statsDB 返回一个绑定了统计超时 context 的 *gorm.DB 会话。
// 调用方必须 defer 返回的 cancel。DB 为 nil 时返回 (nil, no-op cancel)。
// v2.0.54: 所有 /ChatAnalysisTotal 统计查询统一通过它拿会话，保证有界返回。
func StatsDB() (*gorm.DB, context.CancelFunc) {
	if DB == nil {
		return nil, func() {}
	}
	ctx, cancel := newStatsQueryCtx()
	return DB.WithContext(ctx), cancel
}

// buildGormConfig 构造 GORM 配置（纯函数，便于单测断言 LogLevel）。
// v2.0.54: Logger 从逐条打印全部 SQL 的 Info 级降为 Warn 级 + 2s 慢查询阈值，
// 避免高频代理写库 + 2 万行统计查询把 LsmTokensServer.log 撑爆、拖慢 IO。
func buildGormConfig() *gorm.Config {
	newLogger := gormLogger.New(
		nil, // Writer 置空：gormLogger.Warn 级别下不额外输出
		gormLogger.Config{
			SlowThreshold:             2 * time.Second, // 仅 >2s 的查询打慢查询日志
			LogLevel:                  gormLogger.Warn, // 只打 warning / error，不逐条打印 SQL
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	return &gorm.Config{Logger: newLogger}
}

// InitMySQL 初始化 MySQL 连接
func InitMySQL(cfg *config.DBMysqlConfig) error {
	// 构建 DSN
	// 格式: user:password@tcp(addr:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local&...
	// v2.0.54: 追加驱动级超时 —— readTimeout/writeTimeout 是防「MySQL 假死时连接永久卡住」的关键；
	// timeout 是建连超时。三者与 DB.WithContext(ctx) 双重保险。
	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=True&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s",
		cfg.User, cfg.Pwd, cfg.Url, cfg.DataBase)

	// 连接数据库
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), buildGormConfig())
	if err != nil {
		return fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	// 获取底层 SQL DB 对象设置连接池
	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// 设置连接池参数
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	// v2.0.54: Timeout<=0 时不要设成「永不过期」，给一个安全默认，避免陈旧连接堆积。
	lifetimeMin := cfg.Timeout
	if lifetimeMin <= 0 {
		lifetimeMin = config.DEFAULT_MYSQL_TIMEOUT
	}
	sqlDB.SetConnMaxLifetime(time.Duration(lifetimeMin) * time.Minute)
	// v2.0.54: 空闲连接最多保留 5 分钟，避免长期占用连接不释放。
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	// 测试连接
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping MySQL: %w", err)
	}

	log.Printf("[INIT] MySQL connected successfully: %s@%s/%s", cfg.User, cfg.Url, cfg.DataBase)
	return nil
}

// CloseMySQL 关闭 MySQL 连接
func CloseMySQL() {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err == nil {
			sqlDB.Close()
		}
	}
}
