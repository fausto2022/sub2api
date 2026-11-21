package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 分组级 failover 覆盖列的只读仓储（implementation.md T0.2）。
//
// 为什么走原生 SQL 而不是 ent：groups 的既有扩列先例
// `opsmgmt_min_margin_basis_points` 就是纯原生 SQL（repository/ops_mgmt_repo.go），
// `service.Group` 与 ent/schema/group.go 都没有该字段。给 groups 加 ent 字段要重跑
// codegen 并改写 30+ 生成文件，diff 噪声与冲突风险远大于收益（§0.2-B）。
//
// 只读：写入（管理端改分组策略）不在 P0 范围，后续阶段再加。
type groupFailoverPolicyRepository struct {
	db *sql.DB
}

// NewGroupFailoverPolicyRepository 构造分组级 failover 覆盖的只读仓储。
//
// 注意：P0 阶段**不注册**到 repository/wire.go —— 地基不允许接入任何调用点，
// 且注入需要同时改 wire_gen.go（当前工作树已有未提交的 wire 改动）。接线在
// 读取方所在的阶段随调用点一起做。
func NewGroupFailoverPolicyRepository(db *sql.DB) service.GroupFailoverPolicyReader {
	return &groupFailoverPolicyRepository{db: db}
}

var _ service.GroupFailoverPolicyReader = (*groupFailoverPolicyRepository)(nil)

// groupFailoverPolicyColumns 是迁移 254 新增的三列。列名写死而不是 `SELECT *`：
// 一是避免未来加列时扫描列数不匹配，二是让「只读这三列」在代码里可审计。
const groupFailoverPolicyColumns = `failover_on_400, failover_max_account_switches, failover_policy`

func (r *groupFailoverPolicyRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("group failover policy repository is not configured")
	}
	return nil
}

// LoadGroupFailoverPolicy 读取一个分组的覆盖行。
//
// 分组不存在（sql.ErrNoRows）返回 (nil, nil)：调用方据此判定「该分组没有任何
// 覆盖」，直接回落到全局层。JSONB 解析失败返回 error —— 由调用方决定 fail-open
// 到全局还是上报，仓储层不替它决策。
func (r *groupFailoverPolicyRepository) LoadGroupFailoverPolicy(
	ctx context.Context,
	groupID int64,
) (*service.GroupFailoverPolicyRow, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	var (
		on400            sql.NullBool
		maxAccountSwitch sql.NullInt64
		rawPolicy        []byte
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT `+groupFailoverPolicyColumns+` FROM groups WHERE id = $1`,
		groupID,
	).Scan(&on400, &maxAccountSwitch, &rawPolicy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load group failover policy: %w", err)
	}

	row := &service.GroupFailoverPolicyRow{GroupID: groupID}
	if on400.Valid {
		value := on400.Bool
		row.On400 = &value
	}
	if maxAccountSwitch.Valid {
		value := int(maxAccountSwitch.Int64)
		row.MaxAccountSwitches = &value
	}
	policy, err := service.ParseGroupFailoverPolicyJSON(rawPolicy)
	if err != nil {
		return nil, fmt.Errorf("load group failover policy: %w", err)
	}
	row.Policy = policy
	return row, nil
}
