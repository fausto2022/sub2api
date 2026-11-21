package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// 运营管理仓储（design.md 第二部分 v3.0）。
//
// 一律使用原生 SQL：控制对象 channels 与关联表 channel_groups 本来就不是 ent 实体
// （见 channel_repo.go），迁移 253 是表结构的唯一真源。这样避免为一个模块重新生成
// ent 全量产物，也让利润聚合能直接写 SQL。
type opsMgmtRepository struct {
	db *sql.DB
}

// NewOpsMgmtRepository 构造运营管理仓储。
func NewOpsMgmtRepository(db *sql.DB) service.OpsMgmtRepository {
	return &opsMgmtRepository{db: db}
}

var _ service.OpsMgmtRepository = (*opsMgmtRepository)(nil)

func (r *opsMgmtRepository) ready() error {
	if r == nil || r.db == nil {
		return fmt.Errorf("ops mgmt repository is not configured")
	}
	return nil
}

const opsMgmtSiteColumns = `id, name, base_url, auth_mode, username, password_enc,
	access_token_enc, refresh_token_enc, token_expires_at, upstream_api_key_enc,
	status, last_synced_at, last_error, sync_interval_minutes, created_at, updated_at`

func scanOpsMgmtSite(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsUpstreamSite, error) {
	var (
		site           service.OpsUpstreamSite
		accessEnc      sql.NullString
		refreshEnc     sql.NullString
		tokenExpires   sql.NullTime
		upstreamKeyEnc sql.NullString
		lastSynced     sql.NullTime
		lastError      sql.NullString
	)
	if err := scanner.Scan(
		&site.ID, &site.Name, &site.BaseURL, &site.AuthMode, &site.Username, &site.PasswordEnc,
		&accessEnc, &refreshEnc, &tokenExpires, &upstreamKeyEnc,
		&site.Status, &lastSynced, &lastError, &site.SyncIntervalMinutes, &site.CreatedAt, &site.UpdatedAt,
	); err != nil {
		return nil, err
	}
	site.AccessTokenEnc = accessEnc.String
	site.RefreshTokenEnc = refreshEnc.String
	site.UpstreamAPIKeyEnc = upstreamKeyEnc.String
	site.LastError = lastError.String
	if tokenExpires.Valid {
		value := tokenExpires.Time
		site.TokenExpiresAt = &value
	}
	if lastSynced.Valid {
		value := lastSynced.Time
		site.LastSyncedAt = &value
	}
	return &site, nil
}

// ---------------------------------------------------------------------------
// 站点
// ---------------------------------------------------------------------------

func (r *opsMgmtRepository) ListSites(ctx context.Context) ([]service.OpsUpstreamSite, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+opsMgmtSiteColumns+` FROM opsmgmt_upstream_sites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list ops upstream sites: %w", err)
	}
	defer func() { _ = rows.Close() }()
	sites := make([]service.OpsUpstreamSite, 0, 8)
	for rows.Next() {
		site, scanErr := scanOpsMgmtSite(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan ops upstream site: %w", scanErr)
		}
		sites = append(sites, *site)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sites, nil
}

func (r *opsMgmtRepository) GetSite(ctx context.Context, id int64) (*service.OpsUpstreamSite, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+opsMgmtSiteColumns+` FROM opsmgmt_upstream_sites WHERE id = $1`, id)
	site, err := scanOpsMgmtSite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrOpsSiteNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ops upstream site: %w", err)
	}
	return site, nil
}

func (r *opsMgmtRepository) SiteNameExists(ctx context.Context, name string, excludeID int64) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM opsmgmt_upstream_sites WHERE name = $1 AND id <> $2)`,
		strings.TrimSpace(name), excludeID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check ops upstream site name: %w", err)
	}
	return exists, nil
}

func (r *opsMgmtRepository) CreateSite(ctx context.Context, site *service.OpsUpstreamSite) error {
	if err := r.ready(); err != nil {
		return err
	}
	if site == nil {
		return fmt.Errorf("site is nil")
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO opsmgmt_upstream_sites
			(name, base_url, auth_mode, username, password_enc, upstream_api_key_enc,
			 status, sync_interval_minutes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, NOW(), NOW())
		RETURNING id, created_at, updated_at`,
		site.Name, site.BaseURL, site.AuthMode, site.Username, site.PasswordEnc, site.UpstreamAPIKeyEnc,
		site.Status, site.SyncIntervalMinutes,
	).Scan(&site.ID, &site.CreatedAt, &site.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrOpsSiteNameExists
		}
		return fmt.Errorf("create ops upstream site: %w", err)
	}
	return nil
}

func (r *opsMgmtRepository) UpdateSite(ctx context.Context, site *service.OpsUpstreamSite) error {
	if err := r.ready(); err != nil {
		return err
	}
	if site == nil {
		return fmt.Errorf("site is nil")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE opsmgmt_upstream_sites SET
			name = $2, base_url = $3, auth_mode = $4, username = $5, password_enc = $6,
			upstream_api_key_enc = NULLIF($7, ''), sync_interval_minutes = $8,
			access_token_enc = NULLIF($9, ''), refresh_token_enc = NULLIF($10, ''),
			token_expires_at = $11, updated_at = NOW()
		WHERE id = $1`,
		site.ID, site.Name, site.BaseURL, site.AuthMode, site.Username, site.PasswordEnc,
		site.UpstreamAPIKeyEnc, site.SyncIntervalMinutes,
		site.AccessTokenEnc, site.RefreshTokenEnc, site.TokenExpiresAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrOpsSiteNameExists
		}
		return fmt.Errorf("update ops upstream site: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrOpsSiteNotFound
	}
	return nil
}

func (r *opsMgmtRepository) DeleteSite(ctx context.Context, id int64) error {
	if err := r.ready(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM opsmgmt_upstream_sites WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete ops upstream site: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrOpsSiteNotFound
	}
	return nil
}

func (r *opsMgmtRepository) UpdateSiteSyncState(ctx context.Context, id int64, syncedAt *time.Time, lastErr string, status string) error {
	if err := r.ready(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE opsmgmt_upstream_sites SET
			last_synced_at = COALESCE($2, last_synced_at),
			last_error = NULLIF($3, ''),
			status = $4,
			updated_at = NOW()
		WHERE id = $1`, id, syncedAt, strings.TrimSpace(lastErr), status)
	if err != nil {
		return fmt.Errorf("update ops upstream site sync state: %w", err)
	}
	return nil
}

func (r *opsMgmtRepository) UpdateSiteTokens(ctx context.Context, id int64, accessTokenEnc, refreshTokenEnc string, expiresAt *time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE opsmgmt_upstream_sites SET
			access_token_enc = NULLIF($2, ''),
			refresh_token_enc = NULLIF($3, ''),
			token_expires_at = $4,
			updated_at = NOW()
		WHERE id = $1`, id, accessTokenEnc, refreshTokenEnc, expiresAt)
	if err != nil {
		return fmt.Errorf("update ops upstream site tokens: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 上游分组
// ---------------------------------------------------------------------------

const opsMgmtGroupColumns = `id, site_id, remote_group_id, name, platform, ratio, rate_multiplier,
	peak_enabled, peak_start, peak_end, peak_multiplier, image_separate_rate, video_separate_rate,
	is_exclusive, subscription_type, min_margin_basis_points, synced_at`

func scanOpsMgmtGroup(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsUpstreamGroup, error) {
	var (
		group     service.OpsUpstreamGroup
		platform  sql.NullString
		peakStart sql.NullString
		peakEnd   sql.NullString
		subType   sql.NullString
		minMargin sql.NullInt64
	)
	if err := scanner.Scan(
		&group.ID, &group.SiteID, &group.RemoteGroupID, &group.Name, &platform, &group.Ratio,
		&group.RateMultiplier, &group.PeakEnabled, &peakStart, &peakEnd, &group.PeakMultiplier,
		&group.ImageSeparateRate, &group.VideoSeparateRate, &group.IsExclusive, &subType,
		&minMargin, &group.SyncedAt,
	); err != nil {
		return nil, err
	}
	group.Platform = platform.String
	group.PeakStart = peakStart.String
	group.PeakEnd = peakEnd.String
	group.SubscriptionType = subType.String
	if minMargin.Valid {
		value := minMargin.Int64
		group.MinMarginBasisPoints = &value
	}
	return &group, nil
}

func (r *opsMgmtRepository) ListUpstreamGroups(ctx context.Context, siteID int64) ([]service.OpsUpstreamGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+opsMgmtGroupColumns+` FROM opsmgmt_upstream_groups WHERE site_id = $1 ORDER BY remote_group_id`,
		siteID)
	if err != nil {
		return nil, fmt.Errorf("list ops upstream groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := make([]service.OpsUpstreamGroup, 0, 16)
	for rows.Next() {
		group, scanErr := scanOpsMgmtGroup(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan ops upstream group: %w", scanErr)
		}
		groups = append(groups, *group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *opsMgmtRepository) GetUpstreamGroup(ctx context.Context, siteID, remoteGroupID int64) (*service.OpsUpstreamGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+opsMgmtGroupColumns+` FROM opsmgmt_upstream_groups WHERE site_id = $1 AND remote_group_id = $2`,
		siteID, remoteGroupID)
	group, err := scanOpsMgmtGroup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrOpsUpstreamGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get ops upstream group: %w", err)
	}
	return group, nil
}

func (r *opsMgmtRepository) UpsertUpstreamGroups(ctx context.Context, siteID int64, groups []service.OpsUpstreamGroup) (map[int64]service.OpsUpstreamGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin upsert ops upstream groups: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 先读旧快照：用于计算倍率变动标记（不落库）。
	previous := make(map[int64]service.OpsUpstreamGroup, len(groups))
	rows, err := tx.QueryContext(ctx,
		`SELECT `+opsMgmtGroupColumns+` FROM opsmgmt_upstream_groups WHERE site_id = $1`, siteID)
	if err != nil {
		return nil, fmt.Errorf("load previous ops upstream groups: %w", err)
	}
	for rows.Next() {
		group, scanErr := scanOpsMgmtGroup(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan previous ops upstream group: %w", scanErr)
		}
		previous[group.RemoteGroupID] = *group
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for i := range groups {
		group := groups[i]
		// 注意：min_margin_basis_points 是人工配置，upsert 不得覆盖。
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO opsmgmt_upstream_groups
				(site_id, remote_group_id, name, platform, ratio, rate_multiplier, peak_enabled,
				 peak_start, peak_end, peak_multiplier, image_separate_rate, video_separate_rate,
				 is_exclusive, subscription_type, synced_at)
			VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10,
				 $11, $12, $13, NULLIF($14, ''), $15)
			ON CONFLICT (site_id, remote_group_id) DO UPDATE SET
				name = EXCLUDED.name,
				platform = EXCLUDED.platform,
				ratio = EXCLUDED.ratio,
				rate_multiplier = EXCLUDED.rate_multiplier,
				peak_enabled = EXCLUDED.peak_enabled,
				peak_start = EXCLUDED.peak_start,
				peak_end = EXCLUDED.peak_end,
				peak_multiplier = EXCLUDED.peak_multiplier,
				image_separate_rate = EXCLUDED.image_separate_rate,
				video_separate_rate = EXCLUDED.video_separate_rate,
				is_exclusive = EXCLUDED.is_exclusive,
				subscription_type = EXCLUDED.subscription_type,
				synced_at = EXCLUDED.synced_at`,
			siteID, group.RemoteGroupID, group.Name, group.Platform, group.Ratio, group.RateMultiplier,
			group.PeakEnabled, group.PeakStart, group.PeakEnd, group.PeakMultiplier,
			group.ImageSeparateRate, group.VideoSeparateRate, group.IsExclusive, group.SubscriptionType,
			group.SyncedAt,
		); err != nil {
			return nil, fmt.Errorf("upsert ops upstream group %d: %w", group.RemoteGroupID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit upsert ops upstream groups: %w", err)
	}
	return previous, nil
}

func (r *opsMgmtRepository) SetUpstreamGroupMinMargin(ctx context.Context, siteID, remoteGroupID int64, basisPoints *int64) error {
	if err := r.ready(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE opsmgmt_upstream_groups SET min_margin_basis_points = $3
		WHERE site_id = $1 AND remote_group_id = $2`, siteID, remoteGroupID, basisPoints)
	if err != nil {
		return fmt.Errorf("set ops upstream group min margin: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrOpsUpstreamGroupNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// 上游账号
// ---------------------------------------------------------------------------

const opsMgmtAccountColumns = `id, site_id, remote_account_id, name, platform, type, status,
	schedulable, rate_multiplier, group_ids, credentials, synced_at`

func scanOpsMgmtAccount(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsUpstreamAccount, error) {
	var (
		account        service.OpsUpstreamAccount
		name           sql.NullString
		platform       sql.NullString
		kind           sql.NullString
		status         sql.NullString
		schedulable    sql.NullBool
		rateMultiplier sql.NullFloat64
		groupIDs       pq.Int64Array
		credentialsRaw []byte
	)
	if err := scanner.Scan(
		&account.ID, &account.SiteID, &account.RemoteAccountID, &name, &platform, &kind, &status,
		&schedulable, &rateMultiplier, &groupIDs, &credentialsRaw, &account.SyncedAt,
	); err != nil {
		return nil, err
	}
	account.Name = name.String
	account.Platform = platform.String
	account.Type = kind.String
	account.Status = status.String
	if schedulable.Valid {
		value := schedulable.Bool
		account.Schedulable = &value
	}
	if rateMultiplier.Valid {
		value := rateMultiplier.Float64
		account.RateMultiplier = &value
	}
	account.GroupIDs = append([]int64{}, groupIDs...)
	if len(credentialsRaw) > 0 {
		credentials := map[string]any{}
		if err := json.Unmarshal(credentialsRaw, &credentials); err == nil {
			account.Credentials = credentials
		}
	}
	return &account, nil
}

func (r *opsMgmtRepository) ListUpstreamAccounts(ctx context.Context, siteID int64) ([]service.OpsUpstreamAccount, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+opsMgmtAccountColumns+` FROM opsmgmt_upstream_accounts WHERE site_id = $1 ORDER BY remote_account_id`,
		siteID)
	if err != nil {
		return nil, fmt.Errorf("list ops upstream accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	accounts := make([]service.OpsUpstreamAccount, 0, 16)
	for rows.Next() {
		account, scanErr := scanOpsMgmtAccount(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan ops upstream account: %w", scanErr)
		}
		accounts = append(accounts, *account)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}

func (r *opsMgmtRepository) ReplaceUpstreamAccounts(ctx context.Context, siteID int64, accounts []service.OpsUpstreamAccount) error {
	if err := r.ready(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace ops upstream accounts: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	kept := make([]int64, 0, len(accounts))
	for i := range accounts {
		account := accounts[i]
		kept = append(kept, account.RemoteAccountID)
		var credentials any
		if len(account.Credentials) > 0 {
			raw, marshalErr := json.Marshal(account.Credentials)
			if marshalErr != nil {
				return fmt.Errorf("encode credentials for account %d: %w", account.RemoteAccountID, marshalErr)
			}
			credentials = raw
		}
		groupIDs := account.GroupIDs
		if groupIDs == nil {
			groupIDs = []int64{}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO opsmgmt_upstream_accounts
				(site_id, remote_account_id, name, platform, type, status, schedulable,
				 rate_multiplier, group_ids, credentials, synced_at)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
				 $7, $8, $9, $10, $11)
			ON CONFLICT (site_id, remote_account_id) DO UPDATE SET
				name = EXCLUDED.name,
				platform = EXCLUDED.platform,
				type = EXCLUDED.type,
				status = EXCLUDED.status,
				schedulable = EXCLUDED.schedulable,
				rate_multiplier = EXCLUDED.rate_multiplier,
				group_ids = EXCLUDED.group_ids,
				credentials = EXCLUDED.credentials,
				synced_at = EXCLUDED.synced_at`,
			siteID, account.RemoteAccountID, account.Name, account.Platform, account.Type, account.Status,
			account.Schedulable, account.RateMultiplier, pq.Array(groupIDs), credentials, account.SyncedAt,
		); err != nil {
			return fmt.Errorf("upsert ops upstream account %d: %w", account.RemoteAccountID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM opsmgmt_upstream_accounts WHERE site_id = $1 AND NOT (remote_account_id = ANY($2))`,
		siteID, pq.Array(kept),
	); err != nil {
		return fmt.Errorf("prune stale ops upstream accounts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace ops upstream accounts: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 利润检查
// ---------------------------------------------------------------------------

const opsMgmtProfitCheckColumns = `id, site_id, remote_group_id, group_name, channel_ref,
	sale_multiplier_micros, cost_multiplier_micros, revenue_micros, cost_micros,
	margin_basis_points, status, cost_source, cost_observed_at, cost_reliable, cost_reason, created_at`

func scanOpsMgmtProfitCheck(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsProfitCheck, error) {
	var (
		check     service.OpsProfitCheck
		groupName sql.NullString
		channel   sql.NullString
		source    sql.NullString
		observed  sql.NullTime
		reason    sql.NullString
	)
	if err := scanner.Scan(
		&check.ID, &check.SiteID, &check.RemoteGroupID, &groupName, &channel,
		&check.SaleMultiplierMicros, &check.CostMultiplierMicros, &check.RevenueMicros, &check.CostMicros,
		&check.MarginBasisPoints, &check.Status, &source, &observed, &check.CostReliable, &reason, &check.CreatedAt,
	); err != nil {
		return nil, err
	}
	check.GroupName = groupName.String
	check.ChannelRef = channel.String
	check.CostSource = source.String
	check.CostReason = reason.String
	if observed.Valid {
		value := observed.Time
		check.CostObservedAt = &value
	}
	return &check, nil
}

func (r *opsMgmtRepository) AppendProfitCheck(ctx context.Context, check *service.OpsProfitCheck) error {
	if err := r.ready(); err != nil {
		return err
	}
	if check == nil {
		return fmt.Errorf("profit check is nil")
	}
	createdAt := check.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO opsmgmt_profit_checks
			(site_id, remote_group_id, group_name, channel_ref, sale_multiplier_micros,
			 cost_multiplier_micros, revenue_micros, cost_micros, margin_basis_points, status,
			 cost_source, cost_observed_at, cost_reliable, cost_reason, created_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8, $9, $10,
			 NULLIF($11, ''), $12, $13, NULLIF($14, ''), $15)
		RETURNING id`,
		check.SiteID, check.RemoteGroupID, check.GroupName, check.ChannelRef, check.SaleMultiplierMicros,
		check.CostMultiplierMicros, check.RevenueMicros, check.CostMicros, check.MarginBasisPoints, check.Status,
		check.CostSource, check.CostObservedAt, check.CostReliable, check.CostReason, createdAt,
	).Scan(&check.ID)
	if err != nil {
		return fmt.Errorf("append ops profit check: %w", err)
	}
	check.CreatedAt = createdAt
	return nil
}

func (r *opsMgmtRepository) ListProfitChecks(ctx context.Context, filter service.OpsProfitCheckFilter) ([]service.OpsProfitCheck, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 6)
	add := func(clause string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(clause, len(args)))
	}
	if filter.SiteID > 0 {
		add("site_id = $%d", filter.SiteID)
	}
	if filter.RemoteGroupID > 0 {
		add("remote_group_id = $%d", filter.RemoteGroupID)
	}
	if ref := strings.TrimSpace(filter.ChannelRef); ref != "" {
		add("channel_ref = $%d", ref)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		add("status = $%d", status)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM opsmgmt_profit_checks`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ops profit checks: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT %s FROM opsmgmt_profit_checks%s ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d`,
		opsMgmtProfitCheckColumns, where, len(args)+1, len(args)+2,
	), queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list ops profit checks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	checks := make([]service.OpsProfitCheck, 0, limit)
	for rows.Next() {
		check, scanErr := scanOpsMgmtProfitCheck(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan ops profit check: %w", scanErr)
		}
		checks = append(checks, *check)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return checks, total, nil
}

func (r *opsMgmtRepository) ListRecentProfitChecks(ctx context.Context, siteID, remoteGroupID int64, channelRef string, limit int) ([]service.OpsProfitCheck, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s FROM opsmgmt_profit_checks
		WHERE site_id = $1 AND remote_group_id = $2 AND channel_ref = $3
		ORDER BY created_at DESC, id DESC LIMIT $4`, opsMgmtProfitCheckColumns),
		siteID, remoteGroupID, channelRef, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent ops profit checks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	checks := make([]service.OpsProfitCheck, 0, limit)
	for rows.Next() {
		check, scanErr := scanOpsMgmtProfitCheck(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan recent ops profit check: %w", scanErr)
		}
		checks = append(checks, *check)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return checks, nil
}

// ---------------------------------------------------------------------------
// 守护锁
// ---------------------------------------------------------------------------

const opsMgmtGuardLockColumns = `id, site_id, target_ref, lock_type, active, reason, metadata, activated_at, cleared_at`

func scanOpsMgmtGuardLock(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsGuardLock, error) {
	var (
		lock        service.OpsGuardLock
		reason      sql.NullString
		metadataRaw []byte
		clearedAt   sql.NullTime
	)
	if err := scanner.Scan(
		&lock.ID, &lock.SiteID, &lock.TargetRef, &lock.LockType, &lock.Active, &reason,
		&metadataRaw, &lock.ActivatedAt, &clearedAt,
	); err != nil {
		return nil, err
	}
	lock.Reason = reason.String
	if clearedAt.Valid {
		value := clearedAt.Time
		lock.ClearedAt = &value
	}
	if len(metadataRaw) > 0 {
		metadata := map[string]any{}
		if err := json.Unmarshal(metadataRaw, &metadata); err == nil {
			lock.Metadata = metadata
		}
	}
	return &lock, nil
}

func (r *opsMgmtRepository) ActivateGuardLock(ctx context.Context, lock *service.OpsGuardLock) (bool, *service.OpsGuardLock, error) {
	if err := r.ready(); err != nil {
		return false, nil, err
	}
	if lock == nil {
		return false, nil, fmt.Errorf("guard lock is nil")
	}
	var metadata any
	if len(lock.Metadata) > 0 {
		raw, err := json.Marshal(lock.Metadata)
		if err != nil {
			return false, nil, fmt.Errorf("encode guard lock metadata: %w", err)
		}
		metadata = raw
	}
	activatedAt := lock.ActivatedAt
	if activatedAt.IsZero() {
		activatedAt = time.Now()
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO opsmgmt_guard_locks
			(site_id, target_ref, lock_type, active, reason, metadata, activated_at)
		VALUES ($1, $2, $3, TRUE, NULLIF($4, ''), $5, $6)
		ON CONFLICT (site_id, target_ref, lock_type) WHERE active DO NOTHING
		RETURNING id`,
		lock.SiteID, lock.TargetRef, lock.LockType, lock.Reason, metadata, activatedAt,
	).Scan(&id)
	if err == nil {
		lock.ID = id
		lock.Active = true
		lock.ActivatedAt = activatedAt
		return true, lock, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, nil, fmt.Errorf("activate ops guard lock: %w", err)
	}
	// 已存在 active 锁：返回既有锁（幂等）。
	existing, getErr := r.GetActiveGuardLock(ctx, lock.SiteID, lock.TargetRef, lock.LockType)
	if getErr != nil {
		return false, nil, getErr
	}
	return false, existing, nil
}

func (r *opsMgmtRepository) ClearGuardLock(ctx context.Context, siteID int64, targetRef, lockType string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE opsmgmt_guard_locks SET active = FALSE, cleared_at = NOW()
		WHERE site_id = $1 AND target_ref = $2 AND lock_type = $3 AND active`,
		siteID, targetRef, lockType)
	if err != nil {
		return false, fmt.Errorf("clear ops guard lock: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *opsMgmtRepository) ListActiveGuardLocks(ctx context.Context, siteID int64) ([]service.OpsGuardLock, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT %s FROM opsmgmt_guard_locks WHERE active`, opsMgmtGuardLockColumns)
	args := []any{}
	if siteID > 0 {
		query += ` AND site_id = $1`
		args = append(args, siteID)
	}
	query += ` ORDER BY activated_at DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list active ops guard locks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	locks := make([]service.OpsGuardLock, 0, 4)
	for rows.Next() {
		lock, scanErr := scanOpsMgmtGuardLock(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan active ops guard lock: %w", scanErr)
		}
		locks = append(locks, *lock)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return locks, nil
}

func (r *opsMgmtRepository) GetActiveGuardLock(ctx context.Context, siteID int64, targetRef, lockType string) (*service.OpsGuardLock, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM opsmgmt_guard_locks
		WHERE site_id = $1 AND target_ref = $2 AND lock_type = $3 AND active
		ORDER BY activated_at DESC LIMIT 1`, opsMgmtGuardLockColumns),
		siteID, targetRef, lockType)
	lock, err := scanOpsMgmtGuardLock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active ops guard lock: %w", err)
	}
	return lock, nil
}

// ---------------------------------------------------------------------------
// 运营配置
// ---------------------------------------------------------------------------

func (r *opsMgmtRepository) GetOpsSettings(ctx context.Context) (map[string]string, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT key, value::text FROM opsmgmt_settings`)
	if err != nil {
		return nil, fmt.Errorf("get ops mgmt settings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make(map[string]string, 8)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan ops mgmt setting: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func (r *opsMgmtRepository) SetOpsSettings(ctx context.Context, values map[string]string) error {
	if err := r.ready(); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set ops mgmt settings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO opsmgmt_settings (key, value, updated_at)
			VALUES ($1, $2::jsonb, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
			key, value,
		); err != nil {
			return fmt.Errorf("set ops mgmt setting %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set ops mgmt settings: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 本地 Channel 的运营标签
// ---------------------------------------------------------------------------

const opsMgmtChannelColumns = `c.id, c.name, c.status, c.opsmgmt_site_id, c.opsmgmt_remote_group_id,
	c.opsmgmt_upstream_key_hash, c.opsmgmt_auto_disabled_at, c.opsmgmt_auto_disabled_reason,
	c.created_at, c.updated_at`

func scanOpsMgmtChannelLink(scanner interface {
	Scan(dest ...any) error
}) (*service.OpsChannelLink, error) {
	var (
		link            service.OpsChannelLink
		siteID          sql.NullInt64
		remoteGroupID   sql.NullInt64
		upstreamKeyHash sql.NullString
		autoDisabledAt  sql.NullTime
		autoDisabledWhy sql.NullString
	)
	if err := scanner.Scan(
		&link.ChannelID, &link.ChannelName, &link.Status, &siteID, &remoteGroupID,
		&upstreamKeyHash, &autoDisabledAt, &autoDisabledWhy, &link.CreatedAt, &link.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if siteID.Valid {
		value := siteID.Int64
		link.SiteID = &value
	}
	if remoteGroupID.Valid {
		value := remoteGroupID.Int64
		link.RemoteGroupID = &value
	}
	link.UpstreamKeyHash = upstreamKeyHash.String
	link.AutoDisabledReason = autoDisabledWhy.String
	if autoDisabledAt.Valid {
		value := autoDisabledAt.Time
		link.AutoDisabledAt = &value
	}
	return &link, nil
}

func (r *opsMgmtRepository) SetChannelOpsLink(ctx context.Context, channelID, siteID, remoteGroupID int64, upstreamKeyEnc, upstreamKeyHash string) error {
	if err := r.ready(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE channels SET
			opsmgmt_site_id = $2,
			opsmgmt_remote_group_id = $3,
			opsmgmt_upstream_key_enc = NULLIF($4, ''),
			opsmgmt_upstream_key_hash = NULLIF($5, ''),
			updated_at = NOW()
		WHERE id = $1`, channelID, siteID, remoteGroupID, upstreamKeyEnc, upstreamKeyHash)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrOpsExpandDuplicate
		}
		return fmt.Errorf("set channel ops link: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrOpsChannelNotFound
	}
	return nil
}

func (r *opsMgmtRepository) GetChannelLink(ctx context.Context, channelID int64) (*service.OpsChannelLink, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+opsMgmtChannelColumns+` FROM channels c WHERE c.id = $1`, channelID)
	link, err := scanOpsMgmtChannelLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrOpsChannelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get channel ops link: %w", err)
	}
	groupIDs, err := r.channelGroupIDs(ctx, channelID)
	if err != nil {
		return nil, err
	}
	link.GroupIDs = groupIDs
	return link, nil
}

func (r *opsMgmtRepository) ListChannelLinksByUpstreamGroup(ctx context.Context, siteID, remoteGroupID int64) ([]service.OpsChannelLink, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	return r.queryChannelLinks(ctx, `c.opsmgmt_site_id = $1 AND c.opsmgmt_remote_group_id = $2`, siteID, remoteGroupID)
}

func (r *opsMgmtRepository) ListAutoDisabledChannels(ctx context.Context) ([]service.OpsChannelLink, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	return r.queryChannelLinks(ctx, `c.opsmgmt_auto_disabled_at IS NOT NULL`)
}

func (r *opsMgmtRepository) FindChannelLinkByUpstreamKey(ctx context.Context, siteID, remoteGroupID int64, upstreamKeyHash string) (*service.OpsChannelLink, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	links, err := r.queryChannelLinks(ctx,
		`c.opsmgmt_site_id = $1 AND c.opsmgmt_remote_group_id = $2 AND c.opsmgmt_upstream_key_hash = $3`,
		siteID, remoteGroupID, upstreamKeyHash)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	return &links[0], nil
}

func (r *opsMgmtRepository) queryChannelLinks(ctx context.Context, where string, args ...any) ([]service.OpsChannelLink, error) {
	query := fmt.Sprintf(`SELECT %s FROM channels c WHERE %s ORDER BY c.id`, opsMgmtChannelColumns, where)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query channel ops links: %w", err)
	}
	defer func() { _ = rows.Close() }()
	links := make([]service.OpsChannelLink, 0, 4)
	ids := make([]int64, 0, 4)
	for rows.Next() {
		link, scanErr := scanOpsMgmtChannelLink(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan channel ops link: %w", scanErr)
		}
		links = append(links, *link)
		ids = append(ids, link.ChannelID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return links, nil
	}
	groupMap, err := r.channelGroupIDsBatch(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range links {
		links[i].GroupIDs = groupMap[links[i].ChannelID]
	}
	return links, nil
}

func (r *opsMgmtRepository) channelGroupIDs(ctx context.Context, channelID int64) ([]int64, error) {
	groupMap, err := r.channelGroupIDsBatch(ctx, []int64{channelID})
	if err != nil {
		return nil, err
	}
	return groupMap[channelID], nil
}

func (r *opsMgmtRepository) channelGroupIDsBatch(ctx context.Context, channelIDs []int64) (map[int64][]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT channel_id, group_id FROM channel_groups WHERE channel_id = ANY($1) ORDER BY group_id`,
		pq.Array(channelIDs))
	if err != nil {
		return nil, fmt.Errorf("query channel groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64][]int64, len(channelIDs))
	for rows.Next() {
		var channelID, groupID int64
		if err := rows.Scan(&channelID, &groupID); err != nil {
			return nil, fmt.Errorf("scan channel group: %w", err)
		}
		out[channelID] = append(out[channelID], groupID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *opsMgmtRepository) SetChannelAutoDisabled(ctx context.Context, channelID int64, at time.Time, reason string, disabled bool) error {
	if err := r.ready(); err != nil {
		return err
	}
	status := "active"
	var disabledAt any
	var reasonValue any
	if disabled {
		status = "disabled"
		disabledAt = at
		reasonValue = strings.TrimSpace(reason)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE channels SET
			status = $2,
			opsmgmt_auto_disabled_at = $3,
			opsmgmt_auto_disabled_reason = NULLIF($4, ''),
			updated_at = NOW()
		WHERE id = $1`, channelID, status, disabledAt, reasonValue)
	if err != nil {
		return fmt.Errorf("set channel auto disabled: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrOpsChannelNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// 本地分组
// ---------------------------------------------------------------------------

func (r *opsMgmtRepository) ListLocalGroups(ctx context.Context, ids []int64) ([]service.OpsLocalGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []service.OpsLocalGroup{}, nil
	}
	return r.queryLocalGroups(ctx, `WHERE id = ANY($1) ORDER BY id`, pq.Array(ids))
}

func (r *opsMgmtRepository) ListAllLocalGroups(ctx context.Context) ([]service.OpsLocalGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	return r.queryLocalGroups(ctx, `ORDER BY id`)
}

func (r *opsMgmtRepository) queryLocalGroups(ctx context.Context, suffix string, args ...any) ([]service.OpsLocalGroup, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(platform, ''), rate_multiplier, opsmgmt_min_margin_basis_points FROM groups `+suffix,
		args...)
	if err != nil {
		return nil, fmt.Errorf("query local groups: %w", err)
	}
	defer func() { _ = rows.Close() }()
	groups := make([]service.OpsLocalGroup, 0, 16)
	for rows.Next() {
		var (
			group     service.OpsLocalGroup
			minMargin sql.NullInt64
		)
		if err := rows.Scan(&group.ID, &group.Name, &group.Platform, &group.RateMultiplier, &minMargin); err != nil {
			return nil, fmt.Errorf("scan local group: %w", err)
		}
		if minMargin.Valid {
			value := minMargin.Int64
			group.OpsMinMarginBasisPoints = &value
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *opsMgmtRepository) SetLocalGroupMinMargin(ctx context.Context, groupID int64, basisPoints *int64) error {
	if err := r.ready(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE groups SET opsmgmt_min_margin_basis_points = $2, updated_at = NOW() WHERE id = $1`,
		groupID, basisPoints)
	if err != nil {
		return fmt.Errorf("set local group min margin: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: group %d", service.ErrOpsInvalidSetting, groupID)
	}
	return nil
}

func (r *opsMgmtRepository) LocalGroupExists(ctx context.Context, groupID int64) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM groups WHERE id = $1)`, groupID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check local group exists: %w", err)
	}
	return exists, nil
}

// ---------------------------------------------------------------------------
// usage_logs 只读聚合（利润数据源）
// ---------------------------------------------------------------------------

func (r *opsMgmtRepository) SumUsageTotals(ctx context.Context, from, to time.Time, channelID *int64) (service.OpsUsageTotals, error) {
	if err := r.ready(); err != nil {
		return service.OpsUsageTotals{}, err
	}
	query := `SELECT COALESCE(SUM(actual_cost), 0)::float8, COALESCE(SUM(total_cost), 0)::float8,
		COUNT(*), MAX(created_at)
		FROM usage_logs WHERE created_at >= $1 AND created_at < $2`
	args := []any{from, to}
	if channelID != nil {
		query += ` AND channel_id = $3`
		args = append(args, *channelID)
	}
	var (
		totals service.OpsUsageTotals
		lastAt sql.NullTime
	)
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&totals.Revenue, &totals.Cost, &totals.Requests, &lastAt); err != nil {
		return service.OpsUsageTotals{}, fmt.Errorf("sum usage totals: %w", err)
	}
	if lastAt.Valid {
		value := lastAt.Time
		totals.LastAt = &value
	}
	return totals, nil
}

func (r *opsMgmtRepository) SumUsageByGroup(ctx context.Context, from, to time.Time) ([]service.OpsUsageGroupTotals, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_id, COALESCE(SUM(actual_cost), 0)::float8, COALESCE(SUM(total_cost), 0)::float8, COUNT(*)
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
		GROUP BY group_id
		ORDER BY 2 DESC
		LIMIT 200`, from, to)
	if err != nil {
		return nil, fmt.Errorf("sum usage by group: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.OpsUsageGroupTotals, 0, 32)
	for rows.Next() {
		var row service.OpsUsageGroupTotals
		if err := rows.Scan(&row.GroupID, &row.Revenue, &row.Cost, &row.Requests); err != nil {
			return nil, fmt.Errorf("scan usage by group: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *opsMgmtRepository) CountUsageDays(ctx context.Context, from, to time.Time) (int, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	var days int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT created_at::date)
		FROM usage_logs WHERE created_at >= $1 AND created_at < $2`, from, to).Scan(&days); err != nil {
		return 0, fmt.Errorf("count usage days: %w", err)
	}
	return days, nil
}
