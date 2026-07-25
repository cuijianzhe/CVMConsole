package user

import (
	"strings"
	"sync"
	"time"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/snapshot"
	"kvm_console/utils"
)

// 防抖机制：避免并发刷新
var (
	quotaRefreshMu       sync.Mutex
	lastQuotaRefreshTime time.Time
	quotaRefreshRunning  bool
)

// AsyncRefreshQuotaSnapshots 异步刷新配额快照（带防抖，最少间隔 30 秒）
func AsyncRefreshQuotaSnapshots() {
	quotaRefreshMu.Lock()
	if quotaRefreshRunning {
		// 正在刷新中，跳过
		quotaRefreshMu.Unlock()
		return
	}
	if !lastQuotaRefreshTime.IsZero() && time.Since(lastQuotaRefreshTime) < 30*time.Second {
		// 距离上次刷新不到 30 秒，跳过
		quotaRefreshMu.Unlock()
		return
	}
	quotaRefreshRunning = true
	quotaRefreshMu.Unlock()

	go func() {
		defer func() {
			quotaRefreshMu.Lock()
			quotaRefreshRunning = false
			lastQuotaRefreshTime = time.Now()
			quotaRefreshMu.Unlock()
		}()

		if err := RefreshAllUserQuotaSnapshots(); err != nil {
			logger.App.Warn("异步刷新配额缓存失败", "error", err)
		}
	}()
}

// getVMResourcesConcurrently 获取 VM 资源（已在 core.go 实现，这里不重复定义）

// refreshUserQuotaSnapshot 计算并保存单个用户的配额快照
// 返回更新后的快照对象，用于填充 VMUserInfo.Quota
// 所有用户（包括 admin）的配额都会保存到数据库
func refreshUserQuotaSnapshot(u model.User, validDomains map[string]bool, vmResourceCache map[string]struct {
	cpu  int
	mem  int
	disk int
}) (*model.UserQuotaSnapshot, *QuotaUsage, error) {
	now := time.Now()

	// admin 用户也计算并保存配额（复用 GetUserQuotaUsage）
	if u.Role == "admin" {
		quota, err := GetUserQuotaUsage(u.Username)
		if err != nil {
			return nil, nil, err
		}
		// 保存到数据库
		snapshot := &model.UserQuotaSnapshot{
			UserID:                  u.ID,
			Username:                u.Username,
			UsedCPU:                 quota.UsedCPU,
			UsedMemory:              quota.UsedMemory,
			UsedDisk:                quota.UsedDisk,
			UsedVM:                  quota.UsedVM,
			UsedPublicIPs:           quota.UsedPublicIPs,
			UsedPortForwards:        quota.UsedPortForwards,
			UsedSnapshots:           quota.UsedSnapshots,
			UsedStorage:             quota.UsedStorage,
			UsedStorageGB:           quota.UsedStorageGB,
			UsedRuntimeSeconds:      quota.UsedRuntimeSeconds,
			UsedRuntimeDisplay:      quota.UsedRuntimeDisplay,
			RemainingRuntimeSeconds: quota.RemainingRuntimeSeconds,
			RemainingRuntimeDisplay: quota.RemainingRuntimeDisplay,
			RuntimeQuotaReached:     quota.RuntimeQuotaReached,
			UsedTrafficDown:         quota.UsedTrafficDown,
			UsedTrafficUp:           quota.UsedTrafficUp,
			UsedTrafficDownGB:       quota.UsedTrafficDownGB,
			UsedTrafficUpGB:         quota.UsedTrafficUpGB,
			IsLimitedDown:           quota.IsLimitedDown,
			IsLimitedUp:             quota.IsLimitedUp,
			LastRefreshedAt:         &now,
		}
		saveQuotaSnapshot(snapshot)
		return snapshot, quota, nil
	}

	vms := GetUserVMList(u.Username)

	var validVMs []string
	for _, vmName := range vms {
		if validDomains[vmName] {
			validVMs = append(validVMs, vmName)
		}
	}

	if !HookIsLightweightCloudType(u.CloudType) {
		quota := &QuotaUsage{
			MaxCPU:            u.MaxCPU,
			MaxMemory:         u.MaxMemory,
			MaxDisk:           u.MaxDisk,
			MaxVM:             u.MaxVM,
			MaxStorage:        u.MaxStorage,
			MaxRuntimeHours:   u.MaxRuntimeHours,
			EnablePortForward: u.EnablePortForward,
			MaxPortForwards:   u.MaxPortForwards,
			MaxSnapshots:      u.MaxSnapshots,
			MaxBandwidthUp:    u.MaxBandwidthUp,
			MaxBandwidthDown:  u.MaxBandwidthDown,
			MaxTrafficDown:    u.MaxTrafficDown,
			MaxTrafficUp:      u.MaxTrafficUp,
			MaxPublicIPs:      u.MaxPublicIPs,
			UsedVM:            len(validVMs),
		}

		for _, vmName := range validVMs {
			if res, ok := vmResourceCache[vmName]; ok {
				quota.UsedCPU += res.cpu
				quota.UsedMemory += res.mem / 1024
				quota.UsedDisk += res.disk
			}
		}

		quota.UsedPublicIPs = HookGetUserPublicIPUsage(u.Username)
		quota.UsedPortForwards = HookGetUserPortForwardUsage(u.Username)
		quota.UsedSnapshots = snapshot.CountUserSnapshots(u.Username)

		if quotaInfo, err := HookGetUserStorageUsage(u.Username); err == nil && quotaInfo != nil {
			quota.UsedStorage = quotaInfo.UsedBytes
		} else {
			isoDir := GetUserISODir(u.Username)
			shareDir := GetUserShareDir(u.Username)
			quota.UsedStorage = getDirSizeBytes(isoDir) + getDirSizeBytes(shareDir)
		}
		quota.UsedStorageGB = formatBytes(quota.UsedStorage)

		runtimeSnapshot := BuildUserRuntimeQuotaSnapshot(&u, now)
		quota.UsedRuntimeSeconds = runtimeSnapshot.UsedSeconds
		quota.UsedRuntimeDisplay = FormatRuntimeQuotaDuration(runtimeSnapshot.UsedSeconds)
		quota.RemainingRuntimeSeconds = runtimeSnapshot.RemainingSeconds
		quota.RemainingRuntimeDisplay = FormatRuntimeQuotaDuration(runtimeSnapshot.RemainingSeconds)
		quota.RuntimeQuotaReached = runtimeSnapshot.QuotaReached

		trafficInfo := HookGetUserTrafficUsage(u.Username)
		if trafficInfo != nil {
			quota.UsedTrafficDown = trafficInfo.UsedTrafficDown
			quota.UsedTrafficUp = trafficInfo.UsedTrafficUp
			quota.UsedTrafficDownGB = trafficInfo.UsedTrafficDownGB
			quota.UsedTrafficUpGB = trafficInfo.UsedTrafficUpGB
			quota.IsLimitedDown = trafficInfo.IsLimitedDown
			quota.IsLimitedUp = trafficInfo.IsLimitedUp
		}

		// 保存快照到数据库
		snapshot := &model.UserQuotaSnapshot{
			UserID:                  u.ID,
			Username:                u.Username,
			UsedCPU:                 quota.UsedCPU,
			UsedMemory:              quota.UsedMemory,
			UsedDisk:                quota.UsedDisk,
			UsedVM:                  quota.UsedVM,
			UsedPublicIPs:           quota.UsedPublicIPs,
			UsedPortForwards:        quota.UsedPortForwards,
			UsedSnapshots:           quota.UsedSnapshots,
			UsedStorage:             quota.UsedStorage,
			UsedStorageGB:           quota.UsedStorageGB,
			UsedRuntimeSeconds:      quota.UsedRuntimeSeconds,
			UsedRuntimeDisplay:      quota.UsedRuntimeDisplay,
			RemainingRuntimeSeconds: quota.RemainingRuntimeSeconds,
			RemainingRuntimeDisplay: quota.RemainingRuntimeDisplay,
			RuntimeQuotaReached:     quota.RuntimeQuotaReached,
			UsedTrafficDown:         quota.UsedTrafficDown,
			UsedTrafficUp:           quota.UsedTrafficUp,
			UsedTrafficDownGB:       quota.UsedTrafficDownGB,
			UsedTrafficUpGB:         quota.UsedTrafficUpGB,
			IsLimitedDown:           quota.IsLimitedDown,
			IsLimitedUp:             quota.IsLimitedUp,
			LightweightVMCount:      0,
			LastRefreshedAt:         &now,
		}

		saveQuotaSnapshot(snapshot)
		return snapshot, quota, nil

	} else {
		// 轻量化云类型
		var lightweightVMCount int64
		model.DB.Model(&model.LightweightVMQuota{}).Where("username = ?", u.Username).Count(&lightweightVMCount)

		snapshot := &model.UserQuotaSnapshot{
			UserID:             u.ID,
			Username:           u.Username,
			UsedVM:             int(lightweightVMCount),
			LightweightVMCount: int(lightweightVMCount),
			LastRefreshedAt:    &now,
		}

		saveQuotaSnapshot(snapshot)

		// 构建轻量级配额的 QuotaUsage
		quota := &QuotaUsage{
			MaxCPU:            u.MaxCPU,
			MaxMemory:         u.MaxMemory,
			MaxDisk:           u.MaxDisk,
			MaxVM:             u.MaxVM,
			MaxStorage:        u.MaxStorage,
			MaxRuntimeHours:   u.MaxRuntimeHours,
			EnablePortForward: u.EnablePortForward,
			MaxPortForwards:   u.MaxPortForwards,
			MaxSnapshots:      u.MaxSnapshots,
			MaxBandwidthUp:    u.MaxBandwidthUp,
			MaxBandwidthDown:  u.MaxBandwidthDown,
			MaxTrafficDown:    u.MaxTrafficDown,
			MaxTrafficUp:      u.MaxTrafficUp,
			MaxPublicIPs:      u.MaxPublicIPs,
			UsedVM:            int(lightweightVMCount),
		}
		return snapshot, quota, nil
	}
}

// saveQuotaSnapshot 保存配额快照（UPSERT 语义）
func saveQuotaSnapshot(snapshot *model.UserQuotaSnapshot) {
	var existing model.UserQuotaSnapshot
	result := model.DB.Where("user_id = ?", snapshot.UserID).First(&existing)
	if result.Error == nil {
		// 更新：使用 Updates 只更新非零值字段，保留 created_at
		updates := map[string]interface{}{
			"user_id":                  snapshot.UserID,
			"username":                 snapshot.Username,
			"used_cpu":                 snapshot.UsedCPU,
			"used_memory":              snapshot.UsedMemory,
			"used_disk":                snapshot.UsedDisk,
			"used_vm":                  snapshot.UsedVM,
			"used_public_ips":          snapshot.UsedPublicIPs,
			"used_port_forwards":       snapshot.UsedPortForwards,
			"used_snapshots":           snapshot.UsedSnapshots,
			"used_storage":             snapshot.UsedStorage,
			"used_storage_gb":          snapshot.UsedStorageGB,
			"used_runtime_seconds":     snapshot.UsedRuntimeSeconds,
			"used_runtime_display":     snapshot.UsedRuntimeDisplay,
			"remaining_runtime_seconds": snapshot.RemainingRuntimeSeconds,
			"remaining_runtime_display": snapshot.RemainingRuntimeDisplay,
			"runtime_quota_reached":    snapshot.RuntimeQuotaReached,
			"used_traffic_down":        snapshot.UsedTrafficDown,
			"used_traffic_up":          snapshot.UsedTrafficUp,
			"used_traffic_down_gb":     snapshot.UsedTrafficDownGB,
			"used_traffic_up_gb":       snapshot.UsedTrafficUpGB,
			"is_limited_down":          snapshot.IsLimitedDown,
			"is_limited_up":            snapshot.IsLimitedUp,
			"lightweight_vm_count":     snapshot.LightweightVMCount,
			"last_refreshed_at":        snapshot.LastRefreshedAt,
		}
		if err := model.DB.Model(&existing).Updates(updates).Error; err != nil {
			logger.App.Warn("更新配额快照失败", "user", snapshot.Username, "error", err)
		}
	} else {
		// 创建
		if err := model.DB.Create(snapshot).Error; err != nil {
			logger.App.Warn("创建配额快照失败", "user", snapshot.Username, "error", err)
		}
	}
}

// GetUserQuotaSnapshot 从缓存读取用户配额快照
func GetUserQuotaSnapshot(username string) *model.UserQuotaSnapshot {
	var snapshot model.UserQuotaSnapshot
	if err := model.DB.Where("username = ?", username).First(&snapshot).Error; err != nil {
		return nil
	}
	return &snapshot
}

// SnapshotToQuotaUsage 将快照转换为 QuotaUsage
func SnapshotToQuotaUsage(snapshot *model.UserQuotaSnapshot, u model.User) *QuotaUsage {
	if snapshot == nil {
		return nil
	}

	quota := &QuotaUsage{
		UsedCPU:                 snapshot.UsedCPU,
		UsedMemory:              snapshot.UsedMemory,
		UsedDisk:                snapshot.UsedDisk,
		UsedVM:                  snapshot.UsedVM,
		UsedPublicIPs:           snapshot.UsedPublicIPs,
		UsedPortForwards:        snapshot.UsedPortForwards,
		UsedSnapshots:           snapshot.UsedSnapshots,
		UsedStorage:             snapshot.UsedStorage,
		UsedStorageGB:           snapshot.UsedStorageGB,
		UsedRuntimeSeconds:      snapshot.UsedRuntimeSeconds,
		UsedRuntimeDisplay:      snapshot.UsedRuntimeDisplay,
		RemainingRuntimeSeconds: snapshot.RemainingRuntimeSeconds,
		RemainingRuntimeDisplay: snapshot.RemainingRuntimeDisplay,
		RuntimeQuotaReached:     snapshot.RuntimeQuotaReached,
		UsedTrafficDown:         snapshot.UsedTrafficDown,
		UsedTrafficUp:           snapshot.UsedTrafficUp,
		UsedTrafficDownGB:       snapshot.UsedTrafficDownGB,
		UsedTrafficUpGB:         snapshot.UsedTrafficUpGB,
		IsLimitedDown:           snapshot.IsLimitedDown,
		IsLimitedUp:             snapshot.IsLimitedUp,

		MaxCPU:            u.MaxCPU,
		MaxMemory:         u.MaxMemory,
		MaxDisk:           u.MaxDisk,
		MaxVM:             u.MaxVM,
		MaxStorage:        u.MaxStorage,
		MaxRuntimeHours:   u.MaxRuntimeHours,
		EnablePortForward: u.EnablePortForward,
		MaxPortForwards:   u.MaxPortForwards,
		MaxSnapshots:      u.MaxSnapshots,
		MaxBandwidthUp:    u.MaxBandwidthUp,
		MaxBandwidthDown:  u.MaxBandwidthDown,
		MaxTrafficDown:    u.MaxTrafficDown,
		MaxTrafficUp:      u.MaxTrafficUp,
		MaxPublicIPs:      u.MaxPublicIPs,
	}
	return quota
}

// RefreshAllUserQuotaSnapshots 刷新所有用户的配额快照（并发处理）
func RefreshAllUserQuotaSnapshots() error {
	var users []model.User
	if err := model.DB.Find(&users).Error; err != nil {
		return err
	}

	// 获取所有 VM 列表
	allDomainsResult := utils.ExecCommand("virsh", "list", "--all", "--name")
	validDomains := make(map[string]bool)
	var allVMNames []string
	if allDomainsResult.Error == nil {
		for _, name := range strings.Split(allDomainsResult.Stdout, "\n") {
			name = strings.TrimSpace(name)
			if name != "" {
				validDomains[name] = true
				allVMNames = append(allVMNames, name)
			}
		}
	}

	// 并发获取所有 VM 的资源信息
	vmResourceCache := getVMResourcesConcurrently(allVMNames)

	// 并发刷新每个用户的配额
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for _, u := range users {
		wg.Add(1)
		sem <- struct{}{}
		go func(user model.User) {
			defer wg.Done()
			defer func() { <-sem }()

			if user.Role == "admin" {
				// admin 用户不保存快照（配额计算逻辑不同）
				return
			}

			if _, _, err := refreshUserQuotaSnapshot(user, validDomains, vmResourceCache); err != nil {
				logger.App.Warn("刷新用户配额快照失败", "user", user.Username, "error", err)
			}
		}(u)
	}

	wg.Wait()
	logger.App.Info("所有用户配额快照刷新完成", "count", len(users))
	return nil
}

// EnsureQuotaSnapshotForUser 确保指定用户有配额快照（用于实时计算场景）
func EnsureQuotaSnapshotForUser(username string) {
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return
	}

	if user.Role == "admin" {
		return
	}

	// 获取所有 VM 列表
	allDomainsResult := utils.ExecCommand("virsh", "list", "--all", "--name")
	validDomains := make(map[string]bool)
	var allVMNames []string
	if allDomainsResult.Error == nil {
		for _, name := range strings.Split(allDomainsResult.Stdout, "\n") {
			name = strings.TrimSpace(name)
			if name != "" {
				validDomains[name] = true
				allVMNames = append(allVMNames, name)
			}
		}
	}

	vmResourceCache := getVMResourcesConcurrently(allVMNames)
	refreshUserQuotaSnapshot(user, validDomains, vmResourceCache)
}
