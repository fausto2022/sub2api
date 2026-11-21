package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 本文件锁定 T0.3 的契约：
//   - 三级就近覆盖（账号 > 分组 > 全局）+ source 标记
//   - 字段级 merge（不是对象级覆盖）
//   - nil（未设置）与显式 false/0 可区分
//   - enabled=false 时 Legacy=true（回滚到旧硬编码路径）
//
// 注意：**默认值的唯一权威是 config 包**（backend/internal/config/config_test.go 的
// 默认值表）。这里的 global 夹具只是「按 design §5.1 默认值写死的一份测试数据」，
// 用来把 Resolve 的行为与配置默认值解耦，避免两处断言互相掩盖。

func fpBoolPtr(v bool) *bool                  { return &v }
func fpIntPtr(v int) *int                     { return &v }
func fpStrPtr(v string) *string               { return &v }
func fpDurPtr(v time.Duration) *time.Duration { return &v }

// testGlobalFailoverConfig 是 global 层的测试夹具（取值 = design §5.1 的默认值）。
func testGlobalFailoverConfig() config.GatewayFailoverPolicyConfig {
	return config.GatewayFailoverPolicyConfig{
		Enabled: true,
		NetworkErrors: config.FailoverNetworkErrorsConfig{
			Enabled: true,
		},
		FailoverOn400: false,
		Compat400Keywords: []string{
			"anthropic-beta", "beta feature", "requires beta",
			"thinking", "thought_signature", "signature",
			"tool_use", "tool_result", "tools",
		},
		SameAccount: config.FailoverSameAccountConfig{DefaultMode: config.FailoverSameAccountModeInherit},
		Budget: config.FailoverBudgetConfig{
			TotalTimeout:      0,
			PerAccountTimeout: 0,
			MinSwitchCost:     2 * time.Second,
			StopClockOnStream: true,
		},
		FirstOutputTimeoutSeconds: 0,
		Cooldown: config.FailoverCooldownConfig{
			Enabled:                      false,
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
		},
		SwitchOrder: config.FailoverSwitchOrderConfig{
			RecordChain:                 true,
			ClearStickyOnAccountFailure: true,
		},
		MaxSelectionRetries: 0,
		Switchable: config.FailoverSwitchableConfig{
			UseSemanticBytes:     true,
			TreatKeepaliveAsSafe: true,
		},
		T3: config.FailoverT3Config{OnlyExtend: true},
		// 全局层恒有值：规则表与两个字符串切片也必须非 nil，否则上层未写的字段
		// 会变成 nil 并被消费方当成零值。
		StatusClasses: []config.FailoverStatusClassRuleConfig{
			{Class: config.FailoverClassAuthInvalid, Codes: []int{401, 403}},
			{Class: config.FailoverClassUpstream5xx, Codes: []int{500, 502, 504}, Ranges: []string{"500-599"}},
		},
		Idempotency: config.FailoverIdempotencyConfig{BlockedTools: []string{}},
	}
}

func testGlobalFailoverPolicy() FailoverPolicy {
	return FailoverPolicyFromConfig(testGlobalFailoverConfig())
}

type resolvePolicyCase struct {
	name         string
	mutateGlobal func(*config.GatewayFailoverPolicyConfig)
	account      *FailoverPolicy
	group        *GroupFailoverPolicyRow
	assert       func(t *testing.T, policy FailoverPolicy, source string)
}

func TestResolveFailoverPolicyThreeLayerPrecedence(t *testing.T) {
	cases := []resolvePolicyCase{
		{
			name: "无任何覆盖时完全等于全局默认",
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGlobal, source)
				require.NotNil(t, policy.Enabled)
				require.True(t, *policy.Enabled)
				require.NotNil(t, policy.FailoverOn400)
				require.False(t, *policy.FailoverOn400)
				require.NotNil(t, policy.CooldownEnabled)
				require.False(t, *policy.CooldownEnabled)
				require.NotNil(t, policy.CooldownDefaultTTL)
				require.Equal(t, 60*time.Second, *policy.CooldownDefaultTTL)
				require.NotNil(t, policy.MaxSelectionRetries)
				require.Equal(t, 0, *policy.MaxSelectionRetries)
				require.Nil(t, policy.MaxAccountSwitches, "全局层不承载 max_account_switches")
			},
		},
		{
			name:         "分组单列覆盖：显式 false 必须压过全局 true",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.FailoverOn400 = true },
			group:        &GroupFailoverPolicyRow{GroupID: 1, On400: fpBoolPtr(false)},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.NotNil(t, policy.FailoverOn400)
				require.False(t, *policy.FailoverOn400, "显式 false 不能被当成「未设置」")
			},
		},
		{
			name:  "分组只写 cooldown.enabled，其余字段回落全局（字段级 merge）",
			group: &GroupFailoverPolicyRow{GroupID: 2, Policy: &FailoverPolicy{CooldownEnabled: fpBoolPtr(true)}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.True(t, *policy.CooldownEnabled)
				require.Equal(t, 60*time.Second, *policy.CooldownDefaultTTL, "未覆盖字段必须回落全局")
				require.Equal(t, 3, *policy.CooldownConsecutiveFailuresThreshold)
				require.Equal(t, 24*time.Hour, *policy.CooldownMaxTTL)
				require.True(t, *policy.CooldownFailOpen)
				require.False(t, *policy.FailoverOn400, "其它字段同样保持全局值")
			},
		},
		{
			name:  "分组覆盖切换次数（既有扁平键的分组级覆盖）",
			group: &GroupFailoverPolicyRow{GroupID: 3, MaxAccountSwitches: fpIntPtr(2)},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.NotNil(t, policy.MaxAccountSwitches)
				require.Equal(t, 2, *policy.MaxAccountSwitches)
			},
		},
		{
			name:    "账号层覆盖 enabled=false → Legacy，且 source=account",
			account: &FailoverPolicy{Enabled: fpBoolPtr(false)},
			group:   &GroupFailoverPolicyRow{GroupID: 4, On400: fpBoolPtr(true)},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceAccount, source)
				require.True(t, policy.Legacy())
				// 账号层只覆盖 enabled 一个字段，分组层的 on_400 仍然生效（merge 而不是替换）。
				require.True(t, *policy.FailoverOn400)
			},
		},
		{
			name:         "账号层覆盖预算，分组层覆盖冷却，两者叠加",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.Budget.MaxTotalUpstreamAttempts = 0 },
			account:      &FailoverPolicy{BudgetTotalTimeout: fpDurPtr(90 * time.Second)},
			group: &GroupFailoverPolicyRow{GroupID: 5, Policy: &FailoverPolicy{
				CooldownEnabled:      fpBoolPtr(true),
				CooldownProbeEnabled: fpBoolPtr(false),
			}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceAccount, source)
				require.Equal(t, 90*time.Second, *policy.BudgetTotalTimeout)
				require.Equal(t, 2*time.Second, *policy.BudgetMinSwitchCost)
				require.Equal(t, 0, *policy.BudgetMaxTotalUpstreamAttempts)
				require.True(t, *policy.CooldownEnabled)
				require.False(t, *policy.CooldownProbeEnabled)
				require.True(t, *policy.CooldownPoolModeEnabled, "分组未写的字段仍回落全局")
			},
		},
		{
			name:    "同一字段账号层压过分组层",
			account: &FailoverPolicy{CooldownDefaultTTL: fpDurPtr(15 * time.Second)},
			group: &GroupFailoverPolicyRow{GroupID: 6, Policy: &FailoverPolicy{
				CooldownDefaultTTL: fpDurPtr(45 * time.Second),
			}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceAccount, source)
				require.Equal(t, 15*time.Second, *policy.CooldownDefaultTTL)
			},
		},
		{
			name: "分组全字段覆盖",
			group: &GroupFailoverPolicyRow{GroupID: 7, On400: fpBoolPtr(true), MaxAccountSwitches: fpIntPtr(1),
				Policy: &FailoverPolicy{
					Enabled:                               fpBoolPtr(true),
					NetworkErrorsEnabled:                  fpBoolPtr(false),
					Compat400Keywords:                     &[]string{"thinking"},
					SameAccountDefaultMode:                fpStrPtr(config.FailoverSameAccountModeOff),
					BudgetMaxSameAccountAttemptsTotal:     fpIntPtr(2),
					BudgetStopClockOnStream:               fpBoolPtr(false),
					FirstOutputTimeoutSeconds:             fpIntPtr(45),
					CooldownEnabled:                       fpBoolPtr(true),
					CooldownStreakWindow:                  fpDurPtr(60 * time.Second),
					CooldownMaxTTL:                        fpDurPtr(time.Hour),
					CooldownBatchReadMax:                  fpIntPtr(50),
					SwitchOrderRecordChain:                fpBoolPtr(false),
					MaxSelectionRetries:                   fpIntPtr(2),
					SwitchableUseSemanticBytes:            fpBoolPtr(false),
					SwitchableTreatKeepaliveAsSafe:        fpBoolPtr(false),
					IdempotencyRequireKeyForSideEffecting: fpBoolPtr(true),
					ExposeFailoverDetail:                  fpBoolPtr(true),
					TerminalMixedStatus503:                fpBoolPtr(true),
					CostAttemptsEnabled:                   fpBoolPtr(true),
				}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.True(t, *policy.FailoverOn400)
				require.Equal(t, 1, *policy.MaxAccountSwitches)
				require.False(t, *policy.NetworkErrorsEnabled)
				require.Equal(t, []string{"thinking"}, *policy.Compat400Keywords)
				require.Equal(t, config.FailoverSameAccountModeOff, *policy.SameAccountDefaultMode)
				require.Equal(t, 2, *policy.BudgetMaxSameAccountAttemptsTotal)
				require.False(t, *policy.BudgetStopClockOnStream)
				require.Equal(t, 45, *policy.FirstOutputTimeoutSeconds)
				require.True(t, *policy.CooldownEnabled)
				require.Equal(t, 60*time.Second, *policy.CooldownStreakWindow)
				require.Equal(t, time.Hour, *policy.CooldownMaxTTL)
				require.Equal(t, 50, *policy.CooldownBatchReadMax)
				require.False(t, *policy.SwitchOrderRecordChain)
				require.Equal(t, 2, *policy.MaxSelectionRetries)
				require.False(t, *policy.SwitchableUseSemanticBytes)
				require.True(t, *policy.IdempotencyRequireKeyForSideEffecting)
				require.True(t, *policy.ExposeFailoverDetail)
				require.True(t, *policy.TerminalMixedStatus503)
				require.True(t, *policy.CostAttemptsEnabled)
			},
		},
		{
			name:  "分组行存在但字段全空 → 仍算 global",
			group: &GroupFailoverPolicyRow{GroupID: 8, Policy: &FailoverPolicy{}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGlobal, source)
				require.False(t, *policy.FailoverOn400)
			},
		},
		{
			name:         "分组显式写入与全局相同的值，source 仍是 group（存在性而非差异）",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.FailoverOn400 = false },
			group:        &GroupFailoverPolicyRow{GroupID: 9, On400: fpBoolPtr(false)},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.False(t, *policy.FailoverOn400)
			},
		},
		{
			name:         "全局改值后无覆盖时取全局新值",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.Cooldown.Enabled = true },
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGlobal, source)
				require.True(t, *policy.CooldownEnabled)
			},
		},
		{
			name:         "全局 enabled=false 时无覆盖也走 Legacy",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.Enabled = false },
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGlobal, source)
				require.True(t, policy.Legacy())
			},
		},
		{
			name:         "全局 enabled=true 时 Legacy=false",
			mutateGlobal: func(cfg *config.GatewayFailoverPolicyConfig) { cfg.Enabled = true },
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.False(t, policy.Legacy())
			},
		},
		{
			name:    "账号层显式 false 与分组层显式 true 各自独立（不互相吞掉）",
			account: &FailoverPolicy{SwitchableUseSemanticBytes: fpBoolPtr(false)},
			group: &GroupFailoverPolicyRow{GroupID: 10, Policy: &FailoverPolicy{
				SwitchableTreatKeepaliveAsSafe: fpBoolPtr(false),
			}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceAccount, source)
				require.False(t, *policy.SwitchableUseSemanticBytes)
				require.False(t, *policy.SwitchableTreatKeepaliveAsSafe)
			},
		},
		{
			name: "分组只覆盖 status_classes 时不影响其它字段",
			group: &GroupFailoverPolicyRow{GroupID: 11, Policy: &FailoverPolicy{
				StatusClasses: &[]config.FailoverStatusClassRuleConfig{
					{Class: config.FailoverClassAuthInvalid, Codes: []int{401}},
				},
			}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGroup, source)
				require.Len(t, *policy.StatusClasses, 1)
				require.Equal(t, config.FailoverClassAuthInvalid, (*policy.StatusClasses)[0].Class)
				require.True(t, *policy.SwitchableUseSemanticBytes, "其它字段回落全局")
			},
		},
		{
			name:    "账号层只覆盖 blocked_tools",
			account: &FailoverPolicy{IdempotencyBlockedTools: &[]string{"pay", "refund"}},
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceAccount, source)
				require.Equal(t, []string{"pay", "refund"}, *policy.IdempotencyBlockedTools)
				require.False(t, *policy.IdempotencyRequireKeyForSideEffecting)
			},
		},
		{
			name:  "nil 分组行等价于无分组覆盖",
			group: nil,
			assert: func(t *testing.T, policy FailoverPolicy, source string) {
				require.Equal(t, FailoverPolicySourceGlobal, source)
				require.Nil(t, policy.MaxAccountSwitches)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testGlobalFailoverConfig()
			if tc.mutateGlobal != nil {
				tc.mutateGlobal(&cfg)
			}
			global := FailoverPolicyFromConfig(cfg)
			globalSnapshot := global

			policy, source := ResolveFailoverPolicy(tc.account, tc.group, global)

			tc.assert(t, policy, source)
			// 纯函数：不得修改调用方传入的全局层（含指针指向的值）。
			require.Equal(t, globalSnapshot.Enabled, global.Enabled)
			require.Equal(t, *globalSnapshot.CooldownDefaultTTL, *global.CooldownDefaultTTL)
		})
	}
}

func TestResolveFailoverPolicyZeroValueGlobal(t *testing.T) {
	// 保底：连全局层都是零值时也不能 panic，且必须回落到 Legacy（宁可不启用新判定）。
	policy, source := ResolveFailoverPolicy(nil, nil, FailoverPolicy{})
	require.Equal(t, FailoverPolicySourceGlobal, source)
	require.True(t, policy.Legacy())
}

func TestFailoverPolicyHasAnyField(t *testing.T) {
	var nilPolicy *FailoverPolicy
	require.False(t, nilPolicy.HasAnyField())
	require.False(t, (&FailoverPolicy{}).HasAnyField())
	require.True(t, (&FailoverPolicy{FailoverOn400: fpBoolPtr(false)}).HasAnyField(),
		"显式 false 也是覆盖")
	require.True(t, (&FailoverPolicy{CooldownDefaultTTL: fpDurPtr(0)}).HasAnyField(),
		"显式 0 也是覆盖")
}

func TestFailoverPolicyNilReceiverIsLegacy(t *testing.T) {
	var nilPolicy *FailoverPolicy
	require.True(t, nilPolicy.Legacy())
	require.True(t, (&FailoverPolicy{}).Legacy(), "Enabled 未设置时按 legacy 处理")
	require.False(t, (&FailoverPolicy{Enabled: fpBoolPtr(true)}).Legacy())
}

func TestFailoverPolicyFromConfigFillsEveryField(t *testing.T) {
	policy := testGlobalFailoverPolicy()

	// 全局层是 merge 的基座：除「只由分组列承载」的 MaxAccountSwitches 外，所有字段
	// 都必须有值，否则上层未写的字段会变成 nil 并被消费方当成零值。
	require.Nil(t, policy.MaxAccountSwitches)
	require.NotNil(t, policy.Enabled)
	require.NotNil(t, policy.StatusClasses)
	require.NotNil(t, policy.NetworkErrorsEnabled)
	require.NotNil(t, policy.FailoverOn400)
	require.NotNil(t, policy.Compat400Keywords)
	require.NotNil(t, policy.SameAccountDefaultMode)
	require.NotNil(t, policy.BudgetTotalTimeout)
	require.NotNil(t, policy.BudgetPerAccountTimeout)
	require.NotNil(t, policy.BudgetMaxTotalUpstreamAttempts)
	require.NotNil(t, policy.BudgetMaxSameAccountAttemptsTotal)
	require.NotNil(t, policy.BudgetMinSwitchCost)
	require.NotNil(t, policy.BudgetStopClockOnStream)
	require.NotNil(t, policy.FirstOutputTimeoutSeconds)
	require.NotNil(t, policy.CooldownEnabled)
	require.NotNil(t, policy.CooldownConsecutiveFailuresThreshold)
	require.NotNil(t, policy.CooldownStreakWindow)
	require.NotNil(t, policy.CooldownDefaultTTL)
	require.NotNil(t, policy.CooldownMaxTTL)
	require.NotNil(t, policy.CooldownPoolModeEnabled)
	require.NotNil(t, policy.CooldownProbeEnabled)
	require.NotNil(t, policy.CooldownProbeMaxAttempts)
	require.NotNil(t, policy.CooldownRedisTimeout)
	require.NotNil(t, policy.CooldownFailOpen)
	require.NotNil(t, policy.CooldownBatchReadMax)
	require.NotNil(t, policy.SwitchOrderRecordChain)
	require.NotNil(t, policy.SwitchOrderClearStickyOnAccountFailure)
	require.NotNil(t, policy.MaxSelectionRetries)
	require.NotNil(t, policy.SwitchableUseSemanticBytes)
	require.NotNil(t, policy.SwitchableTreatKeepaliveAsSafe)
	require.NotNil(t, policy.IdempotencyRequireKeyForSideEffecting)
	require.NotNil(t, policy.T3OnlyExtend)
	require.NotNil(t, policy.ExposeFailoverDetail)
	require.NotNil(t, policy.TerminalMixedStatus503)
	require.NotNil(t, policy.CostAttemptsEnabled)
}

func TestFailoverPolicyFromConfigCopiesSlices(t *testing.T) {
	cfg := testGlobalFailoverConfig()
	policy := FailoverPolicyFromConfig(cfg)

	// 修改源配置不得影响已构造的策略快照（全局快照会被多请求共享）。
	cfg.Compat400Keywords[0] = "mutated"
	cfg.StatusClasses = append(cfg.StatusClasses, config.FailoverStatusClassRuleConfig{Class: "extra"})
	cfg.Idempotency.BlockedTools = append(cfg.Idempotency.BlockedTools, "mutated")

	require.Equal(t, "anthropic-beta", (*policy.Compat400Keywords)[0])
	require.Len(t, *policy.StatusClasses, len(testGlobalFailoverConfig().StatusClasses))
	require.Empty(t, *policy.IdempotencyBlockedTools)
}

func TestGroupFailoverPolicyRowOverridePrefersScalarColumns(t *testing.T) {
	row := &GroupFailoverPolicyRow{
		GroupID:            42,
		On400:              fpBoolPtr(true),
		MaxAccountSwitches: fpIntPtr(4),
		Policy: &FailoverPolicy{
			FailoverOn400:      fpBoolPtr(false), // 被单列压过
			MaxAccountSwitches: fpIntPtr(9),      // 被单列压过
			CooldownEnabled:    fpBoolPtr(true),  // 仅 JSONB 有
		},
	}

	override := row.Override()

	require.NotNil(t, override)
	require.True(t, *override.FailoverOn400, "单列优先于 JSONB 同名字段")
	require.Equal(t, 4, *override.MaxAccountSwitches)
	require.True(t, *override.CooldownEnabled)
	// 返回的是浅拷贝：改写结果结构体不影响行本身。
	override.CooldownEnabled = fpBoolPtr(false)
	require.True(t, *row.Policy.CooldownEnabled, "改写结果结构体不得影响行本身")
}

func TestGroupFailoverPolicyRowNilOverride(t *testing.T) {
	var row *GroupFailoverPolicyRow
	require.Nil(t, row.Override())
	require.False(t, row.Override().HasAnyField())
}

func TestParseGroupFailoverPolicyJSON(t *testing.T) {
	t.Run("NULL 与空字节返回 nil", func(t *testing.T) {
		for _, raw := range [][]byte{nil, {}, []byte("   ")} {
			policy, err := ParseGroupFailoverPolicyJSON(raw)
			require.NoError(t, err)
			require.Nil(t, policy)
		}
	})

	t.Run("嵌套键与 duration 字符串", func(t *testing.T) {
		raw := []byte(`{
			"enabled": true,
			"failover_on_400": true,
			"same_account": {"default_mode": "off"},
			"budget": {"total_timeout": "120s", "max_total_upstream_attempts": 6},
			"first_output_timeout_seconds": 90,
			"cooldown": {"enabled": true, "default_ttl": "90s", "max_ttl": "2h",
			             "redis_timeout": "250ms", "fail_open": true, "batch_read_max": 100},
			"switch_order": {"record_chain": false, "clear_sticky_on_account_failure": false},
			"max_selection_retries": 1,
			"max_account_switches": 3,
			"switchable": {"use_semantic_bytes": false},
			"idempotency": {"blocked_tools": ["pay"]},
			"t3": {"only_extend": false},
			"terminal": {"mixed_status_503": true},
			"cost_attempts": {"enabled": true}
		}`)

		policy, err := ParseGroupFailoverPolicyJSON(raw)
		require.NoError(t, err)
		require.NotNil(t, policy)
		require.True(t, *policy.Enabled)
		require.True(t, *policy.FailoverOn400)
		require.Equal(t, config.FailoverSameAccountModeOff, *policy.SameAccountDefaultMode)
		require.Equal(t, 120*time.Second, *policy.BudgetTotalTimeout)
		require.Equal(t, 6, *policy.BudgetMaxTotalUpstreamAttempts)
		require.Equal(t, 90, *policy.FirstOutputTimeoutSeconds)
		require.True(t, *policy.CooldownEnabled)
		require.Equal(t, 90*time.Second, *policy.CooldownDefaultTTL)
		require.Equal(t, 2*time.Hour, *policy.CooldownMaxTTL)
		require.Equal(t, 250*time.Millisecond, *policy.CooldownRedisTimeout)
		require.True(t, *policy.CooldownFailOpen)
		require.Equal(t, 100, *policy.CooldownBatchReadMax)
		require.False(t, *policy.SwitchOrderRecordChain)
		require.False(t, *policy.SwitchOrderClearStickyOnAccountFailure)
		require.Equal(t, 1, *policy.MaxSelectionRetries)
		require.Equal(t, 3, *policy.MaxAccountSwitches)
		require.False(t, *policy.SwitchableUseSemanticBytes)
		require.Equal(t, []string{"pay"}, *policy.IdempotencyBlockedTools)
		require.False(t, *policy.T3OnlyExtend)
		require.True(t, *policy.TerminalMixedStatus503)
		require.True(t, *policy.CostAttemptsEnabled)
	})

	t.Run("空对象是合法覆盖（全 nil，不覆盖任何字段）", func(t *testing.T) {
		policy, err := ParseGroupFailoverPolicyJSON([]byte(`{}`))
		require.NoError(t, err)
		require.NotNil(t, policy)
		require.False(t, policy.HasAnyField())
	})

	t.Run("显式 false 与缺省可区分", func(t *testing.T) {
		policy, err := ParseGroupFailoverPolicyJSON([]byte(`{"failover_on_400": false}`))
		require.NoError(t, err)
		require.NotNil(t, policy.FailoverOn400)
		require.False(t, *policy.FailoverOn400)
	})

	t.Run("status_classes 含 advice.cooldown 时长", func(t *testing.T) {
		raw := []byte(`{"status_classes": [
			{"class": "auth_invalid", "codes": [401, 403], "advice": {"failover": true, "cooldown": "60s", "scope": "account"}},
			{"class": "capacity_shed", "codes": [503]}
		]}`)

		policy, err := ParseGroupFailoverPolicyJSON(raw)
		require.NoError(t, err)
		require.NotNil(t, policy.StatusClasses)
		rules := *policy.StatusClasses
		require.Len(t, rules, 2)
		require.Equal(t, "auth_invalid", rules[0].Class)
		require.Equal(t, []int{401, 403}, rules[0].Codes)
		require.NotNil(t, rules[0].Advice)
		require.True(t, *rules[0].Advice.Failover)
		require.NotNil(t, rules[0].Advice.Cooldown)
		require.Equal(t, 60*time.Second, *rules[0].Advice.Cooldown)
		require.Equal(t, "account", *rules[0].Advice.Scope)
		require.Nil(t, rules[1].Advice)
	})

	t.Run("未知键必须报错（配置漂移防护）", func(t *testing.T) {
		_, err := ParseGroupFailoverPolicyJSON([]byte(`{"cooldwon": {"enabled": true}}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "parse group failover policy json")
	})

	t.Run("非法 duration 报错且带字段名", func(t *testing.T) {
		_, err := ParseGroupFailoverPolicyJSON([]byte(`{"cooldown": {"default_ttl": "90"}}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "cooldown.default_ttl")
	})

	t.Run("非法 JSON 报错", func(t *testing.T) {
		_, err := ParseGroupFailoverPolicyJSON([]byte(`{`))
		require.Error(t, err)
	})
}

func TestParseAccountFailoverPolicyNotImplementedYet(t *testing.T) {
	// T6 之前账号级覆盖不开启：任何凭据都返回 nil（= 该层未覆盖）。
	require.Nil(t, ParseAccountFailoverPolicy(nil))
	require.Nil(t, ParseAccountFailoverPolicy(map[string]any{"failover_policy": map[string]any{"enabled": false}}))
}
