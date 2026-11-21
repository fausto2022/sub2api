//go:build unit

package service

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// 本文件锁定 T4.2「T2 冷却过滤接入选号链」的契约（design §3.4.2 / §3.5.3 问题 4）：
//   - 冷却中的账号不进入候选集，冷却到期自动回到候选集；
//   - cooldown.enabled=false → **零存储调用**（F-02）；
//   - 批量：一次 IsCooledBatch 覆盖全部候选（+ 请求带模型时一次 IsModelCooledBatch），不逐账号查；
//   - fail-open：存储返回空 map（= 没有账号冷却）时结果与关闭开关逐一相同，绝不清空候选集；
//   - 只做移除、不改顺序：未冷却账号的相对顺序与现状逐一相同（等价性用例）。
//
// 走的是负载感知路径（load_batch_enabled=true + 并发服务非空），即 Layer 2 候选过滤链。

// cooldownSelectionStoreStub 是选号测试专用的 FailoverCooldownStore 替身：
// 只对两个批量读返回预置结果，其余方法只计数（证明选号路径**从不写**冷却）。
type cooldownSelectionStoreStub struct {
	accountCooled  map[int64]FailoverCooldownEntry
	modelCooledFor map[string]map[int64]FailoverCooldownEntry

	accountBatchCalls int
	accountBatchIDs   []int64
	modelBatchCalls   int
	modelBatchIDs     []int64
	modelBatchArg     string

	writeCalls int
}

var _ FailoverCooldownStore = (*cooldownSelectionStoreStub)(nil)

func (s *cooldownSelectionStoreStub) IsCooledBatch(_ context.Context, accountIDs []int64) map[int64]FailoverCooldownEntry {
	s.accountBatchCalls++
	s.accountBatchIDs = append([]int64(nil), accountIDs...)
	return s.accountCooled
}

func (s *cooldownSelectionStoreStub) IsModelCooledBatch(_ context.Context, accountIDs []int64, model string) map[int64]FailoverCooldownEntry {
	s.modelBatchCalls++
	s.modelBatchIDs = append([]int64(nil), accountIDs...)
	s.modelBatchArg = model
	if cooled, ok := s.modelCooledFor[model]; ok {
		return cooled
	}
	return nil
}

func (s *cooldownSelectionStoreStub) Cool(context.Context, FailoverCooldownWrite) FailoverCooldownWriteResult {
	s.writeCalls++
	return FailoverCooldownWriteResult{}
}

func (s *cooldownSelectionStoreStub) Clear(context.Context, int64) { s.writeCalls++ }

func (s *cooldownSelectionStoreStub) IncrFailureStreak(context.Context, int64, time.Duration) int64 {
	s.writeCalls++
	return 0
}

func (s *cooldownSelectionStoreStub) AcquireProbeToken(context.Context, int64) bool {
	s.writeCalls++
	return false
}

func (s *cooldownSelectionStoreStub) ClearProbeToken(context.Context, int64) { s.writeCalls++ }

// cooldownSelectionFixture 构造走负载感知路径的最小 GatewayService。
type cooldownSelectionFixture struct {
	svc              *GatewayService
	ctx              context.Context
	groupID          int64
	store            *cooldownSelectionStoreStub
	concurrencyCache *mockConcurrencyCache
	accountRepo      *mockAccountRepoForPlatform
}

func newCooldownSelectionFixture(t *testing.T, cooldownEnabled bool, store *cooldownSelectionStoreStub) *cooldownSelectionFixture {
	t.Helper()

	groupID := int64(10)
	accountRepo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 5},
			{ID: 2, Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true, Concurrency: 5},
			{ID: 3, Platform: PlatformAnthropic, Priority: 3, Status: StatusActive, Schedulable: true, Concurrency: 5},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range accountRepo.accounts {
		accountRepo.accountsByID[accountRepo.accounts[i].ID] = &accountRepo.accounts[i]
	}
	group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}}

	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.FailoverPolicy.Cooldown.Enabled = cooldownEnabled

	if store == nil {
		store = &cooldownSelectionStoreStub{}
	}

	concurrencyCache := &mockConcurrencyCache{}
	svc := &GatewayService{
		accountRepo:           accountRepo,
		groupRepo:             groupRepo,
		cache:                 &mockGatewayCacheForPlatform{},
		cfg:                   cfg,
		concurrencyService:    NewConcurrencyService(concurrencyCache),
		failoverCooldownStore: store,
	}

	return &cooldownSelectionFixture{
		svc:              svc,
		ctx:              context.WithValue(context.Background(), ctxkey.Group, group),
		groupID:          groupID,
		store:            store,
		concurrencyCache: concurrencyCache,
		accountRepo:      accountRepo,
	}
}

func cooledEntries(accountIDs ...int64) map[int64]FailoverCooldownEntry {
	cooled := make(map[int64]FailoverCooldownEntry, len(accountIDs))
	for _, accountID := range accountIDs {
		cooled[accountID] = FailoverCooldownEntry{Class: ClassUpstream5xx, Deadline: time.Now().Add(time.Minute)}
	}
	return cooled
}

// TestSelectAccountWithLoadAwareness_CooldownExcludesCooledAccounts 覆盖「冷却中的账号不进入候选集」，
// 同时锁定「只做移除、不改顺序」：剩下的候选仍按既有优先级顺序被选中。
func TestSelectAccountWithLoadAwareness_CooldownExcludesCooledAccounts(t *testing.T) {
	tests := []struct {
		name          string
		cooled        []int64
		wantAccountID int64
		wantErr       bool
	}{
		{name: "冷却最高优先级账号 → 选下一优先级", cooled: []int64{1}, wantAccountID: 2},
		{name: "冷却次优先级账号 → 高优先级不受影响", cooled: []int64{2}, wantAccountID: 1},
		{name: "冷却末位账号 → 高优先级不受影响", cooled: []int64{3}, wantAccountID: 1},
		{name: "冷却前两个 → 只剩末位", cooled: []int64{1, 2}, wantAccountID: 3},
		{name: "全部冷却 → 无可用账号（不降级执行）", cooled: []int64{1, 2, 3}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &cooldownSelectionStoreStub{accountCooled: cooledEntries(tt.cooled...)}
			f := newCooldownSelectionFixture(t, true, store)

			result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "", nil, "", 0)

			require.Equal(t, 1, store.accountBatchCalls, "账号级冷却必须一次批量查询覆盖全部候选")
			require.Equal(t, 0, store.writeCalls, "选号路径不得写冷却")
			if tt.wantErr {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Nil(t, result)
				require.Equal(t, 0, f.concurrencyCache.acquireAccountCalls, "全冷却时不应尝试占用任何账号槽位")
				require.Equal(t, 0, f.concurrencyCache.loadBatchCalls, "候选集为空时不应再查负载")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, result.Account)
			require.Equal(t, tt.wantAccountID, result.Account.ID)
		})
	}
}

// TestSelectAccountWithLoadAwareness_CooldownBatchCoversAllCandidates 覆盖「一次批量、不逐账号」：
// 批量入参必须是全部候选账号 ID，且每类只调用一次。
func TestSelectAccountWithLoadAwareness_CooldownBatchCoversAllCandidates(t *testing.T) {
	store := &cooldownSelectionStoreStub{accountCooled: cooledEntries(1)}
	f := newCooldownSelectionFixture(t, true, store)

	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(2), result.Account.ID)

	require.Equal(t, 1, store.accountBatchCalls, "账号级 MGET 只允许一次")
	require.Equal(t, 1, store.modelBatchCalls, "模型级 MGET 只允许一次")
	sortedAccountIDs := append([]int64(nil), store.accountBatchIDs...)
	sort.Slice(sortedAccountIDs, func(i, j int) bool { return sortedAccountIDs[i] < sortedAccountIDs[j] })
	require.Equal(t, []int64{1, 2, 3}, sortedAccountIDs, "批量入参必须是全部候选账号")
	require.Equal(t, []int64{1, 2, 3}, store.modelBatchIDs, "模型级批量入参同样覆盖全部候选")
	require.Equal(t, "claude-sonnet-4-6", store.modelBatchArg, "模型级 key 必须带本次请求模型")
}

// TestSelectAccountWithLoadAwareness_CooldownDisabledSkipsStore 覆盖 F-02：开关关闭时零存储调用。
func TestSelectAccountWithLoadAwareness_CooldownDisabledSkipsStore(t *testing.T) {
	store := &cooldownSelectionStoreStub{accountCooled: cooledEntries(1, 2, 3)}
	f := newCooldownSelectionFixture(t, false, store)

	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), result.Account.ID, "开关关闭时行为与现状一致（最高优先级）")
	require.Equal(t, 0, store.accountBatchCalls, "F-02：关闭时不得访问账号级冷却")
	require.Equal(t, 0, store.modelBatchCalls, "F-02：关闭时不得访问模型级冷却")
}

// TestSelectAccountWithLoadAwareness_CooldownSingleAccountSkipsStoreQuery 覆盖「单账号分组零开销」。
func TestSelectAccountWithLoadAwareness_CooldownSingleAccountSkipsStoreQuery(t *testing.T) {
	store := &cooldownSelectionStoreStub{accountCooled: cooledEntries(1)}
	f := newCooldownSelectionFixture(t, true, store)

	// 只留一个可调度账号：没有可切换对象，冷却过滤无意义。
	f.accountRepo.accounts = f.accountRepo.accounts[:1]
	f.accountRepo.accountsByID = map[int64]*Account{1: &f.accountRepo.accounts[0]}

	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), result.Account.ID, "单账号分组不得因冷却被排除")
	require.Equal(t, 0, store.accountBatchCalls)
	require.Equal(t, 0, store.modelBatchCalls)
}

// TestSelectAccountWithLoadAwareness_CooldownFailOpenKeepsAllCandidates 覆盖 fail-open（C-02）：
// 存储降级时返回空 map，选号侧必须按「没有账号在冷却」处理，绝不清空候选集。
func TestSelectAccountWithLoadAwareness_CooldownFailOpenKeepsAllCandidates(t *testing.T) {
	store := &cooldownSelectionStoreStub{} // 两处批量读都返回 nil（模拟 Redis 降级后的 fail-open）
	f := newCooldownSelectionFixture(t, true, store)

	result, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), result.Account.ID, "降级时必须按原优先级选号，不得清空候选集")
	require.Equal(t, 1, store.accountBatchCalls, "降级路径依然只做一次批量查询")
}

// TestSelectAccountWithLoadAwareness_CooldownEquivalentWhenNothingCooled 是 T4.2 的自证用例：
// 开启冷却但没有任何账号在冷却时，选号结果与关闭开关时**逐一相同**（接入无副作用）。
func TestSelectAccountWithLoadAwareness_CooldownEquivalentWhenNothingCooled(t *testing.T) {
	disabledStore := &cooldownSelectionStoreStub{}
	disabled := newCooldownSelectionFixture(t, false, disabledStore)
	baseline, err := disabled.svc.SelectAccountWithLoadAwareness(disabled.ctx, &disabled.groupID, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, baseline)

	enabledStore := &cooldownSelectionStoreStub{}
	enabled := newCooldownSelectionFixture(t, true, enabledStore)
	withCooldown, err := enabled.svc.SelectAccountWithLoadAwareness(enabled.ctx, &enabled.groupID, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, withCooldown)

	// 逐一相同：账号、是否拿到槽位、等待计划、释放函数都有无一致。
	require.Equal(t, baseline.Account.ID, withCooldown.Account.ID, "无账号冷却时选号结果必须逐位相同")
	require.Equal(t, baseline.Acquired, withCooldown.Acquired)
	require.Equal(t, baseline.WaitPlan, withCooldown.WaitPlan)
	require.Equal(t, baseline.ReleaseFunc != nil, withCooldown.ReleaseFunc != nil)
	if baseline.ReleaseFunc != nil {
		baseline.ReleaseFunc()
	}
	if withCooldown.ReleaseFunc != nil {
		withCooldown.ReleaseFunc()
	}

	// 差别只在「多做了一次批量查询」：关闭时 0 次，开启时各 1 次。
	require.Equal(t, 0, disabledStore.accountBatchCalls)
	require.Equal(t, 0, disabledStore.modelBatchCalls)
	require.Equal(t, 1, enabledStore.accountBatchCalls)
	require.Equal(t, 1, enabledStore.modelBatchCalls)
}

// TestSelectAccountWithLoadAwareness_ModelCooldownOnlyAffectsMatchingModel 覆盖模型级冷却
// 与账号级冷却的 key 语义分离（design §6.2）：只有同模型的模型级冷却才排除账号。
func TestSelectAccountWithLoadAwareness_ModelCooldownOnlyAffectsMatchingModel(t *testing.T) {
	store := &cooldownSelectionStoreStub{
		modelCooledFor: map[string]map[int64]FailoverCooldownEntry{
			"claude-sonnet-4-6": cooledEntries(1),
		},
	}
	f := newCooldownSelectionFixture(t, true, store)

	// 同模型：账号 1 的模型级冷却生效 → 选 2。
	sameModel, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, sameModel)
	require.Equal(t, int64(2), sameModel.Account.ID)
	require.Equal(t, "claude-sonnet-4-6", store.modelBatchArg)

	// 不同模型：同一账号不受影响 → 仍选 1。
	otherModel, err := f.svc.SelectAccountWithLoadAwareness(f.ctx, &f.groupID, "", "claude-opus-4-1", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, otherModel)
	require.Equal(t, int64(1), otherModel.Account.ID)
	require.Equal(t, "claude-opus-4-1", store.modelBatchArg)
	require.Equal(t, 2, store.modelBatchCalls, "每次选号一次模型级批量查询")
}

// TestCooledAccountIDsForSelection_ZeroRTTWhenDisabledOrUninjected 直接锁定三层短路：
// 开关关闭 / 未注入存储 / 候选不足 2 个，都不得访问存储。
func TestCooledAccountIDsForSelection_ZeroRTTWhenDisabledOrUninjected(t *testing.T) {
	accounts := []Account{{ID: 1}, {ID: 2}}
	store := &cooldownSelectionStoreStub{accountCooled: cooledEntries(1)}

	t.Run("开关关闭", func(t *testing.T) {
		f := newCooldownSelectionFixture(t, false, store)
		require.Nil(t, f.svc.cooledAccountIDsForSelection(context.Background(), accounts, "m"))
		require.Equal(t, 0, store.accountBatchCalls+store.modelBatchCalls)
	})

	t.Run("未注入存储", func(t *testing.T) {
		f := newCooldownSelectionFixture(t, true, store)
		f.svc.SetFailoverCooldownStore(nil)
		require.Nil(t, f.svc.cooledAccountIDsForSelection(context.Background(), accounts, "m"))
		require.Equal(t, 0, store.accountBatchCalls+store.modelBatchCalls)
	})

	t.Run("候选不足 2 个", func(t *testing.T) {
		f := newCooldownSelectionFixture(t, true, store)
		require.Nil(t, f.svc.cooledAccountIDsForSelection(context.Background(), accounts[:1], "m"))
		require.Equal(t, 0, store.accountBatchCalls+store.modelBatchCalls)
	})

	t.Run("启用且有多候选 → 返回冷却集合", func(t *testing.T) {
		f := newCooldownSelectionFixture(t, true, store)
		cooled := f.svc.cooledAccountIDsForSelection(context.Background(), accounts, "")
		require.Equal(t, map[int64]struct{}{1: {}}, cooled)
		require.Equal(t, 1, store.accountBatchCalls)
		require.Equal(t, 0, store.modelBatchCalls, "空模型不查模型级 key")
	})
}
