package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// T2 跨请求冷却层的 Redis 实现（design.md §3.5 / §6.2，implementation.md T4.1）
// ---------------------------------------------------------------------------
//
// 为什么实现在 repository：`backend/.golangci.yml` 的 `service-no-repository` 规则禁止
// service / handler import redis（先例：ConcurrencyCache 的接口在 service、实现在本包）。
//
// 三段原子语义（design §3.5.3，全部用 Lua/单命令，禁止「先读后写」的两次往返）：
//  1. 写冷却：PTTL 比较 + SET PX 必须在同一脚本 —— 朴素 `SET key v EX ttl` 会用短 TTL
//     覆盖长 TTL（G10 的 Redis 版），缩短冷却 = 坏账号过早恢复。
//  2. 连续失败计数：INCR 与首个 PEXPIRE 必须同一脚本 —— 否则崩溃/超时会留下**永久 key**
//     → 账号永久冷却（严重事故）。
//  3. 半开探测令牌：`SET key 1 NX EX 10` 单命令天然原子，不需要 Lua。
//
// fail-open（C-02）：本类型的所有方法都**不向调用方返回 error**；Redis 故障时按「未冷却」
// 处理，并递增 service.FailoverCooldownDegradedCounter（P5 的
// `failover_cooldown_degraded_total`）。日志按 5s 去抖，计数不去抖。

const (
	// 账号级冷却 key（design §6.2）：failover:cooldown:acct:{accountID}
	failoverCooldownAccountKeyPrefix = "failover:cooldown:acct:"
	// 连续失败计数 key：failover:streak:acct:{accountID}
	failoverFailureStreakKeyPrefix = "failover:streak:acct:"
	// 半开探测令牌 key：failover:probe:acct:{accountID}
	failoverProbeTokenKeyPrefix = "failover:probe:acct:"

	// failoverCooldownProbeTokenTTL 探测令牌时长：design §3.4.4 规定「每 10s 最多放行 1 个请求」。
	// 这是探测节流常量（不是冷却时长），不来自 cooldown.* 配置。
	failoverCooldownProbeTokenTTL = 10 * time.Second

	// failoverCooldownMinTTLMS Lua 侧兜底：PX 不接受 <=0 的毫秒值，至少给 1s
	// （design §3.5.3 草图的 `math.max(..., 1000)`）。真正的时长边界来自配置的
	// default_ttl / max_ttl，见 service.NormalizeFailoverCooldownTTL。
	failoverCooldownMinTTLMS int64 = 1000

	// failoverCooldownValueSeparator Redis value 格式：`{class}|{deadlineMs}`（design §6.2）。
	failoverCooldownValueSeparator = "|"

	// failoverCooldownDegradeLogInterval 降级日志去抖间隔（计数不去抖）。
	failoverCooldownDegradeLogInterval = 5 * time.Second

	// failoverCooldownModelHashLength modelHash 取 sha256 十六进制前 16 字符（design §6.2）。
	failoverCooldownModelHashLength = 16
)

var (
	// failoverFailureStreakScript 连续失败计数：INCR 与首个 PEXPIRE 原子（design §3.5.3 问题 2）。
	//
	// KEYS[1] = streak key
	// ARGV[1] = 计数窗口（毫秒）
	// 返回自增后的计数。
	//
	// 额外守卫：PTTL < 0（无 TTL 的历史 key）也补设 TTL，避免任何路径留下永久计数键。
	failoverFailureStreakScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)
)

// The marker outlives the cooling key for a bounded discovery interval. An
// acquisition near its expiration gives the marker enough time for that lease.
const failoverRecoveryGrace = time.Minute
const failoverRecoveryDebounce = time.Second

// Recovery keys per scope: cooldown, recovery marker, lease and write debounce.
// Redis TIME is only used to produce the existing observability value.
const recoveryLua = `
local function writeCooldown(key, class, ttl)
  local time = redis.call('TIME')
  local deadline = tonumber(time[1]) * 1000 + math.floor(tonumber(time[2]) / 1000) + ttl
  redis.call('SET', key, class .. '|' .. tostring(deadline), 'PX', ttl)
end
local function readRecovery(key)
  local ttl = redis.call('PTTL', key)
  if ttl < 0 then return nil end
  local marker = redis.call('GET', key)
  if not marker then return nil end
  local class, base, failed = string.match(marker, '^([^|]+)|(%d+)|(%d+)$')
  if not class or tonumber(base) <= 0 then return nil end
  return class, tonumber(base), tonumber(failed), ttl
end
`

var recoveryWriteScript = redis.NewScript(recoveryLua + `
local ttl = math.max(tonumber(ARGV[1]), tonumber(ARGV[3]))
local cur = redis.call('PTTL', KEYS[1])
if cur > ttl then return {0, cur} end
local window = tonumber(ARGV[4])
if window > 0 then
  if redis.call('PTTL', KEYS[4]) == -1 then redis.call('DEL', KEYS[4]) end
  if not redis.call('SET', KEYS[4], '1', 'PX', window, 'NX') then
    return {0, math.max(cur, 0)}
  end
end
redis.call('DEL', KEYS[2], KEYS[3])
writeCooldown(KEYS[1], ARGV[2], ttl)
local grace = tonumber(ARGV[5])
if grace > 0 and ARGV[2] ~= '' then
  redis.call('SET', KEYS[2], ARGV[2] .. '|' .. ttl .. '|0', 'PX', ttl + grace)
end
return {1, ttl}
`)

// A flat tuple per scope keeps Redis reply shape stable for misses and allows
// the caller to discard every chunk on failure.
var recoveryReadScript = redis.NewScript(recoveryLua + `
local result = {}
for i = 1, #KEYS, 2 do
  local value = false
  local pending = 0
  if redis.call('PTTL', KEYS[i]) >= 0 then
    value = redis.call('GET', KEYS[i])
  elseif readRecovery(KEYS[i + 1]) then
    pending = 1
  end
  result[#result + 1] = value
  result[#result + 1] = pending
end
return result
`)

var recoveryAcquireScript = redis.NewScript(recoveryLua + `
if redis.call('PTTL', KEYS[1]) >= 0 then return {'cooling'} end
local class, base, failed, markerTTL = readRecovery(KEYS[2])
if not class then return {'not_required'} end
local leaseTTL = tonumber(ARGV[2])
if redis.call('PTTL', KEYS[3]) == -1 then redis.call('DEL', KEYS[3]) end
if not redis.call('SET', KEYS[3], ARGV[1], 'PX', leaseTTL, 'NX') then return {'busy'} end
if markerTTL < leaseTTL then redis.call('PEXPIRE', KEYS[2], leaseTTL) end
return {'acquired', base, failed}
`)

// The current lease must still exist, and a newer cooldown takes precedence.
// ARGV contains the validated service policy plan for this lease.
var recoveryCompleteScript = redis.NewScript(recoveryLua + `
if redis.call('PTTL', KEYS[3]) < 0 or redis.call('GET', KEYS[3]) ~= ARGV[1] then return 0 end
if redis.call('PTTL', KEYS[1]) >= 0 then return 0 end
local class, base, failed = readRecovery(KEYS[2])
if not class then return 0 end
local action = ARGV[2]
if action == 'release' then
  redis.call('DEL', KEYS[3])
  return 1
end
if failed ~= tonumber(ARGV[3]) then return 0 end
if action == 'clear' then
  redis.call('DEL', unpack(KEYS))
  return 1
end
local ttl = tonumber(ARGV[4])
writeCooldown(KEYS[1], class, ttl)
redis.call('SET', KEYS[2], class .. '|' .. base .. '|' .. (failed + 1), 'PX', ttl + tonumber(ARGV[5]))
redis.call('DEL', KEYS[3])
return 1
`)

// failoverCooldownCache implements both the legacy and the recovery store.
type failoverCooldownCache struct {
	rdb          *redis.Client
	bounds       service.FailoverCooldownTTLBounds
	streakWindow time.Duration
	batchReadMax int
	redisTimeout time.Duration
	reporter     service.FailoverCooldownDegradeReporter

	// lastDegradeLogUnixNano 降级日志去抖时间戳（计数不去抖）。
	lastDegradeLogUnixNano atomic.Int64
}

// NewFailoverCooldownCache 构造 T2 冷却存储。
//
// cfg 只提供**资源与边界**参数（default_ttl / max_ttl / streak_window / batch_read_max /
// redis_timeout）；是否启用冷却（cooldown.enabled）不在这里判断——它按生效策略分组可覆盖，
// 门控属于调用方（见 service.FailoverCooldownStore 的注释）。
//
// reporter 为 nil 时降级只打日志，不计数（便于单测与最小化接线）。
func NewFailoverCooldownCache(
	rdb *redis.Client,
	cfg config.FailoverCooldownConfig,
	reporter service.FailoverCooldownDegradeReporter,
) service.FailoverCooldownStore {
	return &failoverCooldownCache{
		rdb:          rdb,
		bounds:       service.FailoverCooldownTTLBoundsFromConfig(cfg),
		streakWindow: cfg.StreakWindow,
		batchReadMax: cfg.BatchReadMax,
		redisTimeout: cfg.RedisTimeout,
		reporter:     reporter,
	}
}

// IsCooledBatch 批量判定账号级冷却：一次 MGET 拿全量（design §3.5.3 问题 4）。
func (c *failoverCooldownCache) IsCooledBatch(ctx context.Context, accountIDs []int64) map[int64]service.FailoverCooldownEntry {
	return c.isCooledBatch(ctx, accountIDs, failoverCooldownAccountKey)
}

// IsModelCooledBatch 批量判定账号×模型级冷却（design §6.2 的 {modelHash} key）。
func (c *failoverCooldownCache) IsModelCooledBatch(ctx context.Context, accountIDs []int64, model string) map[int64]service.FailoverCooldownEntry {
	model = strings.TrimSpace(model)
	if model == "" {
		// 空 model 不是模型级请求；账号级由 IsCooledBatch 负责，这里**不**回落。
		return map[int64]service.FailoverCooldownEntry{}
	}
	modelHash := failoverCooldownModelHash(model)
	return c.isCooledBatch(ctx, accountIDs, func(accountID int64) string {
		return failoverCooldownModelKey(accountID, modelHash)
	})
}

// Cool preserves the legacy immediate extend-only behavior.
func (c *failoverCooldownCache) Cool(ctx context.Context, write service.FailoverCooldownWrite) service.FailoverCooldownWriteResult {
	return c.writeCooldown(ctx, write, 0, 0)
}

// CoolWithRecovery debounces production isolation writes per scope.
func (c *failoverCooldownCache) CoolWithRecovery(ctx context.Context, write service.FailoverCooldownWrite, probeEnabled bool) service.FailoverCooldownWriteResult {
	var grace time.Duration
	if probeEnabled {
		grace = failoverRecoveryGrace
	}
	return c.writeCooldown(ctx, write, failoverRecoveryDebounce, grace)
}

func (c *failoverCooldownCache) writeCooldown(ctx context.Context, write service.FailoverCooldownWrite, window, grace time.Duration) service.FailoverCooldownWriteResult {
	if write.AccountID <= 0 {
		return service.FailoverCooldownWriteResult{}
	}
	ttl := service.NormalizeFailoverCooldownTTL(write.TTL, c.bounds, write.UseDefaultTTL)
	if ttl <= 0 {
		// 不冷却的类别（capacity_shed / request_invalid / policy_blocked / binding_passthru）：
		// 零 Redis RTT（design §3.4.3）。
		return service.FailoverCooldownWriteResult{}
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeWrite, "cool", errFailoverCooldownRedisUnavailable)
		return service.FailoverCooldownWriteResult{Degraded: true}
	}

	scope := service.FailoverCooldownScope{AccountID: write.AccountID, Model: write.Model}
	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	raw, err := recoveryWriteScript.Run(
		opCtx, c.rdb, recoveryScopeKeys(scope), ttl.Milliseconds(), string(write.Class),
		failoverCooldownMinTTLMS, window.Milliseconds(), grace.Milliseconds(),
	).Result()
	if err != nil {
		c.degrade(service.FailoverCooldownDegradeWrite, "cool", err)
		return service.FailoverCooldownWriteResult{Degraded: true}
	}

	applied, effectiveTTL := parseFailoverCooldownWriteReply(raw)
	return service.FailoverCooldownWriteResult{Applied: applied, TTL: effectiveTTL}
}

var _ service.FailoverRecoveryStore = (*failoverCooldownCache)(nil)

func recoveryScopeKeys(scope service.FailoverCooldownScope) []string {
	suffix := strconv.FormatInt(scope.AccountID, 10)
	if model := strings.TrimSpace(scope.Model); model != "" {
		suffix += ":" + failoverCooldownModelHash(model)
	}
	return []string{
		failoverCooldownAccountKeyPrefix + suffix,
		"failover:recovery:acct:" + suffix,
		"failover:recovery-lease:acct:" + suffix,
		"failover:cooldown-write:acct:" + suffix,
	}
}

func (c *failoverCooldownCache) ReadRecoveryBatch(ctx context.Context, scopes []service.FailoverCooldownScope) service.FailoverCooldownSnapshot {
	if len(scopes) == 0 {
		return service.FailoverCooldownSnapshot{}
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeRead, "recovery_batch", errFailoverCooldownRedisUnavailable)
		return service.FailoverCooldownSnapshot{Degraded: true}
	}
	snapshot := service.FailoverCooldownSnapshot{
		Active:  make(map[service.FailoverCooldownScope]service.FailoverCooldownEntry),
		Pending: make(map[service.FailoverCooldownScope]struct{}),
	}
	chunkSize := c.batchReadMax
	if chunkSize <= 0 {
		chunkSize = len(scopes)
	}
	for start := 0; start < len(scopes); start += chunkSize {
		chunk := scopes[start:min(start+chunkSize, len(scopes))]
		keys := make([]string, 0, len(chunk)*2)
		for _, scope := range chunk {
			keys = append(keys, recoveryScopeKeys(scope)[:2]...)
		}
		opCtx, cancel := c.operationContext(ctx)
		// Eval directly guarantees one round trip per chunk, even on a cold script cache.
		values, err := recoveryReadScript.Eval(opCtx, c.rdb, keys).Slice()
		cancel()
		if err == nil && len(values) != len(keys) {
			err = errUnexpectedRecoveryReply
		}
		if err != nil {
			c.degrade(service.FailoverCooldownDegradeRead, "recovery_batch", err)
			return service.FailoverCooldownSnapshot{Degraded: true}
		}
		for i, scope := range chunk {
			if scope.AccountID <= 0 {
				continue
			}
			if value, ok := values[2*i].(string); ok && value != "" {
				class, deadline := parseFailoverCooldownValue(value)
				snapshot.Active[scope] = service.FailoverCooldownEntry{Class: class, Deadline: deadline}
			} else if pending, _ := failoverCooldownReplyInt64(values[2*i+1]); pending == 1 {
				snapshot.Pending[scope] = struct{}{}
			}
		}
	}
	return snapshot
}

func (c *failoverCooldownCache) AcquireRecoveryProbe(ctx context.Context, scope service.FailoverCooldownScope) service.FailoverProbeAcquisition {
	if scope.AccountID <= 0 {
		return service.FailoverProbeAcquisition{Status: service.FailoverProbeNotRequired}
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeProbe, "recovery_acquire", errFailoverCooldownRedisUnavailable)
		return service.FailoverProbeAcquisition{Status: service.FailoverProbeDegraded}
	}
	token, err := uuid.NewRandom()
	if err == nil {
		opCtx, cancel := c.operationContext(ctx)
		defer cancel()
		var values []any
		values, err = recoveryAcquireScript.Run(opCtx, c.rdb, recoveryScopeKeys(scope)[:3], token.String(), failoverCooldownProbeTokenTTL.Milliseconds()).Slice()
		if err == nil {
			if result, ok := parseRecoveryAcquisition(values, scope, token.String()); ok {
				return result
			}
			err = errUnexpectedRecoveryReply
		}
	}
	c.degrade(service.FailoverCooldownDegradeProbe, "recovery_acquire", err)
	return service.FailoverProbeAcquisition{Status: service.FailoverProbeDegraded}
}

func parseRecoveryAcquisition(values []any, scope service.FailoverCooldownScope, token string) (service.FailoverProbeAcquisition, bool) {
	if len(values) == 0 {
		return service.FailoverProbeAcquisition{}, false
	}
	status, _ := values[0].(string)
	result := service.FailoverProbeAcquisition{Status: service.FailoverProbeStatus(status)}
	switch result.Status {
	case service.FailoverProbeCooling, service.FailoverProbeNotRequired, service.FailoverProbeBusy:
		return result, len(values) == 1
	case service.FailoverProbeAcquired:
		if len(values) != 3 {
			return result, false
		}
		base, baseOK := failoverCooldownReplyInt64(values[1])
		failed, failedOK := failoverCooldownReplyInt64(values[2])
		if !baseOK || !failedOK || base <= 0 || base > int64((1<<63-1)/time.Millisecond) || failed < 0 || int64(int(failed)) != failed {
			return result, false
		}
		result.Lease = service.FailoverProbeLease{Scope: scope, Token: token, State: service.FailoverIsolationProbeState{
			Pending: true, BaseTTL: time.Duration(base) * time.Millisecond, FailedAttempts: int(failed),
		}}
		return result, true
	default:
		return result, false
	}
}

func (c *failoverCooldownCache) CompleteRecoveryProbe(ctx context.Context, lease service.FailoverProbeLease, outcome service.FailoverProbeOutcome, policy *service.FailoverPolicy) service.FailoverProbeCompletion {
	if lease.Scope.AccountID <= 0 || lease.Token == "" {
		return service.FailoverProbeCompletion{}
	}
	action := "release"
	var retry time.Duration
	switch outcome {
	case service.FailoverProbeRelease:
	case service.FailoverProbeSuccess:
		action = "clear"
	case service.FailoverProbeFailure:
		if plan, ok := service.PlanProbeFailure(policy, lease.State, service.FailoverIsolationProbeAcquired); ok {
			if plan.Clear {
				action = "clear"
			} else {
				action = "retry"
				retry = plan.RetryAfter
			}
		}
	default:
		return service.FailoverProbeCompletion{}
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeProbe, "recovery_complete", errFailoverCooldownRedisUnavailable)
		return service.FailoverProbeCompletion{Degraded: true}
	}
	keys := recoveryScopeKeys(lease.Scope)
	if strings.TrimSpace(lease.Scope.Model) == "" {
		keys = append(keys, failoverFailureStreakKey(lease.Scope.AccountID))
	}
	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	applied, err := recoveryCompleteScript.Run(opCtx, c.rdb, keys, lease.Token, action, lease.State.FailedAttempts,
		max(retry.Milliseconds(), 1), failoverRecoveryGrace.Milliseconds()).Int64()
	if err != nil {
		c.degrade(service.FailoverCooldownDegradeProbe, "recovery_complete", err)
		return service.FailoverProbeCompletion{Degraded: true}
	}
	return service.FailoverProbeCompletion{Applied: applied == 1}
}

var errUnexpectedRecoveryReply = errors.New("unexpected failover recovery reply")

// Clear 清除账号级冷却与连续失败计数（design §3.4.4「成功即清除」）。
//
// 只清账号级 key：模型级 key 带 modelHash、无法枚举，交给 TTL 自然过期（最长 max_ttl）。
func (c *failoverCooldownCache) Clear(ctx context.Context, accountID int64) {
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeClear, "clear", errFailoverCooldownRedisUnavailable)
		return
	}
	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	keys := append(recoveryScopeKeys(service.FailoverCooldownScope{AccountID: accountID}), failoverFailureStreakKey(accountID))
	if err := c.rdb.Del(opCtx, keys...).Err(); err != nil {
		c.degrade(service.FailoverCooldownDegradeClear, "clear", err)
	}
}

// IncrFailureStreak 原子自增连续失败计数（带 TTL 保证）。
//
// window <= 0 时使用配置 streak_window；两者都未配置时不计数（返回 0 = 不冷却，fail-open）。
func (c *failoverCooldownCache) IncrFailureStreak(ctx context.Context, accountID int64, window time.Duration) int64 {
	effectiveWindow := window
	if effectiveWindow <= 0 {
		effectiveWindow = c.streakWindow
	}
	windowMS := effectiveWindow.Milliseconds()
	if windowMS <= 0 {
		return 0
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeStreak, "incr_streak", errFailoverCooldownRedisUnavailable)
		return 0
	}

	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	count, err := failoverFailureStreakScript.Run(
		opCtx,
		c.rdb,
		[]string{failoverFailureStreakKey(accountID)},
		windowMS,
	).Int64()
	if err != nil {
		c.degrade(service.FailoverCooldownDegradeStreak, "incr_streak", err)
		return 0
	}
	return count
}

// AcquireProbeToken 取半开探测令牌（`SET NX` 单命令原子，design §3.5.3 问题 3）。
func (c *failoverCooldownCache) AcquireProbeToken(ctx context.Context, accountID int64) bool {
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeProbe, "probe_setnx", errFailoverCooldownRedisUnavailable)
		return false
	}
	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	acquired, err := c.rdb.SetNX(opCtx, failoverProbeTokenKey(accountID), "1", failoverCooldownProbeTokenTTL).Result()
	if err != nil {
		c.degrade(service.FailoverCooldownDegradeProbe, "probe_setnx", err)
		return false
	}
	return acquired
}

// ClearProbeToken 释放探测令牌（探测失败再升级 / 手动清除）。
func (c *failoverCooldownCache) ClearProbeToken(ctx context.Context, accountID int64) {
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeProbe, "probe_clear", errFailoverCooldownRedisUnavailable)
		return
	}
	opCtx, cancel := c.operationContext(ctx)
	defer cancel()
	if err := c.rdb.Del(opCtx, failoverProbeTokenKey(accountID)).Err(); err != nil {
		c.degrade(service.FailoverCooldownDegradeProbe, "probe_clear", err)
	}
}

// isCooledBatch 是账号级 / 模型级共用的批量读实现：按 batch_read_max 分片，每片一次 MGET。
func (c *failoverCooldownCache) isCooledBatch(
	ctx context.Context,
	accountIDs []int64,
	keyFn func(accountID int64) string,
) map[int64]service.FailoverCooldownEntry {
	cooled := make(map[int64]service.FailoverCooldownEntry)
	if len(accountIDs) == 0 {
		return cooled
	}
	if !c.ready() {
		c.degrade(service.FailoverCooldownDegradeRead, "mget", errFailoverCooldownRedisUnavailable)
		return map[int64]service.FailoverCooldownEntry{}
	}

	chunkSize := c.batchReadMax
	if chunkSize <= 0 || chunkSize > len(accountIDs) {
		chunkSize = len(accountIDs)
	}
	for start := 0; start < len(accountIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(accountIDs) {
			end = len(accountIDs)
		}
		chunk := accountIDs[start:end]
		keys := make([]string, len(chunk))
		for i, accountID := range chunk {
			keys[i] = keyFn(accountID)
		}

		opCtx, cancel := c.operationContext(ctx)
		values, err := c.rdb.MGet(opCtx, keys...).Result()
		cancel()
		if err != nil {
			c.degrade(service.FailoverCooldownDegradeRead, "mget", err)
			// fail-open：任何一个分片失败都**不允许排除任何账号**，因此丢弃已读到的分片。
			return map[int64]service.FailoverCooldownEntry{}
		}
		for i, raw := range values {
			value, ok := raw.(string)
			if !ok || value == "" {
				continue
			}
			class, deadline := parseFailoverCooldownValue(value)
			cooled[chunk[i]] = service.FailoverCooldownEntry{Class: class, Deadline: deadline}
		}
	}
	return cooled
}

// ready 报告底层 Redis 客户端是否可用（接线漏配时也要 fail-open，不能 panic）。
func (c *failoverCooldownCache) ready() bool {
	return c != nil && c.rdb != nil
}

// operationContext 给单次 Redis 操作加 cooldown.redis_timeout（design §3.5.7 默认 500ms）。
func (c *failoverCooldownCache) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.redisTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.redisTimeout)
}

// degrade 记录一次 fail-open 降级：计数**不去抖**，日志按 5s 去抖。
func (c *failoverCooldownCache) degrade(reason service.FailoverCooldownDegradeReason, op string, err error) {
	if c == nil {
		return
	}
	if c.reporter != nil {
		c.reporter.ReportFailoverCooldownDegraded(reason)
	}
	now := time.Now().UnixNano()
	last := c.lastDegradeLogUnixNano.Load()
	if now-last < int64(failoverCooldownDegradeLogInterval) {
		return
	}
	if !c.lastDegradeLogUnixNano.CompareAndSwap(last, now) {
		return
	}
	logger.L().Warn("failover cooldown degraded, fail-open",
		zap.String("component", "repository.failover_cooldown"),
		zap.String("op", op),
		zap.String("reason", string(reason)),
		zap.Error(err),
	)
}

// errFailoverCooldownRedisUnavailable 表示未注入 Redis 客户端（接线错误同样按 fail-open 处理）。
var errFailoverCooldownRedisUnavailable = errors.New("failover cooldown redis client is not configured")

// ---------------------------------------------------------------------------
// key / value 编解码（design §6.2）
// ---------------------------------------------------------------------------

// failoverCooldownAccountKey 账号级冷却 key：failover:cooldown:acct:{accountID}
func failoverCooldownAccountKey(accountID int64) string {
	return failoverCooldownAccountKeyPrefix + strconv.FormatInt(accountID, 10)
}

// failoverCooldownModelKey 账号×模型级冷却 key：failover:cooldown:acct:{accountID}:{modelHash}
func failoverCooldownModelKey(accountID int64, modelHash string) string {
	return failoverCooldownAccountKeyPrefix + strconv.FormatInt(accountID, 10) + ":" + modelHash
}

// failoverFailureStreakKey 连续失败计数 key：failover:streak:acct:{accountID}
func failoverFailureStreakKey(accountID int64) string {
	return failoverFailureStreakKeyPrefix + strconv.FormatInt(accountID, 10)
}

// failoverProbeTokenKey 半开探测令牌 key：failover:probe:acct:{accountID}
func failoverProbeTokenKey(accountID int64) string {
	return failoverProbeTokenKeyPrefix + strconv.FormatInt(accountID, 10)
}

// failoverCooldownModelHash 与 design §6.2 一致：sha256(model) 的十六进制前 16 字符，
// 避免超长 model 名把 key 撑大（风格对齐 openai_compat_prompt_cache_key.go 的哈希做法）。
func failoverCooldownModelHash(model string) string {
	sum := sha256.Sum256([]byte(model))
	return hex.EncodeToString(sum[:])[:failoverCooldownModelHashLength]
}

// parseFailoverCooldownValue 解析 Redis value `{class}|{deadlineMs}`。
// 格式不合法时按「冷却中但信息不明」处理（非空 value 即代表冷却），deadline 为零值。
func parseFailoverCooldownValue(value string) (service.FailureClass, time.Time) {
	class, deadlineRaw, found := strings.Cut(value, failoverCooldownValueSeparator)
	if !found {
		return service.FailureClass(value), time.Time{}
	}
	deadlineMS, err := strconv.ParseInt(deadlineRaw, 10, 64)
	if err != nil || deadlineMS <= 0 {
		return service.FailureClass(class), time.Time{}
	}
	return service.FailureClass(class), time.UnixMilli(deadlineMS)
}

// parseFailoverCooldownWriteReply 解析写脚本的 {applied, ttlMs} 回复。
func parseFailoverCooldownWriteReply(raw any) (applied bool, ttl time.Duration) {
	values, ok := raw.([]any)
	if !ok || len(values) < 2 {
		return false, 0
	}
	appliedFlag, _ := failoverCooldownReplyInt64(values[0])
	ttlMS, _ := failoverCooldownReplyInt64(values[1])
	return appliedFlag > 0, time.Duration(ttlMS) * time.Millisecond
}

// failoverCooldownReplyInt64 兼容 Redis 回复的多种数值表现（int64 / int / string / []byte）。
func failoverCooldownReplyInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case uint64:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	case []byte:
		parsed, err := strconv.ParseInt(string(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
