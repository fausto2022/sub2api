package repository

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// 本文件覆盖 T4.1 的 Redis 冷却存储（design.md §3.5 / §6.2）。
//
// 用 miniredis（已在 go.mod）做真实 Redis 语义测试：Lua、PX/TTL、SETNX、过期都用
// 真实命令路径，而不是 mock ——「INCR 后必有 TTL」「extend-only 不缩短」这类结论只有
// 跑真命令才有意义。

// failoverCooldownTestConfig 是测试用的冷却配置（数值与 config 默认值一致，见 config.go）。
func failoverCooldownTestConfig() config.FailoverCooldownConfig {
	return config.FailoverCooldownConfig{
		Enabled:                      true,
		ConsecutiveFailuresThreshold: 3,
		StreakWindow:                 120 * time.Second,
		DefaultTTL:                   60 * time.Second,
		MaxTTL:                       24 * time.Hour,
		PoolModeEnabled:              true,
		ProbeEnabled:                 true,
		ProbeMaxAttempts:             3,
		RedisTimeout:                 500 * time.Millisecond,
		FailOpen:                     true,
		BatchReadMax:                 200,
	}
}

// newFailoverCooldownCacheForTest 构造一个接 miniredis 的冷却存储 + 降级计数器。
func newFailoverCooldownCacheForTest(
	t *testing.T,
	mutate func(cfg *config.FailoverCooldownConfig),
	hooks ...redis.Hook,
) (service.FailoverCooldownStore, *miniredis.Miniredis, *service.FailoverCooldownDegradedCounter) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	for _, hook := range hooks {
		client.AddHook(hook)
	}
	t.Cleanup(func() { _ = client.Close() })

	cfg := failoverCooldownTestConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	counter := &service.FailoverCooldownDegradedCounter{}
	return NewFailoverCooldownCache(client, cfg, counter), server, counter
}

// failoverCooldownCommandRecorder 记录发往 Redis 的命令名与次数（用于断言「MGET 一次往返」）。
type failoverCooldownCommandRecorder struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFailoverCooldownCommandRecorder() *failoverCooldownCommandRecorder {
	return &failoverCooldownCommandRecorder{counts: make(map[string]int)}
}

func (r *failoverCooldownCommandRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *failoverCooldownCommandRecorder) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.record(cmd)
		return next(ctx, cmd)
	}
}

func (r *failoverCooldownCommandRecorder) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			r.record(cmd)
		}
		return next(ctx, cmds)
	}
}

func (r *failoverCooldownCommandRecorder) record(cmd redis.Cmder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[cmd.Name()]++
}

func (r *failoverCooldownCommandRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[name]
}

func (r *failoverCooldownCommandRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts = make(map[string]int)
}

// TestFailoverCooldownCacheCoolHitAndMiss 覆盖命中/未命中 + value 编解码。
func TestFailoverCooldownCacheCoolHitAndMiss(t *testing.T) {
	store, server, counter := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()

	if got := store.IsCooledBatch(ctx, nil); len(got) != 0 {
		t.Fatalf("空候选集不应产生任何命中: %v", got)
	}
	if got := store.IsCooledBatch(ctx, []int64{101, 102}); len(got) != 0 {
		t.Fatalf("写入前应为未命中（fail-open 基线）: %v", got)
	}

	written := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: 101,
		Class:     service.ClassAuthInvalid,
		TTL:       30 * time.Second,
	})
	if !written.Applied || written.Degraded {
		t.Fatalf("首次写入应生效: %+v", written)
	}
	if written.TTL != 30*time.Second {
		t.Fatalf("写入生效 TTL = %v, want 30s", written.TTL)
	}
	if !server.Exists(failoverCooldownAccountKey(101)) {
		t.Fatal("冷却 key 未落盘")
	}
	if ttl := server.TTL(failoverCooldownAccountKey(101)); ttl != 30*time.Second {
		t.Fatalf("key TTL = %v, want 30s", ttl)
	}

	cooled := store.IsCooledBatch(ctx, []int64{101, 102})
	if len(cooled) != 1 {
		t.Fatalf("应只有 101 命中: %v", cooled)
	}
	entry, ok := cooled[101]
	if !ok {
		t.Fatalf("101 应命中: %v", cooled)
	}
	if entry.Class != service.ClassAuthInvalid {
		t.Fatalf("命中类别 = %q, want %q", entry.Class, service.ClassAuthInvalid)
	}
	if entry.Deadline.IsZero() {
		t.Fatal("命中条目应带 deadline（Redis TIME 写入的 value 段）")
	}
	if counter.Total() != 0 {
		t.Fatalf("正常路径不应有降级计数: %v", counter.Total())
	}

	// 冷却自然到期后不再命中（TTL 由 Redis 掌握，客户端不算 until-now）。
	server.FastForward(30 * time.Second)
	if got := store.IsCooledBatch(ctx, []int64{101}); len(got) != 0 {
		t.Fatalf("冷却到期后不应命中: %v", got)
	}
}

// TestFailoverCooldownCacheExtendOnly 覆盖 extend-only：短 TTL 不得缩短既有冷却。
func TestFailoverCooldownCacheExtendOnly(t *testing.T) {
	store, server, _ := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()
	const accountID = 201
	key := failoverCooldownAccountKey(accountID)

	if res := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: accountID,
		Class:     service.ClassUpstream5xx,
		TTL:       10 * time.Second,
	}); !res.Applied {
		t.Fatalf("首写应生效: %+v", res)
	}

	// 更短的 TTL：不写入、不缩短。
	shorter := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: accountID,
		Class:     service.ClassAuthInvalid,
		TTL:       2 * time.Second,
	})
	if shorter.Applied {
		t.Fatalf("短 TTL 不应覆盖长冷却: %+v", shorter)
	}
	if shorter.TTL != 10*time.Second {
		t.Fatalf("未写入时应返回既有剩余 TTL = %v, want 10s", shorter.TTL)
	}
	if ttl := server.TTL(key); ttl != 10*time.Second {
		t.Fatalf("既有冷却被缩短了: TTL = %v, want 10s", ttl)
	}
	if value, err := server.Get(key); err != nil {
		t.Fatalf("读取冷却 value 失败: %v", err)
	} else if class, _ := parseFailoverCooldownValue(value); class != service.ClassUpstream5xx {
		t.Fatalf("短 TTL 请求不应改写 class: %q", class)
	}

	// 更长的 TTL：延长并改写 class。
	longer := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: accountID,
		Class:     service.ClassQuotaExhausted,
		TTL:       45 * time.Second,
	})
	if !longer.Applied || longer.TTL != 45*time.Second {
		t.Fatalf("更长 TTL 应延长冷却: %+v", longer)
	}
	if ttl := server.TTL(key); ttl != 45*time.Second {
		t.Fatalf("延长后 TTL = %v, want 45s", ttl)
	}
	if value, err := server.Get(key); err != nil {
		t.Fatalf("读取冷却 value 失败: %v", err)
	} else if class, _ := parseFailoverCooldownValue(value); class != service.ClassQuotaExhausted {
		t.Fatalf("延长后 class = %q, want %q", class, service.ClassQuotaExhausted)
	}
}

// TestFailoverCooldownCacheTTLBounds 覆盖 TTL 边界：0 / 负值 / 超 max 截断 / default 兜底。
func TestFailoverCooldownCacheTTLBounds(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(cfg *config.FailoverCooldownConfig)
		ttl           time.Duration
		useDefault    bool
		wantApplied   bool
		wantTTL       time.Duration
		wantKeyExists bool
	}{
		{
			name:          "显式 TTL 原样生效",
			ttl:           30 * time.Second,
			wantApplied:   true,
			wantTTL:       30 * time.Second,
			wantKeyExists: true,
		},
		{
			name:          "TTL=0 且不兜底 → 不冷却（不写 key、零 Redis 写）",
			ttl:           0,
			wantApplied:   false,
			wantTTL:       0,
			wantKeyExists: false,
		},
		{
			name:          "TTL=0 且要求兜底 → 配置 default_ttl",
			ttl:           0,
			useDefault:    true,
			wantApplied:   true,
			wantTTL:       60 * time.Second,
			wantKeyExists: true,
		},
		{
			name:          "负 TTL 且要求兜底 → 配置 default_ttl",
			ttl:           -5 * time.Second,
			useDefault:    true,
			wantApplied:   true,
			wantTTL:       60 * time.Second,
			wantKeyExists: true,
		},
		{
			name:          "负 TTL 且不兜底 → 不冷却",
			ttl:           -5 * time.Second,
			wantApplied:   false,
			wantTTL:       0,
			wantKeyExists: false,
		},
		{
			name:          "超过 max_ttl → 截断到配置 max_ttl",
			ttl:           48 * time.Hour,
			wantApplied:   true,
			wantTTL:       24 * time.Hour,
			wantKeyExists: true,
		},
		{
			name: "边界值全部来自配置（default=5s / max=90s）",
			mutate: func(cfg *config.FailoverCooldownConfig) {
				cfg.DefaultTTL = 5 * time.Second
				cfg.MaxTTL = 90 * time.Second
			},
			ttl:           0,
			useDefault:    true,
			wantApplied:   true,
			wantTTL:       5 * time.Second,
			wantKeyExists: true,
		},
		{
			name: "超上限按配置 max 截断（default=5s / max=90s）",
			mutate: func(cfg *config.FailoverCooldownConfig) {
				cfg.DefaultTTL = 5 * time.Second
				cfg.MaxTTL = 90 * time.Second
			},
			ttl:           10 * time.Minute,
			wantApplied:   true,
			wantTTL:       90 * time.Second,
			wantKeyExists: true,
		},
		{
			name:          "毫秒级 TTL 被 Lua 抬到最小 1s（PX 不接受 0）",
			ttl:           time.Millisecond,
			wantApplied:   true,
			wantTTL:       time.Second,
			wantKeyExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, server, counter := newFailoverCooldownCacheForTest(t, tt.mutate)
			key := failoverCooldownAccountKey(301)

			res := store.Cool(context.Background(), service.FailoverCooldownWrite{
				AccountID:     301,
				Class:         service.ClassRateLimited,
				TTL:           tt.ttl,
				UseDefaultTTL: tt.useDefault,
			})
			if res.Applied != tt.wantApplied {
				t.Fatalf("Applied = %v, want %v (%+v)", res.Applied, tt.wantApplied, res)
			}
			if res.Degraded {
				t.Fatalf("正常配置不应降级: %+v", res)
			}
			if res.TTL != tt.wantTTL {
				t.Fatalf("TTL = %v, want %v", res.TTL, tt.wantTTL)
			}
			if got := server.Exists(key); got != tt.wantKeyExists {
				t.Fatalf("key 存在 = %v, want %v", got, tt.wantKeyExists)
			}
			if tt.wantKeyExists {
				if ttl := server.TTL(key); ttl != tt.wantTTL {
					t.Fatalf("Redis TTL = %v, want %v", ttl, tt.wantTTL)
				}
			}
			if counter.Total() != 0 {
				t.Fatalf("不应有降级计数: %v", counter.Total())
			}
		})
	}
}

// TestFailoverCooldownCacheBatchReadUsesOneMGetPerChunk 覆盖批量读「每片一次 MGET、零 GET」。
func TestFailoverCooldownCacheBatchReadUsesOneMGetPerChunk(t *testing.T) {
	recorder := newFailoverCooldownCommandRecorder()
	store, _, counter := newFailoverCooldownCacheForTest(t, func(cfg *config.FailoverCooldownConfig) {
		cfg.BatchReadMax = 20
	}, recorder)

	ids := make([]int64, 45)
	for i := range ids {
		ids[i] = int64(1000 + i)
	}

	if cooled := store.IsCooledBatch(context.Background(), ids); len(cooled) != 0 {
		t.Fatalf("无冷却时不应命中: %v", cooled)
	}
	if got := recorder.count("mget"); got != 3 {
		t.Fatalf("45 个候选 / batch_read_max=20 应为 3 次 MGET（一次往返一片），实际 %d", got)
	}
	if got := recorder.count("get"); got != 0 {
		t.Fatalf("热路径禁止逐个 GET（N 候选 = N RTT），实际 %d 次", got)
	}
	if counter.Total() != 0 {
		t.Fatalf("不应有降级: %v", counter.Total())
	}

	// 单分片足以覆盖全部候选时只有一次 MGET。
	recorder.reset()
	store, _, _ = newFailoverCooldownCacheForTest(t, func(cfg *config.FailoverCooldownConfig) {
		cfg.BatchReadMax = 200
	}, recorder)
	if cooled := store.IsCooledBatch(context.Background(), ids); len(cooled) != 0 {
		t.Fatalf("无冷却时不应命中: %v", cooled)
	}
	if got := recorder.count("mget"); got != 1 {
		t.Fatalf("batch_read_max=200 覆盖 45 个候选应只 1 次 MGET，实际 %d", got)
	}
}

// TestFailoverCooldownCacheFailOpenOnRedisError 覆盖 fail-open：Redis 不可用时错误不上抛。
func TestFailoverCooldownCacheFailOpenOnRedisError(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1", // 必然连接失败
		MaxRetries:   -1,
		DialTimeout:  30 * time.Millisecond,
		ReadTimeout:  30 * time.Millisecond,
		WriteTimeout: 30 * time.Millisecond,
	})
	t.Cleanup(func() { _ = client.Close() })

	cfg := failoverCooldownTestConfig()
	cfg.RedisTimeout = 200 * time.Millisecond
	counter := &service.FailoverCooldownDegradedCounter{}
	store := NewFailoverCooldownCache(client, cfg, counter)
	ctx := context.Background()

	if cooled := store.IsCooledBatch(ctx, []int64{1, 2, 3}); len(cooled) != 0 {
		t.Fatalf("Redis 挂掉必须 fail-open（不排除任何账号），实际 %v", cooled)
	}
	write := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: 1,
		Class:     service.ClassAuthInvalid,
		TTL:       time.Minute,
	})
	if write.Applied || !write.Degraded {
		t.Fatalf("Redis 挂掉时写冷却应降级而非生效: %+v", write)
	}
	store.Clear(ctx, 1)
	if got := store.IncrFailureStreak(ctx, 1, 0); got != 0 {
		t.Fatalf("计数失败应返回 0（不冷却），实际 %d", got)
	}
	if store.AcquireProbeToken(ctx, 1) {
		t.Fatal("Redis 挂掉时不应放行探测")
	}
	store.ClearProbeToken(ctx, 1)

	if got := counter.Count(service.FailoverCooldownDegradeRead); got != 1 {
		t.Fatalf("read 降级计数 = %d, want 1", got)
	}
	if got := counter.Count(service.FailoverCooldownDegradeWrite); got != 1 {
		t.Fatalf("write 降级计数 = %d, want 1", got)
	}
	if got := counter.Count(service.FailoverCooldownDegradeClear); got != 1 {
		t.Fatalf("clear 降级计数 = %d, want 1", got)
	}
	if got := counter.Count(service.FailoverCooldownDegradeStreak); got != 1 {
		t.Fatalf("streak 降级计数 = %d, want 1", got)
	}
	if got := counter.Count(service.FailoverCooldownDegradeProbe); got != 2 {
		t.Fatalf("probe 降级计数 = %d, want 2", got)
	}
	if got := counter.Total(); got != 6 {
		t.Fatalf("降级总数 = %d, want 6", got)
	}
}

// TestFailoverCooldownCacheContextCanceledFailsOpen 覆盖调用方 ctx 取消时的 fail-open。
//
// 与超时走同一条代码路径（operationContext 派生的子 ctx 先被取消 → Redis 返回错误），
// 但用「已取消的 ctx」而不是「1ns 超时」断言，避免依赖本地 Redis 的往返竞速导致 flake。
func TestFailoverCooldownCacheContextCanceledFailsOpen(t *testing.T) {
	store, _, counter := newFailoverCooldownCacheForTest(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if cooled := store.IsCooledBatch(ctx, []int64{1, 2}); len(cooled) != 0 {
		t.Fatalf("ctx 取消必须 fail-open（不排除任何账号），实际 %v", cooled)
	}
	if got := counter.Count(service.FailoverCooldownDegradeRead); got != 1 {
		t.Fatalf("ctx 取消应计入 read 降级 1 次，实际 %d", got)
	}
	if write := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: 1,
		Class:     service.ClassAuthInvalid,
		TTL:       time.Minute,
	}); !write.Degraded {
		t.Fatalf("ctx 取消时写冷却应降级: %+v", write)
	}
}

// TestFailoverCooldownCacheOperationContextUsesConfiguredTimeout 证明
// `cooldown.redis_timeout` 真的被应用到每次 Redis 操作（不是写死常量）。
func TestFailoverCooldownCacheOperationContextUsesConfiguredTimeout(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "配置 500ms", timeout: 500 * time.Millisecond, wantDeadline: true},
		{name: "未配置（0）→ 不加额外超时", timeout: 0, wantDeadline: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _ := newFailoverCooldownCacheForTest(t, func(cfg *config.FailoverCooldownConfig) {
				cfg.RedisTimeout = tt.timeout
			})
			cache, ok := store.(*failoverCooldownCache)
			if !ok {
				t.Fatalf("构造返回类型 = %T, want *failoverCooldownCache", store)
			}
			ctx, cancel := cache.operationContext(context.Background())
			defer cancel()

			deadline, hasDeadline := ctx.Deadline()
			if hasDeadline != tt.wantDeadline {
				t.Fatalf("deadline 存在 = %v, want %v", hasDeadline, tt.wantDeadline)
			}
			if tt.wantDeadline {
				remaining := time.Until(deadline)
				if remaining <= 0 || remaining > tt.timeout {
					t.Fatalf("剩余超时 = %v, want (0, %v]", remaining, tt.timeout)
				}
			}
		})
	}
}

// TestFailoverCooldownCacheNilRedisFailsOpen 覆盖接线漏配（nil client）也 fail-open。
func TestFailoverCooldownCacheNilRedisFailsOpen(t *testing.T) {
	counter := &service.FailoverCooldownDegradedCounter{}
	store := NewFailoverCooldownCache(nil, failoverCooldownTestConfig(), counter)
	ctx := context.Background()

	if cooled := store.IsCooledBatch(ctx, []int64{1}); len(cooled) != 0 {
		t.Fatalf("nil client 应 fail-open，实际 %v", cooled)
	}
	if write := store.Cool(ctx, service.FailoverCooldownWrite{AccountID: 1, TTL: time.Minute}); !write.Degraded {
		t.Fatalf("nil client 写冷却应降级: %+v", write)
	}
	if store.AcquireProbeToken(ctx, 1) {
		t.Fatal("nil client 不应放行探测")
	}
	if counter.Total() == 0 {
		t.Fatal("nil client 应计入降级")
	}
}

// TestFailoverCooldownCacheFailureStreak 覆盖 INCR+PEXPIRE 原子性（30 次并发后 PTTL > 0）。
func TestFailoverCooldownCacheFailureStreak(t *testing.T) {
	store, server, counter := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()
	const accountID = 401
	key := failoverFailureStreakKey(accountID)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := store.IncrFailureStreak(ctx, accountID, 0); got <= 0 {
				t.Errorf("并发自增失败（fail-open 返回 0）: %d", got)
			}
		}()
	}
	wg.Wait()

	value, err := server.Get(key)
	if err != nil {
		t.Fatalf("streak key 不存在: %v", err)
	}
	if value != "30" {
		t.Fatalf("streak = %s, want 30", value)
	}
	if ttl := server.TTL(key); ttl <= 0 {
		t.Fatalf("streak key 必须带 TTL，否则账号永久冷却（严重事故）: %v", ttl)
	}
	if counter.Total() != 0 {
		t.Fatalf("正常路径不应降级: %v", counter.Total())
	}

	// 窗口过期后重新从 1 开始。
	server.FastForward(120 * time.Second)
	if server.Exists(key) {
		t.Fatal("streak 窗口过期后 key 应消失")
	}
	if got := store.IncrFailureStreak(ctx, accountID, 0); got != 1 {
		t.Fatalf("窗口过期后应重新计数为 1，实际 %d", got)
	}

	// 分组覆盖的窗口（显式参数）优先于配置 streak_window。
	if got := store.IncrFailureStreak(ctx, 402, 5*time.Second); got != 1 {
		t.Fatalf("自定义窗口计数 = %d, want 1", got)
	}
	if ttl := server.TTL(failoverFailureStreakKey(402)); ttl != 5*time.Second {
		t.Fatalf("自定义窗口 TTL = %v, want 5s", ttl)
	}

	// 窗口未配置（配置与参数都为 0）→ 不计数（fail-open，避免永久 key）。
	storeWithoutWindow, _, _ := newFailoverCooldownCacheForTest(t, func(cfg *config.FailoverCooldownConfig) {
		cfg.StreakWindow = 0
	})
	if got := storeWithoutWindow.IncrFailureStreak(ctx, 403, 0); got != 0 {
		t.Fatalf("窗口未配置时不应计数，实际 %d", got)
	}
}

// TestFailoverCooldownCacheProbeToken 覆盖半开探测令牌（SET NX EX 10）。
func TestFailoverCooldownCacheProbeToken(t *testing.T) {
	store, server, counter := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()
	const accountID = 501

	if !store.AcquireProbeToken(ctx, accountID) {
		t.Fatal("冷却到期后的第一个探测应拿到令牌")
	}
	if store.AcquireProbeToken(ctx, accountID) {
		t.Fatal("探测令牌未过期前只应放行 1 个（防惊群）")
	}
	if ttl := server.TTL(failoverProbeTokenKey(accountID)); ttl <= 0 || ttl > failoverCooldownProbeTokenTTL {
		t.Fatalf("探测令牌 TTL = %v, want (0, %v]", ttl, failoverCooldownProbeTokenTTL)
	}

	server.FastForward(failoverCooldownProbeTokenTTL)
	if !store.AcquireProbeToken(ctx, accountID) {
		t.Fatal("令牌到期后应能重新获取")
	}

	store.ClearProbeToken(ctx, accountID)
	if !store.AcquireProbeToken(ctx, accountID) {
		t.Fatal("主动清除令牌后应能立即重新获取")
	}
	if counter.Total() != 0 {
		t.Fatalf("正常路径不应降级: %v", counter.Total())
	}
}

// TestFailoverCooldownCacheModelScope 覆盖账号×模型级 key（modelHash = sha256[:16]）。
func TestFailoverCooldownCacheModelScope(t *testing.T) {
	store, server, _ := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()
	const accountID = 601

	written := store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: accountID,
		Model:     "claude-sonnet-4-5",
		Class:     service.ClassRateLimited,
		TTL:       30 * time.Second,
	})
	if !written.Applied {
		t.Fatalf("模型级写入应生效: %+v", written)
	}

	if got := store.IsCooledBatch(ctx, []int64{accountID}); len(got) != 0 {
		t.Fatalf("模型级冷却不应落在账号级 key 上: %v", got)
	}
	modelCooled := store.IsModelCooledBatch(ctx, []int64{accountID, 999}, "claude-sonnet-4-5")
	if len(modelCooled) != 1 {
		t.Fatalf("同模型应命中且只命中该账号: %v", modelCooled)
	}
	if entry := modelCooled[accountID]; entry.Class != service.ClassRateLimited {
		t.Fatalf("模型级命中类别 = %q, want %q", entry.Class, service.ClassRateLimited)
	}
	if got := store.IsModelCooledBatch(ctx, []int64{accountID}, "gpt-5"); len(got) != 0 {
		t.Fatalf("不同模型不应命中: %v", got)
	}
	if got := store.IsModelCooledBatch(ctx, []int64{accountID}, "  "); len(got) != 0 {
		t.Fatalf("空 model 不应命中（也不回落账号级）: %v", got)
	}

	hash := failoverCooldownModelHash("claude-sonnet-4-5")
	if len(hash) != failoverCooldownModelHashLength {
		t.Fatalf("modelHash 长度 = %d, want %d", len(hash), failoverCooldownModelHashLength)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(hash) {
		t.Fatalf("modelHash 应为 16 位小写十六进制: %q", hash)
	}
	wantKey := "failover:cooldown:acct:601:" + hash
	if !server.Exists(wantKey) {
		t.Fatalf("模型级 key 名不符合 design §6.2，实际 keys=%v", server.Keys())
	}
}

// TestFailoverCooldownCacheClear 覆盖清除（冷却 + 连续失败计数）。
func TestFailoverCooldownCacheClear(t *testing.T) {
	store, server, counter := newFailoverCooldownCacheForTest(t, nil)
	ctx := context.Background()
	const accountID = 701

	store.Cool(ctx, service.FailoverCooldownWrite{
		AccountID: accountID,
		Class:     service.ClassAuthInvalid,
		TTL:       time.Minute,
	})
	store.IncrFailureStreak(ctx, accountID, 0)

	if !server.Exists(failoverCooldownAccountKey(accountID)) || !server.Exists(failoverFailureStreakKey(accountID)) {
		t.Fatal("前置条件：冷却与计数都应存在")
	}

	store.Clear(ctx, accountID)

	if got := store.IsCooledBatch(ctx, []int64{accountID}); len(got) != 0 {
		t.Fatalf("清除后不应命中: %v", got)
	}
	if server.Exists(failoverFailureStreakKey(accountID)) {
		t.Fatal("清除后连续失败计数应一并删除")
	}
	if counter.Total() != 0 {
		t.Fatalf("正常路径不应降级: %v", counter.Total())
	}
}

// TestFailoverCooldownCacheValueParsing 覆盖 value 编解码的容错（脏数据不应 panic/丢冷却）。
func TestFailoverCooldownCacheValueParsing(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		wantClass    service.FailureClass
		wantDeadline bool
	}{
		{name: "标准格式", value: "auth_invalid|1760000000000", wantClass: service.ClassAuthInvalid, wantDeadline: true},
		{name: "无分隔符按裸 class 处理", value: "upstream_5xx", wantClass: service.ClassUpstream5xx},
		{name: "deadline 非法仍算冷却", value: "rate_limited|not-a-number", wantClass: service.ClassRateLimited},
		{name: "deadline 非正仍算冷却", value: "network|0", wantClass: service.ClassNetwork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, deadline := parseFailoverCooldownValue(tt.value)
			if class != tt.wantClass {
				t.Fatalf("class = %q, want %q", class, tt.wantClass)
			}
			if gotDeadline := !deadline.IsZero(); gotDeadline != tt.wantDeadline {
				t.Fatalf("deadline = %v, wantDeadline = %v", deadline, tt.wantDeadline)
			}
		})
	}
}
