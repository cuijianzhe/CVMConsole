package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/model"
)

const (
	CloudTypeElastic     = "elastic"
	CloudTypeLightweight = "lightweight"

	defaultLightweightTrafficPenaltyMbps = 1
	lightweightTrafficPenaltySettingKey  = "lightweight_traffic_penalty_mbps"
)

func lightweightTrafficPenaltyMbps() int {
	if model.DB == nil {
		return defaultLightweightTrafficPenaltyMbps
	}
	value, ok := model.GetSetting(lightweightTrafficPenaltySettingKey)
	if !ok {
		return defaultLightweightTrafficPenaltyMbps
	}
	mbps, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || mbps <= 0 {
		return defaultLightweightTrafficPenaltyMbps
	}
	return mbps
}

// LightweightVMQuotaRequest 管理员为轻量云 VM 设置的单机配额。
type LightweightVMQuotaRequest struct {
	VMName            string  `json:"vm_name"`
	TrafficDownGB     float64 `json:"traffic_down_gb"`
	TrafficUpGB       float64 `json:"traffic_up_gb"`
	BandwidthDownMbps int     `json:"bandwidth_down_mbps"`
	BandwidthUpMbps   int     `json:"bandwidth_up_mbps"`
	MaxPortForwards   int     `json:"max_port_forwards"`
	MaxSnapshots      int     `json:"max_snapshots"`
	MaxRuntimeHours   int     `json:"max_runtime_hours"`
}

func NormalizeCloudType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CloudTypeLightweight:
		return CloudTypeLightweight
	default:
		return CloudTypeElastic
	}
}

func IsLightweightCloudType(value string) bool {
	return NormalizeCloudType(value) == CloudTypeLightweight
}

func IsLightweightCloudUser(username string) bool {
	if strings.TrimSpace(username) == "" || model.DB == nil {
		return false
	}
	var user model.User
	if err := model.DB.Where("username = ?", strings.TrimSpace(username)).First(&user).Error; err != nil {
		return false
	}
	return IsLightweightCloudType(user.CloudType)
}

func IsLightweightCloudVM(vmName string) bool {
	if strings.TrimSpace(vmName) == "" || model.DB == nil {
		return false
	}
	var count int64
	model.DB.Model(&model.LightweightVMQuota{}).Where("vm_name = ?", strings.TrimSpace(vmName)).Count(&count)
	return count > 0
}

func UpdateUserCloudProfile(username, cloudType string, dedicatedVPCSwitchID uint) error {
	username = strings.TrimSpace(username)
	cloudType = NormalizeCloudType(cloudType)
	if username == "" {
		return fmt.Errorf("用户不能为空")
	}
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在: %w", err)
	}
	if user.Role == "admin" {
		cloudType = CloudTypeElastic
		dedicatedVPCSwitchID = 0
	}
	if user.Role == "user" && IsLightweightCloudType(cloudType) {
		if dedicatedVPCSwitchID == 0 {
			return fmt.Errorf("轻量云用户必须选择专用 VPC 网络")
		}
		var count int64
		if err := model.DB.Model(&model.VPCSwitch{}).
			Where("id = ? AND (bridge_mode = '' OR bridge_mode = ? OR bridge_mode IS NULL)", dedicatedVPCSwitchID, BridgeModeNAT).
			Count(&count).Error; err != nil {
			return fmt.Errorf("检查专用 VPC 网络失败: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("请选择有效的 NAT 类型专用 VPC 网络")
		}
	} else {
		dedicatedVPCSwitchID = 0
	}
	if err := model.DB.Model(&model.User{}).Where("username = ?", username).Updates(map[string]interface{}{
		"cloud_type":              cloudType,
		"dedicated_vpc_switch_id": dedicatedVPCSwitchID,
	}).Error; err != nil {
		return err
	}
	if user.Role != "user" {
		return nil
	}
	if IsLightweightCloudType(cloudType) {
		for _, vmName := range GetUserVMList(username) {
			if _, err := GetLightweightVMQuota(vmName); err != nil {
				if _, err := UpsertLightweightVMQuota(username, defaultLightweightVMQuota(vmName)); err != nil {
					return err
				}
			}
			if err := EnsureLightweightVMNetwork(username, vmName); err != nil {
				return err
			}
		}
		return nil
	}
	for _, vmName := range GetUserVMList(username) {
		CleanupVMVPCBinding(vmName)
		CleanupLightweightVMResources(vmName)
	}
	if !IsLightweightCloudType(cloudType) {
		if _, err := EnsureDefaultSecurityGroup(username); err != nil {
			return err
		}
		if _, err := EnsureDefaultVPCSwitch(username); err != nil {
			return err
		}
	}
	return nil
}

func NormalizeLightweightVMQuotaRequest(req LightweightVMQuotaRequest) LightweightVMQuotaRequest {
	req.VMName = strings.TrimSpace(req.VMName)
	if req.TrafficDownGB < 0 {
		req.TrafficDownGB = 0
	}
	if req.TrafficUpGB < 0 {
		req.TrafficUpGB = 0
	}
	if req.BandwidthDownMbps < 0 {
		req.BandwidthDownMbps = 0
	}
	if req.BandwidthUpMbps < 0 {
		req.BandwidthUpMbps = 0
	}
	if req.MaxPortForwards < 0 {
		req.MaxPortForwards = 0
	}
	if req.MaxSnapshots < 0 {
		req.MaxSnapshots = 0
	}
	if req.MaxRuntimeHours < 0 {
		req.MaxRuntimeHours = 0
	}
	return req
}

func defaultLightweightVMQuota(vmName string) LightweightVMQuotaRequest {
	return LightweightVMQuotaRequest{
		VMName:          vmName,
		MaxPortForwards: 10,
		MaxSnapshots:    2,
	}
}

func UpsertLightweightVMQuota(username string, req LightweightVMQuotaRequest) (*model.LightweightVMQuota, error) {
	username = strings.TrimSpace(username)
	req = NormalizeLightweightVMQuotaRequest(req)
	if username == "" {
		return nil, fmt.Errorf("用户不能为空")
	}
	if req.VMName == "" {
		return nil, fmt.Errorf("虚拟机名称不能为空")
	}
	var quota model.LightweightVMQuota
	err := model.DB.Where("vm_name = ?", req.VMName).First(&quota).Error
	if err == nil {
		quota.Username = username
		quota.TrafficDownGB = req.TrafficDownGB
		quota.TrafficUpGB = req.TrafficUpGB
		quota.BandwidthDownMbps = req.BandwidthDownMbps
		quota.BandwidthUpMbps = req.BandwidthUpMbps
		quota.MaxPortForwards = req.MaxPortForwards
		quota.MaxSnapshots = req.MaxSnapshots
		quota.MaxRuntimeHours = req.MaxRuntimeHours
		if err := model.DB.Save(&quota).Error; err != nil {
			return nil, err
		}
		CheckLightweightVMTrafficAfterQuotaUpdate(req.VMName)
		SyncLightweightVMRuntimeQuotaState(req.VMName, time.Now())
		RefreshVMCacheByNameAsync(req.VMName)
		return fillLightweightVMQuotaRuntime(&quota), nil
	}
	quota = model.LightweightVMQuota{
		Username:          username,
		VMName:            req.VMName,
		TrafficDownGB:     req.TrafficDownGB,
		TrafficUpGB:       req.TrafficUpGB,
		BandwidthDownMbps: req.BandwidthDownMbps,
		BandwidthUpMbps:   req.BandwidthUpMbps,
		MaxPortForwards:   req.MaxPortForwards,
		MaxSnapshots:      req.MaxSnapshots,
		MaxRuntimeHours:   req.MaxRuntimeHours,
	}
	if err := model.DB.Create(&quota).Error; err != nil {
		return nil, err
	}
	if err := ApplyLightweightVMBandwidth(req.VMName); err != nil {
		logger.App.Warn("应用 VM 带宽失败", "component", "轻量云", "vm", req.VMName, "error", err)
	}
	SyncLightweightVMRuntimeQuotaState(req.VMName, time.Now())
	RefreshVMCacheByNameAsync(req.VMName)
	return fillLightweightVMQuotaRuntime(&quota), nil
}

func EnsureLightweightVMNetwork(username, vmName string) error {
	username = strings.TrimSpace(username)
	vmName = strings.TrimSpace(vmName)
	if username == "" || vmName == "" {
		return fmt.Errorf("用户和虚拟机不能为空")
	}
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在: %w", err)
	}
	if !IsLightweightCloudType(user.CloudType) {
		return nil
	}
	// 如果用户没有配置专用VPC，跳过网络配置（适用于选择已有VM的场景）
	if user.DedicatedVPCSwitchID == 0 {
		return nil
	}
	var sw model.VPCSwitch
	if err := model.DB.First(&sw, user.DedicatedVPCSwitchID).Error; err != nil {
		return fmt.Errorf("专用 VPC 网络不存在")
	}
	if SwitchUsesDirectBridge(sw) {
		return fmt.Errorf("轻量云专用 VPC 不能使用桥接直通网络")
	}
	group, err := ensureLightweightVMSecurityGroup(sw.Username, vmName)
	if err != nil {
		return err
	}
	return BindVMToVPCAsAdmin(vmName, sw.ID, group.ID)
}

func ensureLightweightVMSecurityGroup(groupOwner, vmName string) (*model.VPCSecurityGroup, error) {
	var group model.VPCSecurityGroup
	if err := model.DB.Where("vm_name = ? AND is_vm_scoped = ?", vmName, true).First(&group).Error; err == nil {
		return &group, nil
	}
	name := "light-" + vmName
	if len(name) > 64 {
		name = name[:64]
	}
	group = model.VPCSecurityGroup{
		Username:   groupOwner,
		VMName:     vmName,
		Name:       name,
		IsVMScoped: true,
		Remark:     "轻量云 VM 专属安全组",
	}
	if err := model.DB.Create(&group).Error; err != nil {
		return nil, fmt.Errorf("创建轻量云 VM 专属安全组失败: %w", err)
	}
	return &group, nil
}

func GetLightweightVMQuota(vmName string) (*model.LightweightVMQuota, error) {
	var quota model.LightweightVMQuota
	if err := model.DB.Where("vm_name = ?", strings.TrimSpace(vmName)).First(&quota).Error; err != nil {
		return nil, err
	}
	return fillLightweightVMQuotaRuntime(&quota), nil
}

func fillLightweightVMQuotaRuntime(quota *model.LightweightVMQuota) *model.LightweightVMQuota {
	if quota == nil {
		return nil
	}
	down, up := AggregateLightweightVMMonthlyTraffic(quota.VMName)
	quota.UsedTrafficDown = down
	quota.UsedTrafficUp = up
	quota.UsedTrafficDownGB = formatTrafficBytes(down)
	quota.UsedTrafficUpGB = formatTrafficBytes(up)
	quota.IsLimitedDown, quota.IsLimitedUp = IsLightweightVMTrafficLimited(quota.VMName)
	quota.UsedPortForwards = GetLightweightVMPortForwardUsage(quota.VMName)
	quota.UsedSnapshots = CountVMSnapshots(quota.VMName)
	fillLightweightVMRuntimeSnapshot(quota, time.Now())
	fillLightweightVMNICRuntime(quota)
	return quota
}

func fillLightweightVMNICRuntime(quota *model.LightweightVMQuota) {
	if quota == nil || model.DB == nil || strings.TrimSpace(quota.VMName) == "" {
		return
	}
	var records []model.VmStatsRecord
	model.DB.Where("vm_name = ?", strings.TrimSpace(quota.VMName)).
		Order("recorded_at DESC").
		Limit(2).
		Find(&records)
	if len(records) == 0 {
		quota.CurrentNetRxRate = "0 B/s"
		quota.CurrentNetTxRate = "0 B/s"
		return
	}
	latest := records[0]
	quota.CurrentNetRxBytes = latest.NetRxBytes
	quota.CurrentNetTxBytes = latest.NetTxBytes
	quota.CurrentNetRxRate = "0 B/s"
	quota.CurrentNetTxRate = "0 B/s"
	if len(records) < 2 {
		return
	}
	prev := records[1]
	seconds := latest.RecordedAt.Sub(prev.RecordedAt).Seconds()
	if seconds <= 0 {
		return
	}
	if delta := latest.NetRxBytes - prev.NetRxBytes; delta > 0 {
		quota.CurrentNetRxRate = formatTrafficRate(float64(delta) / seconds)
	}
	if delta := latest.NetTxBytes - prev.NetTxBytes; delta > 0 {
		quota.CurrentNetTxRate = formatTrafficRate(float64(delta) / seconds)
	}
}

func formatTrafficRate(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return "0 B/s"
	}
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case bytesPerSecond >= GB:
		return fmt.Sprintf("%.2f GB/s", bytesPerSecond/GB)
	case bytesPerSecond >= MB:
		return fmt.Sprintf("%.2f MB/s", bytesPerSecond/MB)
	case bytesPerSecond >= KB:
		return fmt.Sprintf("%.2f KB/s", bytesPerSecond/KB)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSecond)
	}
}

func aggregateLightweightVMMonthlyTrafficRaw(vmName string) (downBytes, upBytes int64) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" || model.DB == nil {
		return 0, 0
	}
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	monthEnd := monthStart.AddDate(0, 1, 0)
	var records []model.VmStatsRecord
	model.DB.Where("vm_name = ? AND recorded_at >= ? AND recorded_at < ?", vmName, monthStart, monthEnd).
		Order("recorded_at ASC").
		Find(&records)
	for i := 1; i < len(records); i++ {
		if delta := records[i].NetRxBytes - records[i-1].NetRxBytes; delta > 0 {
			downBytes += delta
		}
		if delta := records[i].NetTxBytes - records[i-1].NetTxBytes; delta > 0 {
			upBytes += delta
		}
	}
	return downBytes, upBytes
}

func AggregateLightweightVMMonthlyTraffic(vmName string) (downBytes, upBytes int64) {
	rawDown, rawUp := aggregateLightweightVMMonthlyTrafficRaw(vmName)
	var record model.LightweightVMTrafficMonthly
	if err := model.DB.Where("vm_name = ? AND month = ?", strings.TrimSpace(vmName), currentTrafficMonth()).First(&record).Error; err != nil {
		return rawDown, rawUp
	}
	return clampTrafficBytes(rawDown - record.OffsetDown), clampTrafficBytes(rawUp - record.OffsetUp)
}

func getOrCreateLightweightVMTrafficMonthly(quota model.LightweightVMQuota, month string) model.LightweightVMTrafficMonthly {
	var record model.LightweightVMTrafficMonthly
	if err := model.DB.Where("vm_name = ? AND month = ?", quota.VMName, month).First(&record).Error; err == nil {
		return record
	}
	return model.LightweightVMTrafficMonthly{
		VMName:   quota.VMName,
		Username: quota.Username,
		Month:    month,
	}
}

func saveLightweightVMTrafficMonthly(record model.LightweightVMTrafficMonthly) error {
	if record.ID == 0 {
		return model.DB.Create(&record).Error
	}
	return model.DB.Save(&record).Error
}

func IsLightweightVMTrafficLimited(vmName string) (downLimited, upLimited bool) {
	if model.DB == nil || strings.TrimSpace(vmName) == "" {
		return false, false
	}
	var record model.LightweightVMTrafficMonthly
	if err := model.DB.Where("vm_name = ? AND month = ?", strings.TrimSpace(vmName), currentTrafficMonth()).First(&record).Error; err != nil {
		return false, false
	}
	return record.IsLimitedDown, record.IsLimitedUp
}

func ApplyLightweightVMBandwidth(vmName string) error {
	var quota model.LightweightVMQuota
	if err := model.DB.Where("vm_name = ?", strings.TrimSpace(vmName)).First(&quota).Error; err != nil {
		return nil
	}
	downMbps := quota.BandwidthDownMbps
	upMbps := quota.BandwidthUpMbps
	downLimited, upLimited := IsLightweightVMTrafficLimited(quota.VMName)
	penaltyMbps := lightweightTrafficPenaltyMbps()
	if downLimited {
		downMbps = penaltyMbps
	}
	if upLimited {
		upMbps = penaltyMbps
	}
	if downMbps <= 0 && upMbps <= 0 {
		return ClearVMBandwidth(quota.VMName)
	}
	downKB := MbpsToKBps(downMbps)
	upKB := MbpsToKBps(upMbps)
	return ApplyVMNICBandwidth(quota.VMName, downKB, downKB, downKB*30, upKB, upKB, upKB*30)
}

func CheckAndApplyLightweightVMTrafficLimit(quota model.LightweightVMQuota) {
	if quota.VMName == "" || model.DB == nil {
		return
	}
	rawDown, rawUp := aggregateLightweightVMMonthlyTrafficRaw(quota.VMName)
	record := getOrCreateLightweightVMTrafficMonthly(quota, currentTrafficMonth())
	effectiveDown := clampTrafficBytes(rawDown - record.OffsetDown)
	effectiveUp := clampTrafficBytes(rawUp - record.OffsetUp)
	record.Username = quota.Username
	record.TrafficDown = effectiveDown
	record.TrafficUp = effectiveUp

	downLimited := quota.TrafficDownGB > 0 && float64(effectiveDown) >= trafficQuotaBytes(quota.TrafficDownGB)
	upLimited := quota.TrafficUpGB > 0 && float64(effectiveUp) >= trafficQuotaBytes(quota.TrafficUpGB)
	changed := record.IsLimitedDown != downLimited || record.IsLimitedUp != upLimited
	record.IsLimitedDown = downLimited
	record.IsLimitedUp = upLimited
	if err := saveLightweightVMTrafficMonthly(record); err != nil {
		logger.App.Warn("保存 VM 月流量失败", "component", "轻量云流量配额", "vm", quota.VMName, "error", err)
		return
	}
	if changed {
		if err := ApplyLightweightVMBandwidth(quota.VMName); err != nil {
			logger.App.Warn("应用 VM 限速状态失败", "component", "轻量云流量配额", "vm", quota.VMName, "error", err)
		}
	}
	if (downLimited || upLimited) && changed {
		logger.App.Warn("VM 本月流量超限，已按超限方向强制限速",
			"component", "轻量云流量配额",
			"vm", quota.VMName,
			"penalty_mbps", lightweightTrafficPenaltyMbps(),
			"down", formatTrafficBytes(effectiveDown),
			"down_quota_gb", quota.TrafficDownGB,
			"up", formatTrafficBytes(effectiveUp),
			"up_quota_gb", quota.TrafficUpGB)
	}
}

func CheckAllLightweightVMTrafficQuota() {
	if model.DB == nil {
		return
	}
	var quotas []model.LightweightVMQuota
	model.DB.Find(&quotas)
	for _, quota := range quotas {
		CheckAndApplyLightweightVMTrafficLimit(quota)
	}
}

func CheckLightweightVMTrafficAfterQuotaUpdate(vmName string) {
	var quota model.LightweightVMQuota
	if err := model.DB.Where("vm_name = ?", strings.TrimSpace(vmName)).First(&quota).Error; err != nil {
		return
	}
	rawDown, rawUp := aggregateLightweightVMMonthlyTrafficRaw(quota.VMName)
	record := getOrCreateLightweightVMTrafficMonthly(quota, currentTrafficMonth())
	effectiveDown := clampTrafficBytes(rawDown - record.OffsetDown)
	effectiveUp := clampTrafficBytes(rawUp - record.OffsetUp)
	record.Username = quota.Username
	record.TrafficDown = effectiveDown
	record.TrafficUp = effectiveUp
	if !record.IsLimitedDown && !record.IsLimitedUp {
		_ = saveLightweightVMTrafficMonthly(record)
		_ = ApplyLightweightVMBandwidth(quota.VMName)
		return
	}
	downLimited := quota.TrafficDownGB > 0 && float64(effectiveDown) >= trafficQuotaBytes(quota.TrafficDownGB)
	upLimited := quota.TrafficUpGB > 0 && float64(effectiveUp) >= trafficQuotaBytes(quota.TrafficUpGB)
	record.IsLimitedDown = downLimited
	record.IsLimitedUp = upLimited
	if err := saveLightweightVMTrafficMonthly(record); err != nil {
		logger.App.Warn("保存 VM 配额调整状态失败", "component", "轻量云流量配额", "vm", quota.VMName, "error", err)
		return
	}
	if err := ApplyLightweightVMBandwidth(quota.VMName); err != nil {
		logger.App.Warn("配额调整后应用 VM 带宽失败", "component", "轻量云流量配额", "vm", quota.VMName, "error", err)
	}
}

func ResetAllLightweightVMTraffic() {
	if model.DB == nil {
		return
	}
	lastMonth := time.Now().AddDate(0, -1, 0).Format("2006-01")
	var records []model.LightweightVMTrafficMonthly
	model.DB.Where("month = ? AND (is_limited_down = ? OR is_limited_up = ?)", lastMonth, true, true).Find(&records)
	for _, record := range records {
		if err := ApplyLightweightVMBandwidth(record.VMName); err != nil {
			logger.App.Warn("月重置后恢复 VM 带宽失败", "component", "轻量云流量配额", "vm", record.VMName, "error", err)
		}
	}
	cleanupMonth := time.Now().AddDate(0, -12, 0).Format("2006-01")
	model.DB.Where("month < ?", cleanupMonth).Delete(&model.LightweightVMTrafficMonthly{})
}

func GetLightweightVMPortForwardUsage(vmName string) int {
	rules, err := listLivePortForwardsFromIPTables()
	if err != nil {
		return 0
	}
	count := 0
	for _, rule := range rules {
		if strings.TrimSpace(rule.VMName) == strings.TrimSpace(vmName) {
			count++
		}
	}
	return count
}

func CheckLightweightVMPortForwardQuota(username, vmName string, delta int) error {
	if delta <= 0 {
		return nil
	}
	var quota model.LightweightVMQuota
	if err := model.DB.Where("username = ? AND vm_name = ?", strings.TrimSpace(username), strings.TrimSpace(vmName)).First(&quota).Error; err != nil {
		return fmt.Errorf("当前轻量云服务器未配置端口转发配额")
	}
	if quota.MaxPortForwards <= 0 {
		return nil
	}
	used := GetLightweightVMPortForwardUsage(vmName)
	if used+delta > quota.MaxPortForwards {
		return fmt.Errorf("当前服务器端口转发数量超出配额限制（已用 %d / 上限 %d）", used, quota.MaxPortForwards)
	}
	return nil
}

func CheckLightweightVMSnapshotQuota(username, vmName string, delta int) error {
	if delta <= 0 {
		return nil
	}
	var quota model.LightweightVMQuota
	if err := model.DB.Where("username = ? AND vm_name = ?", strings.TrimSpace(username), strings.TrimSpace(vmName)).First(&quota).Error; err != nil {
		return fmt.Errorf("当前服务器未配置快照配额")
	}
	if quota.MaxSnapshots <= 0 {
		return nil
	}
	used := CountVMSnapshots(vmName)
	if used+delta > quota.MaxSnapshots {
		return fmt.Errorf("当前服务器快照数量超出配额限制（已用 %d / 上限 %d）", used, quota.MaxSnapshots)
	}
	return nil
}

func CleanupLightweightVMResources(vmName string) {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" || model.DB == nil {
		return
	}
	model.DB.Where("vm_name = ?", vmName).Delete(&model.LightweightVMTrafficMonthly{})
	model.DB.Where("vm_name = ?", vmName).Delete(&model.LightweightVMQuota{})
	model.DB.Where("vm_name = ?", vmName).Delete(&model.LightweightVMRegistration{})
	releaseLightweightRuntimeQuotaEnforcement(vmName)

	var groups []model.VPCSecurityGroup
	model.DB.Where("vm_name = ? AND is_vm_scoped = ?", vmName, true).Find(&groups)
	for _, group := range groups {
		model.DB.Where("security_group_id = ?", group.ID).Delete(&model.VPCSecurityGroupRule{})
		model.DB.Delete(&group)
	}
	if len(groups) > 0 {
		if err := ApplyVPCACLRules(); err != nil {
			logger.App.Warn("清理 VM 专属安全组后重建 VPC ACL 失败", "component", "轻量云", "vm", vmName, "error", err)
		}
	}
}
