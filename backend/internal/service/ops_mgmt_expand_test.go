package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 扩池 = 对接渠道（design.md §2.6，v3.0 语义）单测。
//
// 覆盖点：
//   - (site_id, remote_group_id, api_key_hash) 去重：重复导入直接跳过，不重复建 Channel；
//   - 参数校验与站点/分组存在性；
//   - 异步任务带进度（逐条 item + 汇总计数）；
//   - Channel 名净化（只保留安全字符、长度上限 100）。
//
// 真正的 Channel 落库由 *ChannelService 负责（需要数据库），因此这里只覆盖
// 「创建前的判定与任务编排」，创建成功路径由 handler 契约测试 + 集成环境覆盖。

func TestOpsUpstreamAPIKeyHashIsStableAndOpaque(t *testing.T) {
	t.Parallel()

	hash := OpsUpstreamAPIKeyHash("sk-upstream-123")
	require.Len(t, hash, 64, "SHA-256 十六进制")
	require.Equal(t, hash, OpsUpstreamAPIKeyHash("  sk-upstream-123  "), "去空白后哈希一致")
	require.NotEqual(t, hash, OpsUpstreamAPIKeyHash("sk-upstream-124"))
	require.NotContains(t, hash, "sk-upstream", "哈希不得包含明文前缀")
}

func TestOpsExpandSkipsAlreadyConnected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newFakeOpsRepo()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	site := repo.seedSite("up-x", "https://up-x.example.com")
	repo.seedUpstreamGroup(site.ID, 11, 0.9, now)
	repo.seedLocalGroup(5, "claude-pro", 1.0)
	repo.seedChannel(77, site.ID, 11, 5)

	svc := NewOpsManagementService(repo, fakeOpsEncryptor{}, NewOpsUpstreamClient(nil), nil)
	svc.SetClock(func() time.Time { return now })

	keyHash := OpsUpstreamAPIKeyHash("sk-dup")
	repo.mu.Lock()
	repo.channels[77].UpstreamKeyHash = keyHash
	repo.mu.Unlock()

	result, err := svc.Expand(ctx, OpsExpandInput{
		SiteID:         site.ID,
		RemoteGroupID:  11,
		TargetGroupID:  5,
		UpstreamAPIKey: "sk-dup",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Skipped, "同 (站点, 上游分组, 上游 Key) 必须跳过")
	require.False(t, result.Created)
	require.Equal(t, int64(77), result.ChannelID)
	require.Contains(t, result.Message, "already connected")
}

func TestOpsExpandValidatesInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newFakeOpsRepo()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	site := repo.seedSite("up-y", "https://up-y.example.com")
	repo.seedUpstreamGroup(site.ID, 11, 0.9, now)
	repo.seedLocalGroup(5, "claude-pro", 1.0)

	svc := NewOpsManagementService(repo, fakeOpsEncryptor{}, NewOpsUpstreamClient(nil), nil)
	svc.SetClock(func() time.Time { return now })

	// 缺 site/upstream group/target group。
	_, err := svc.Expand(ctx, OpsExpandInput{})
	require.ErrorIs(t, err, ErrOpsExpandInvalidInput)

	// 站点不存在。
	_, err = svc.Expand(ctx, OpsExpandInput{SiteID: 9999, RemoteGroupID: 11, TargetGroupID: 5, UpstreamAPIKey: "sk"})
	require.ErrorIs(t, err, ErrOpsSiteNotFound)

	// 上游分组快照不存在。
	_, err = svc.Expand(ctx, OpsExpandInput{SiteID: site.ID, RemoteGroupID: 404, TargetGroupID: 5, UpstreamAPIKey: "sk"})
	require.ErrorIs(t, err, ErrOpsUpstreamGroupNotFound)

	// 本地目标分组不存在。
	_, err = svc.Expand(ctx, OpsExpandInput{SiteID: site.ID, RemoteGroupID: 11, TargetGroupID: 404, UpstreamAPIKey: "sk"})
	require.ErrorIs(t, err, ErrOpsExpandInvalidInput)

	// 没有 URL Key 且站点未配置上游 Key → 参数不足。
	_, err = svc.Expand(ctx, OpsExpandInput{SiteID: site.ID, RemoteGroupID: 11, TargetGroupID: 5})
	require.ErrorIs(t, err, ErrOpsExpandInvalidInput)

	// 参数齐备但没有 ChannelService（未注入）→ 明确报错而不是静默失败。
	_, err = svc.Expand(ctx, OpsExpandInput{SiteID: site.ID, RemoteGroupID: 11, TargetGroupID: 5, UpstreamAPIKey: "sk"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel service is not configured")
}

func TestOpsExpandBatchAsyncTracksProgress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newFakeOpsRepo()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	site := repo.seedSite("up-z", "https://up-z.example.com")
	repo.seedUpstreamGroup(site.ID, 11, 0.9, now)
	repo.seedUpstreamGroup(site.ID, 12, 0.8, now)
	repo.seedLocalGroup(5, "claude-pro", 1.0)

	svc := NewOpsManagementService(repo, fakeOpsEncryptor{}, NewOpsUpstreamClient(nil), nil)
	svc.SetClock(func() time.Time { return now })

	task, err := svc.ExpandBatchAsync(ctx, []OpsExpandInput{
		{SiteID: site.ID, RemoteGroupID: 11, TargetGroupID: 5, UpstreamAPIKey: "sk-a"},
		{SiteID: site.ID, RemoteGroupID: 12, TargetGroupID: 5, UpstreamAPIKey: "sk-b"},
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, OpsTaskKindExpand, task.Kind)
	require.Equal(t, 2, task.Total)

	// 等待异步任务结束（未注入 ChannelService → 每条都失败，但进度必须齐）。
	final := waitOpsTask(t, svc, task.ID)
	require.Equal(t, OpsTaskStatusFailed, final.Status)
	require.Len(t, final.Items, 2, "每条上游分组一条进度")
	require.Equal(t, 2, final.Progress)
	require.Equal(t, "failed", final.Items[0].Status)
	require.Contains(t, final.Items[0].Message, "channel service is not configured")
	require.NotNil(t, final.FinishedAt)

	// 任务列表按创建时间倒序可见。
	tasks := svc.ListTasks(10)
	require.NotEmpty(t, tasks)
	require.Equal(t, task.ID, tasks[0].ID)

	// 未知任务返回 ErrOpsTaskNotFound。
	_, err = svc.GetTask("missing-task")
	require.ErrorIs(t, err, ErrOpsTaskNotFound)

	// 空批量/超限直接拒绝。
	_, err = svc.ExpandBatchAsync(ctx, nil)
	require.ErrorIs(t, err, ErrOpsExpandInvalidInput)
	tooMany := make([]OpsExpandInput, 51)
	_, err = svc.ExpandBatchAsync(ctx, tooMany)
	require.ErrorIs(t, err, ErrOpsExpandInvalidInput)
}

func waitOpsTask(t *testing.T, svc *OpsManagementService, id string) *OpsTask {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := svc.GetTask(id)
		require.NoError(t, err)
		if task.Status == OpsTaskStatusSucceeded || task.Status == OpsTaskStatusFailed {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ops task %s did not finish in time", id)
	return nil
}

func TestOpsSanitizeChannelName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "ups-site-11", sanitizeOpsChannelName("ups-site-11"))
	require.Equal(t, "ups-site-11", sanitizeOpsChannelName("  ups/site 11  "), "非法字符折叠为单个连字符并去尾")
	require.Equal(t, "ups.claude_pro-12", sanitizeOpsChannelName("ups.claude_pro-12"), "点与下划线是白名单字符，保留")
	require.Equal(t, "", sanitizeOpsChannelName("   "))
	long := sanitizeOpsChannelName(string(make([]byte, 0)) + "abcdefghij" + "abcdefghij" + "abcdefghij" +
		"abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" +
		"abcdefghij" + "abcdefghij")
	require.Len(t, long, 100, "Channel 名长度上限 100")
}

func TestOpsTaskRegistryIsBoundedAndPrunesExpired(t *testing.T) {
	t.Parallel()

	registry := newOpsTaskRegistry()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	registry.maxSize = 2

	first := registry.create(OpsTaskKindSync, 1, 1)
	second := registry.create(OpsTaskKindSync, 2, 1)
	require.NotEmpty(t, first.ID)
	require.NotEqual(t, first.ID, second.ID)

	// 超过容量时淘汰最旧的。
	third := registry.create(OpsTaskKindSync, 3, 1)
	require.Nil(t, registry.get(first.ID))
	require.NotNil(t, registry.get(third.ID))
	require.Len(t, registry.list(10), 2)

	// 完成任务 + TTL 过期后被清理。
	registry.complete(second.ID, "done", map[string]any{"ok": true})
	completed := registry.get(second.ID)
	require.Equal(t, OpsTaskStatusSucceeded, completed.Status)
	require.Equal(t, 1, completed.Progress)

	now = now.Add(opsTaskRegistryTTL + time.Hour)
	_ = registry.create(OpsTaskKindSync, 4, 1)
	require.Nil(t, registry.get(second.ID), "超过 TTL 的已完成任务被清理")
}
