package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 运营看板纯函数边界单测（补充 ops_mgmt_dashboard_test.go 的集成路径覆盖）。
//
// 这些函数是利润口径的最小单元，边界值（0 收入、非法保底比率、亏损、浮点尾差）
// 一旦算错会直接导致看板数字误导运营决策，因此单独锁定。

// 保底收入：收入 ÷ 保底比例；非法比例回落默认 100%（不打折）。
func TestOpsGuaranteedRevenueEdgeCases(t *testing.T) {
	// 100% 保底 = 不打折，保底收入等于实际收入
	require.InDelta(t, 100.0, opsGuaranteedRevenue(100, 10_000), 1e-9)
	// 90% 保底：实际收到 90，折算回不打折口径为 100
	require.InDelta(t, 100.0, opsGuaranteedRevenue(90, 9_000), 1e-9)
	// 无收入 -> 0，且不能产生 NaN/Inf
	require.InDelta(t, 0.0, opsGuaranteedRevenue(0, 10_000), 1e-9)
	// 负收入 -> 0（防御脏数据）
	require.InDelta(t, 0.0, opsGuaranteedRevenue(-5, 10_000), 1e-9)
	// 非法比例（0 / 负数）回落默认，不除零、不放大
	require.InDelta(t, 100.0, opsGuaranteedRevenue(100, 0), 1e-9)
	require.InDelta(t, 100.0, opsGuaranteedRevenue(100, -500), 1e-9)
}

// 金额口径利润率：分母 revenue；亏损必须保留负号。
func TestOpsMarginBasisPointsMoneyEdgeCases(t *testing.T) {
	require.Equal(t, int64(3_000), opsMarginBasisPointsMoney(100, 70))   // 30%
	require.Equal(t, int64(-2_000), opsMarginBasisPointsMoney(100, 120)) // 亏损 -20%
	require.Equal(t, int64(0), opsMarginBasisPointsMoney(0, 50))         // 零收入不除零
	require.Equal(t, int64(10_000), opsMarginBasisPointsMoney(100, 0))   // 零成本 100%
	require.Equal(t, int64(0), opsMarginBasisPointsMoney(100, 100))      // 打平
}

// 金额取整：规避浮点尾差，避免看板出现 2.1420000000000003 这类数字。
func TestOpsRoundMoneyEdgeCases(t *testing.T) {
	require.InDelta(t, 2.142, roundOpsMoney(2.1420000000000003), 1e-12)
	require.InDelta(t, 0.3, roundOpsMoney(0.1+0.2), 1e-12)
	require.InDelta(t, 0.0, roundOpsMoney(0), 1e-12)
	// 负值同样稳定
	require.InDelta(t, -2.142, roundOpsMoney(-2.1420000000000003), 1e-12)
}
