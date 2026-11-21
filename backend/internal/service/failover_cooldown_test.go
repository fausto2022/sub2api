//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 本文件覆盖 T4.1 service 侧的纯逻辑与契约（design.md §3.4 / §3.5.7）：
//   - TTL 规范化（上下限全部来自 config 的 default_ttl / max_ttl，代码里没有写死时长）；
//   - fail-open 降级计数（P5 的 failover_cooldown_degraded_total 数据源）；
//   - FailoverCooldownStore 的内存实现，用来固化「命中/未命中、extend-only、清除」的语义，
//     后续 T4.2/T4.3 的接线测试可以直接复用它（implementation.md T4.1 的「内存实现（测试用）」）。

// ---------------------------------------------------------------------------
// 内存实现（测试用）
// ---------------------------------------------------------------------------

// failoverCooldownMemoryStore 是 FailoverCooldownStore 的内存实现，语义与 Redis 实现对齐：
// 命中/未命中、extend-only（不缩短既有冷却）、清除、探测令牌只放行 1 个。
type failoverCooldownMemoryStore struct {
	mu      sync.Mutex
	bounds  FailoverCooldownTTLBounds
	now     func() time.Time
	cool    map[int64]FailoverCooldownEntry
	expire  map[int64]time.Time
	streaks map[int64]int
	probes  map[int64]time.Time
	window  time.Duration
}

func newFailoverCooldownMemoryStore(bounds FailoverCooldownTTLBounds, window time.Duration) *failoverCooldownMemoryStore {
	return &failoverCooldownMemoryStore{
		bounds:  bounds,
		now:     time.Now,
		cool:    make(map[int64]FailoverCooldownEntry),
		expire:  make(map[int64]time.Time),
		streaks: make(map[int64]int),
		probes:  make(map[int64]time.Time),
		window:  window,
	}
}

// advance 推进内存时钟（测试用，避免 sleep）。
func (s *failoverCooldownMemoryStore) advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.now = func() time.Time { return now.Add(d) }
}

func (s *failoverCooldownMemoryStore) IsCooledBatch(_ context.Context, accountIDs []int64) map[int64]FailoverCooldownEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cooled := make(map[int64]FailoverCooldownEntry)
	now := s.now()
	for _, accountID := range accountIDs {
		entry, ok := s.cool[accountID]
		if !ok {
			continue
		}
		if now.After(s.expire[accountID]) {
			delete(s.cool, accountID)
			delete(s.expire, accountID)
			continue
		}
		cooled[accountID] = entry
	}
	return cooled
}

func (s *failoverCooldownMemoryStore) IsModelCooledBatch(context.Context, []int64, string) map[int64]FailoverCooldownEntry {
	// 内存实现只覆盖账号级（模型级由 repository 的 Redis 测试覆盖）。
	return map[int64]FailoverCooldownEntry{}
}

func (s *failoverCooldownMemoryStore) Cool(_ context.Context, write FailoverCooldownWrite) FailoverCooldownWriteResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl := NormalizeFailoverCooldownTTL(write.TTL, s.bounds, write.UseDefaultTTL)
	if ttl <= 0 {
		return FailoverCooldownWriteResult{}
	}
	now := s.now()
	if expireAt, ok := s.expire[write.AccountID]; ok && expireAt.Sub(now) > ttl {
		return FailoverCooldownWriteResult{Applied: false, TTL: expireAt.Sub(now)}
	}
	s.cool[write.AccountID] = FailoverCooldownEntry{Class: write.Class, Deadline: now.Add(ttl)}
	s.expire[write.AccountID] = now.Add(ttl)
	return FailoverCooldownWriteResult{Applied: true, TTL: ttl}
}

func (s *failoverCooldownMemoryStore) Clear(_ context.Context, accountID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cool, accountID)
	delete(s.expire, accountID)
	delete(s.streaks, accountID)
}

func (s *failoverCooldownMemoryStore) IncrFailureStreak(_ context.Context, accountID int64, window time.Duration) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	effective := window
	if effective <= 0 {
		effective = s.window
	}
	if effective <= 0 {
		return 0
	}
	s.streaks[accountID]++
	return int64(s.streaks[accountID])
}

func (s *failoverCooldownMemoryStore) AcquireProbeToken(_ context.Context, accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if expireAt, ok := s.probes[accountID]; ok && now.Before(expireAt) {
		return false
	}
	s.probes[accountID] = now.Add(10 * time.Second)
	return true
}

func (s *failoverCooldownMemoryStore) ClearProbeToken(_ context.Context, accountID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.probes, accountID)
}

var _ FailoverCooldownStore = (*failoverCooldownMemoryStore)(nil)

// ---------------------------------------------------------------------------
// TTL 规范化：上下限全部来自配置
// ---------------------------------------------------------------------------

func TestFailoverCooldownTTLBoundsFromConfig(t *testing.T) {
	cfg := config.FailoverCooldownConfig{
		DefaultTTL: 90 * time.Second,
		MaxTTL:     3 * time.Hour,
	}
	bounds := FailoverCooldownTTLBoundsFromConfig(cfg)
	if bounds.DefaultTTL != 90*time.Second {
		t.Fatalf("DefaultTTL = %v, want 90s（必须来自配置）", bounds.DefaultTTL)
	}
	if bounds.MaxTTL != 3*time.Hour {
		t.Fatalf("MaxTTL = %v, want 3h（必须来自配置）", bounds.MaxTTL)
	}
}

func TestNormalizeFailoverCooldownTTL(t *testing.T) {
	tests := []struct {
		name       string
		ttl        time.Duration
		bounds     FailoverCooldownTTLBounds
		useDefault bool
		want       time.Duration
	}{
		{
			name:   "TTL=0 且不兜底 → 0（不冷却）",
			ttl:    0,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
			want:   0,
		},
		{
			name:       "TTL=0 且要求兜底 → 配置 default_ttl",
			ttl:        0,
			bounds:     FailoverCooldownTTLBounds{DefaultTTL: 45 * time.Second, MaxTTL: 24 * time.Hour},
			useDefault: true,
			want:       45 * time.Second,
		},
		{
			name:       "负 TTL 且要求兜底 → 配置 default_ttl",
			ttl:        -3 * time.Second,
			bounds:     FailoverCooldownTTLBounds{DefaultTTL: 45 * time.Second, MaxTTL: 24 * time.Hour},
			useDefault: true,
			want:       45 * time.Second,
		},
		{
			name:   "负 TTL 且不兜底 → 0（不冷却）",
			ttl:    -3 * time.Second,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
			want:   0,
		},
		{
			name:   "超过 max_ttl → 截断到配置上限",
			ttl:    48 * time.Hour,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 10 * time.Minute},
			want:   10 * time.Minute,
		},
		{
			name:   "等于 max_ttl → 原样",
			ttl:    10 * time.Minute,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 10 * time.Minute},
			want:   10 * time.Minute,
		},
		{
			name:   "max_ttl 未配置（0）→ 不截断",
			ttl:    48 * time.Hour,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second},
			want:   48 * time.Hour,
		},
		{
			name:   "区间内原样",
			ttl:    30 * time.Second,
			bounds: FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
			want:   30 * time.Second,
		},
		{
			name:       "default_ttl 未配置（0）→ 兜底为 0（不冷却）",
			ttl:        0,
			bounds:     FailoverCooldownTTLBounds{MaxTTL: 24 * time.Hour},
			useDefault: true,
			want:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeFailoverCooldownTTL(tt.ttl, tt.bounds, tt.useDefault); got != tt.want {
				t.Fatalf("NormalizeFailoverCooldownTTL(%v, %+v, %v) = %v, want %v",
					tt.ttl, tt.bounds, tt.useDefault, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// fail-open 降级计数（P5 指标数据源）
// ---------------------------------------------------------------------------

func TestFailoverCooldownDegradedCounter(t *testing.T) {
	counter := &FailoverCooldownDegradedCounter{}
	reasons := []FailoverCooldownDegradeReason{
		FailoverCooldownDegradeRead,
		FailoverCooldownDegradeWrite,
		FailoverCooldownDegradeClear,
		FailoverCooldownDegradeStreak,
		FailoverCooldownDegradeProbe,
	}

	var wg sync.WaitGroup
	for _, reason := range reasons {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(r FailoverCooldownDegradeReason) {
				defer wg.Done()
				counter.ReportFailoverCooldownDegraded(r)
			}(reason)
		}
	}
	// 未知原因落 other 桶，不丢总数。
	for i := 0; i < 7; i++ {
		counter.ReportFailoverCooldownDegraded("mystery")
	}
	wg.Wait()

	for _, reason := range reasons {
		if got := counter.Count(reason); got != 100 {
			t.Fatalf("%s 降级计数 = %d, want 100", reason, got)
		}
	}
	if got := counter.Count("mystery"); got != 7 {
		t.Fatalf("未知原因计数 = %d, want 7", got)
	}
	if got := counter.Total(); got != 507 {
		t.Fatalf("降级总数 = %d, want 507", got)
	}
}

// ---------------------------------------------------------------------------
// FailoverCooldownStore 契约（内存实现固化语义）
// ---------------------------------------------------------------------------

func TestFailoverCooldownStoreHitAndMiss(t *testing.T) {
	store := newFailoverCooldownMemoryStore(
		FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
		120*time.Second,
	)
	ctx := context.Background()

	if got := store.IsCooledBatch(ctx, []int64{1, 2}); len(got) != 0 {
		t.Fatalf("写入前应全部未命中: %v", got)
	}
	if res := store.Cool(ctx, FailoverCooldownWrite{AccountID: 1, Class: ClassAuthInvalid, TTL: 30 * time.Second}); !res.Applied {
		t.Fatalf("首写应生效: %+v", res)
	}

	cooled := store.IsCooledBatch(ctx, []int64{1, 2})
	if len(cooled) != 1 {
		t.Fatalf("应只有账号 1 命中: %v", cooled)
	}
	if entry := cooled[1]; entry.Class != ClassAuthInvalid {
		t.Fatalf("命中类别 = %q, want %q", entry.Class, ClassAuthInvalid)
	}

	store.advance(31 * time.Second)
	if got := store.IsCooledBatch(ctx, []int64{1}); len(got) != 0 {
		t.Fatalf("冷却到期后不应命中: %v", got)
	}
}

func TestFailoverCooldownStoreExtendOnly(t *testing.T) {
	store := newFailoverCooldownMemoryStore(
		FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
		120*time.Second,
	)
	ctx := context.Background()

	if res := store.Cool(ctx, FailoverCooldownWrite{AccountID: 1, Class: ClassUpstream5xx, TTL: 10 * time.Second}); !res.Applied {
		t.Fatalf("首写应生效: %+v", res)
	}
	shorter := store.Cool(ctx, FailoverCooldownWrite{AccountID: 1, Class: ClassAuthInvalid, TTL: 2 * time.Second})
	if shorter.Applied {
		t.Fatalf("短 TTL 不得缩短既有冷却: %+v", shorter)
	}
	if shorter.TTL != 10*time.Second {
		t.Fatalf("未写入时应返回既有剩余 TTL = %v, want 10s", shorter.TTL)
	}
	if entry := store.IsCooledBatch(ctx, []int64{1})[1]; entry.Class != ClassUpstream5xx {
		t.Fatalf("短 TTL 请求不应改写类别: %q", entry.Class)
	}

	longer := store.Cool(ctx, FailoverCooldownWrite{AccountID: 1, Class: ClassQuotaExhausted, TTL: 40 * time.Second})
	if !longer.Applied || longer.TTL != 40*time.Second {
		t.Fatalf("更长 TTL 应延长冷却: %+v", longer)
	}
	if entry := store.IsCooledBatch(ctx, []int64{1})[1]; entry.Class != ClassQuotaExhausted {
		t.Fatalf("延长后类别 = %q, want %q", entry.Class, ClassQuotaExhausted)
	}

	// 不冷却（TTL<=0 且不兜底）→ 不写。
	if res := store.Cool(ctx, FailoverCooldownWrite{AccountID: 2, Class: ClassCapacityShed, TTL: 0}); res.Applied {
		t.Fatalf("capacity_shed 不应写冷却: %+v", res)
	}
	if got := store.IsCooledBatch(ctx, []int64{2}); len(got) != 0 {
		t.Fatalf("不冷却的账号不应命中: %v", got)
	}
}

func TestFailoverCooldownStoreClearAndStreak(t *testing.T) {
	store := newFailoverCooldownMemoryStore(
		FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
		120*time.Second,
	)
	ctx := context.Background()

	store.Cool(ctx, FailoverCooldownWrite{AccountID: 1, Class: ClassUpstream5xx, TTL: 30 * time.Second})
	for i := 0; i < 3; i++ {
		if got := store.IncrFailureStreak(ctx, 1, 0); got != int64(i+1) {
			t.Fatalf("第 %d 次连续失败计数 = %d", i+1, got)
		}
	}
	if got := store.IncrFailureStreak(ctx, 2, 0); got != 1 {
		t.Fatalf("另一个账号应独立计数，实际 %d", got)
	}

	store.Clear(ctx, 1)
	if got := store.IsCooledBatch(ctx, []int64{1}); len(got) != 0 {
		t.Fatalf("清除后不应命中: %v", got)
	}
	if got := store.IncrFailureStreak(ctx, 1, 0); got != 1 {
		t.Fatalf("清除后连续失败计数应归零，实际 %d", got)
	}
}

func TestFailoverCooldownStoreProbeToken(t *testing.T) {
	store := newFailoverCooldownMemoryStore(
		FailoverCooldownTTLBounds{DefaultTTL: 60 * time.Second, MaxTTL: 24 * time.Hour},
		120*time.Second,
	)
	ctx := context.Background()

	if !store.AcquireProbeToken(ctx, 1) {
		t.Fatal("第一个探测应拿到令牌")
	}
	if store.AcquireProbeToken(ctx, 1) {
		t.Fatal("令牌未过期前只应放行 1 个")
	}
	store.advance(11 * time.Second)
	if !store.AcquireProbeToken(ctx, 1) {
		t.Fatal("令牌过期后应能重新获取")
	}
	store.ClearProbeToken(ctx, 1)
	if !store.AcquireProbeToken(ctx, 1) {
		t.Fatal("主动清除令牌后应能立即重新获取")
	}
}
