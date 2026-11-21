package handler

import (
	"sync"
	"time"
)

const (
	// ticketWriteRateLimit 与 ticketWriteRateWindow 对应 design.md §1.3 中
	// 「handler 内 1 分钟 5 次的轻量计数」：用户创建工单与追加回复合计受限。
	ticketWriteRateLimit  = 5
	ticketWriteRateWindow = time.Minute
)

// ticketWriteRateLimiter 是用户侧工单写操作的进程内按用户滑动窗口限流器。
//
// 设计要点：
//   - 只用于「创建工单 + 追加回复」两类写操作，读接口与管理端不受影响；
//   - 不引入第三方依赖，也不需要 Redis：单实例进程内计数即可满足防刷要求，
//     多副本部署时每个副本各自计数（与 panel 全局限流的进程内语义一致）；
//   - 只记录放行的时间戳；被拒绝的请求不写入，持续重试不会把禁用窗口向后顺延；
//   - 每次 Allow 惰性清理所有用户的过期时间戳与空键，长期运行不会无限增长；
//   - now 可注入，测试窗口滑动无需真正等待 60 秒。
type ticketWriteRateLimiter struct {
	mu     sync.Mutex
	hits   map[int64][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

// newTicketWriteRateLimiter 创建限流器；limit 或 window 非正时等价于关闭限流。
func newTicketWriteRateLimiter(limit int, window time.Duration) *ticketWriteRateLimiter {
	return &ticketWriteRateLimiter{
		hits:   make(map[int64][]time.Time),
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

// Allow 记录一次写操作并报告是否放行。并发安全。
func (l *ticketWriteRateLimiter) Allow(userID int64) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now)
	if len(l.hits[userID]) >= l.limit {
		return false
	}
	l.hits[userID] = append(l.hits[userID], now)
	return true
}

// RetryAfter 返回该用户窗口内最早一次写操作过期所需的时间；未触发限流时返回 0。
// 供 429 响应设置 Retry-After 头使用。
func (l *ticketWriteRateLimiter) RetryAfter(userID int64) time.Duration {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return 0
	}
	now := l.now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// 只读过滤，不原地压缩底层数组（否则 map 中仍是旧长度，会重复计数）。
	active := make([]time.Time, 0, l.limit)
	for _, ts := range l.hits[userID] {
		if ts.After(cutoff) {
			active = append(active, ts)
		}
	}
	if len(active) < l.limit {
		return 0
	}
	wait := active[0].Add(l.window).Sub(now)
	if wait < 0 {
		return 0
	}
	return wait
}

// pruneLocked 清理所有用户的过期时间戳，空键直接删除。调用方必须持有锁。
func (l *ticketWriteRateLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-l.window)
	for userID, timestamps := range l.hits {
		// 时间戳按追加顺序即时间升序，过滤后写回同一底层数组是安全的。
		kept := timestamps[:0]
		for _, ts := range timestamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(l.hits, userID)
			continue
		}
		l.hits[userID] = kept
	}
}
