package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/utils"
)

const (
	LightweightVMRegistrationStatusPending      = "pending"
	LightweightVMRegistrationStatusProvisioning = "provisioning"
	LightweightVMRegistrationStatusActive       = "active"
	LightweightVMRegistrationStatusFailed       = "failed"
)

var lightweightVMNameRegexp = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

// LightweightVMRegistrationRequest 是管理员登记轻量云 VM 的表单数据。
type LightweightVMRegistrationRequest struct {
	VMName               string                  `json:"vm_name"`
	Template             string                  `json:"template"`
	TemplateType         string                  `json:"template_type"`
	CloneMode            string                  `json:"clone_mode"`
	VCPU                 int                     `json:"vcpu"`
	RAM                  int                     `json:"ram"`
	DiskSize             int                     `json:"disk_size"`
	DiskBus              string                  `json:"disk_bus"`
	Hostname             string                  `json:"hostname"`
	Autostart            bool                    `json:"autostart"`
	Freeze               bool                    `json:"freeze"`
	APIC                 *bool                   `json:"apic"`
	PAE                  *bool                   `json:"pae"`
	RTCOffset            string                  `json:"rtc_offset"`
	RTCStartDate         string                  `json:"rtc_startdate"`
	GuestAgent           *VMGuestAgentConfig     `json:"guest_agent"`
	SMBIOS1              *VMSMBIOS1Config        `json:"smbios1"`
	VideoModel           string                  `json:"video_model"`
	CPUTopologyMode      string                  `json:"cpu_topology_mode"`
	CPULimitPercent      int                     `json:"cpu_limit_percent"`
	CPUAffinity          string                  `json:"cpu_affinity,omitempty"` // CPU 亲和性，如 "0,2,4"
	FirstBootRebootMode  string                  `json:"first_boot_reboot_mode"`
	MemoryDynamic        *VMMemoryDynamicRequest `json:"memory_dynamic"`
	NicModel             string                  `json:"nic_model"`
	StoragePoolID        string                  `json:"storage_pool_id"`
	PreserveFnOSDeviceID bool                    `json:"preserve_fnos_device_id"`
	FnOSDeviceID         string                  `json:"fnos_device_id"`
	TrafficDownGB        float64                 `json:"traffic_down_gb"`
	TrafficUpGB          float64                 `json:"traffic_up_gb"`
	BandwidthDownMbps    int                     `json:"bandwidth_down_mbps"`
	BandwidthUpMbps      int                     `json:"bandwidth_up_mbps"`
	MaxPortForwards      int                     `json:"max_port_forwards"`
	MaxSnapshots         int                     `json:"max_snapshots"`
	MaxRuntimeHours      int                     `json:"max_runtime_hours"`
}

// LightweightVMConfirmRequest 是用户确认开通时补齐的登录凭据。
type LightweightVMConfirmRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LightweightVMProvisionParams struct {
	RegistrationID     uint   `json:"registration_id"`
	Username           string `json:"username"`
	CredentialUsername string `json:"credential_username"`
	CredentialPassword string `json:"credential_password"`
	Operator           string `json:"operator"`
}

type LightweightVMRegistrationView struct {
	ID                   uint    `json:"id"`
	Username             string  `json:"username"`
	VMName               string  `json:"vm_name"`
	Template             string  `json:"template"`
	TemplateType         string  `json:"template_type"`
	VCPU                 int     `json:"vcpu"`
	RAM                  int     `json:"ram"`
	DiskSize             int     `json:"disk_size"`
	DiskBus              string  `json:"disk_bus"`
	Hostname             string  `json:"hostname"`
	Autostart            bool    `json:"autostart"`
	Freeze               bool    `json:"freeze"`
	APIC                 bool    `json:"apic"`
	PAE                  bool    `json:"pae"`
	RTCOffset            string  `json:"rtc_offset"`
	RTCStartDate         string  `json:"rtc_startdate"`
	VideoModel           string  `json:"video_model"`
	CPUTopologyMode      string  `json:"cpu_topology_mode"`
	CPULimitPercent      int     `json:"cpu_limit_percent"`
	CPUAffinity          string  `json:"cpu_affinity,omitempty"` // CPU 亲和性，如 "0,2,4"
	FirstBootRebootMode  string  `json:"first_boot_reboot_mode"`
	NicModel             string  `json:"nic_model"`
	StoragePoolID        string  `json:"storage_pool_id"`
	PreserveFnOSDeviceID bool    `json:"preserve_fnos_device_id"`
	FnOSDeviceID         string  `json:"fnos_device_id"`
	SwitchID             uint    `json:"switch_id"`
	SwitchName           string  `json:"switch_name"`
	SwitchCIDR           string  `json:"switch_cidr"`
	TrafficDownGB        float64 `json:"traffic_down_gb"`
	TrafficUpGB          float64 `json:"traffic_up_gb"`
	BandwidthDownMbps    int     `json:"bandwidth_down_mbps"`
	BandwidthUpMbps      int     `json:"bandwidth_up_mbps"`
	MaxPortForwards      int     `json:"max_port_forwards"`
	MaxSnapshots         int     `json:"max_snapshots"`
	MaxRuntimeHours      int     `json:"max_runtime_hours"`
	Status               string  `json:"status"`
	TaskID               uint    `json:"task_id"`
	ErrorMessage         string  `json:"error_message"`
	CreatedBy            string  `json:"created_by"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	ConfirmedAt          string  `json:"confirmed_at,omitempty"`
}

func createJSONText(value interface{}) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseJSONText(raw string, value interface{}) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), value)
}

func NormalizeVMNicModel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "e1000e":
		return "e1000e"
	case "rtl8139":
		return "rtl8139"
	default:
		return "virtio"
	}
}

func NormalizeVMDiskBus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "scsi":
		return "scsi"
	case "sata":
		return "sata"
	case "ide":
		return "ide"
	default:
		return "virtio"
	}
}

func validateLightweightVMRegistrationUser(user model.User) error {
	if user.Role != "user" {
		return fmt.Errorf("只能为普通用户注册轻量云服务器")
	}
	if !IsLightweightCloudType(user.CloudType) {
		return fmt.Errorf("当前用户不是轻量云用户")
	}
	// 如果用户没有配置专用VPC，跳过VPC检查（适用于选择已有VM的场景）
	if user.DedicatedVPCSwitchID == 0 {
		return nil
	}
	var count int64
	if err := model.DB.Model(&model.VPCSwitch{}).
		Where("id = ? AND (bridge_mode = '' OR bridge_mode = ? OR bridge_mode IS NULL)", user.DedicatedVPCSwitchID, BridgeModeNAT).
		Count(&count).Error; err != nil {
		return fmt.Errorf("检查专用 VPC 网络失败: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("轻量云专用 VPC 必须是有效的 NAT VPC")
	}
	return nil
}

func normalizeLightweightVMRegistrationRequest(user model.User, req LightweightVMRegistrationRequest, createdBy string) (*model.LightweightVMRegistration, error) {
	req.VMName = strings.TrimSpace(req.VMName)
	req.Template = strings.TrimSpace(req.Template)
	req.TemplateType = strings.ToLower(strings.TrimSpace(req.TemplateType))
	req.Hostname = strings.TrimSpace(req.Hostname)
	req.RTCOffset = strings.TrimSpace(req.RTCOffset)
	req.RTCStartDate = strings.TrimSpace(req.RTCStartDate)
	req.VideoModel = strings.TrimSpace(req.VideoModel)
	req.CPUTopologyMode = NormalizeVMCPUTopologyMode(req.CPUTopologyMode)
	req.CPULimitPercent = NormalizeVMCPULimitPercent(req.CPULimitPercent)
	req.FirstBootRebootMode = NormalizeVMFirstBootRebootMode(req.FirstBootRebootMode)
	req.DiskBus = NormalizeVMDiskBus(req.DiskBus)
	req.NicModel = NormalizeVMNicModel(req.NicModel)
	req.StoragePoolID = strings.TrimSpace(req.StoragePoolID)
	if req.VMName == "" {
		return nil, fmt.Errorf("虚拟机名称不能为空")
	}
	if !lightweightVMNameRegexp.MatchString(req.VMName) {
		return nil, fmt.Errorf("虚拟机名称只能包含字母和数字")
	}
	if req.Template == "" {
		return nil, fmt.Errorf("请选择模板")
	}
	if req.VCPU <= 0 {
		return nil, fmt.Errorf("CPU 核心数必须大于 0")
	}
	if req.RAM <= 0 {
		return nil, fmt.Errorf("内存必须大于 0")
	}
	if err := ValidateVMCPULimitPercent(req.CPULimitPercent); err != nil {
		return nil, err
	}
	if req.Hostname == "" {
		req.Hostname = GenerateRandomCloneHostname()
	}
	meta := GetTemplateMeta(req.Template)
	if req.TemplateType == "" {
		req.TemplateType = meta.Type
	}
	req.TemplateType = strings.ToLower(strings.TrimSpace(req.TemplateType))
	if req.TemplateType == "" {
		req.TemplateType = "linux"
	}
	if err := ValidateCloneCredentialsForTemplate(req.TemplateType, req.Hostname, NormalizeCloneUsernameForTemplate(req.TemplateType, "admin"), "TempAa12345!", false); err != nil {
		return nil, err
	}
	if req.DiskSize < 0 {
		req.DiskSize = 0
	}
	req.TrafficDownGB = NormalizeLightweightVMQuotaRequest(LightweightVMQuotaRequest{TrafficDownGB: req.TrafficDownGB}).TrafficDownGB
	req.TrafficUpGB = NormalizeLightweightVMQuotaRequest(LightweightVMQuotaRequest{TrafficUpGB: req.TrafficUpGB}).TrafficUpGB
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
	guestAgentJSON, err := createJSONText(req.GuestAgent)
	if err != nil {
		return nil, fmt.Errorf("序列化 Guest Agent 配置失败: %w", err)
	}
	smbiosJSON, err := createJSONText(req.SMBIOS1)
	if err != nil {
		return nil, fmt.Errorf("序列化 SMBIOS 配置失败: %w", err)
	}
	memoryJSON, err := createJSONText(req.MemoryDynamic)
	if err != nil {
		return nil, fmt.Errorf("序列化动态内存配置失败: %w", err)
	}
	if strings.TrimSpace(req.FnOSDeviceID) != "" {
		if _, _, err := normalizeFnOSDeviceID(req.FnOSDeviceID); err != nil {
			return nil, err
		}
		req.PreserveFnOSDeviceID = true
	}
	return &model.LightweightVMRegistration{
		Username:             user.Username,
		VMName:               req.VMName,
		Template:             req.Template,
		TemplateType:         req.TemplateType,
		CloneMode:            req.CloneMode,
		VCPU:                 req.VCPU,
		RAM:                  req.RAM,
		DiskSize:             req.DiskSize,
		DiskBus:              req.DiskBus,
		Hostname:             req.Hostname,
		Autostart:            req.Autostart,
		Freeze:               req.Freeze,
		APIC:                 req.APIC,
		PAE:                  req.PAE,
		RTCOffset:            req.RTCOffset,
		RTCStartDate:         req.RTCStartDate,
		GuestAgentJSON:       guestAgentJSON,
		SMBIOS1JSON:          smbiosJSON,
		VideoModel:           req.VideoModel,
		CPUTopologyMode:      req.CPUTopologyMode,
		CPULimitPercent:      req.CPULimitPercent,
		CPUAffinity:          req.CPUAffinity,
		FirstBootRebootMode:  req.FirstBootRebootMode,
		MemoryDynamicJSON:    memoryJSON,
		NicModel:             req.NicModel,
		StoragePoolID:        req.StoragePoolID,
		PreserveFnOSDeviceID: req.PreserveFnOSDeviceID,
		FnOSDeviceID:         strings.TrimSpace(req.FnOSDeviceID),
		SwitchID:             user.DedicatedVPCSwitchID,
		TrafficDownGB:        req.TrafficDownGB,
		TrafficUpGB:          req.TrafficUpGB,
		BandwidthDownMbps:    req.BandwidthDownMbps,
		BandwidthUpMbps:      req.BandwidthUpMbps,
		MaxPortForwards:      req.MaxPortForwards,
		MaxSnapshots:         req.MaxSnapshots,
		MaxRuntimeHours:      req.MaxRuntimeHours,
		Status:               LightweightVMRegistrationStatusPending,
		CreatedBy:            strings.TrimSpace(createdBy),
	}, nil
}

func CreateLightweightVMRegistrations(username string, reqs []LightweightVMRegistrationRequest, createdBy string) ([]LightweightVMRegistrationView, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(reqs) == 0 {
		return nil, nil
	}
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("用户不存在: %w", err)
	}
	if err := validateLightweightVMRegistrationUser(user); err != nil {
		return nil, err
	}
	created := make([]model.LightweightVMRegistration, 0, len(reqs))
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		for _, raw := range reqs {
			reg, err := normalizeLightweightVMRegistrationRequest(user, raw, createdBy)
			if err != nil {
				return err
			}
			if err := tx.Create(reg).Error; err != nil {
				return fmt.Errorf("创建轻量云 VM 注册失败: %w", err)
			}
			created = append(created, *reg)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	views := make([]LightweightVMRegistrationView, 0, len(created))
	for i := range created {
		views = append(views, BuildLightweightVMRegistrationView(created[i]))
	}
	return views, nil
}

func BuildLightweightVMRegistrationView(reg model.LightweightVMRegistration) LightweightVMRegistrationView {
	view := LightweightVMRegistrationView{
		ID:                   reg.ID,
		Username:             reg.Username,
		VMName:               reg.VMName,
		Template:             reg.Template,
		TemplateType:         reg.TemplateType,
		VCPU:                 reg.VCPU,
		RAM:                  reg.RAM,
		DiskSize:             reg.DiskSize,
		DiskBus:              NormalizeVMDiskBus(reg.DiskBus),
		Hostname:             reg.Hostname,
		Autostart:            reg.Autostart,
		Freeze:               reg.Freeze,
		APIC:                 ResolveVMAPICEnabled(reg.APIC),
		PAE:                  ResolveVMPAEEnabled(reg.PAE),
		RTCOffset:            reg.RTCOffset,
		RTCStartDate:         reg.RTCStartDate,
		VideoModel:           reg.VideoModel,
		CPUTopologyMode:      NormalizeVMCPUTopologyMode(reg.CPUTopologyMode),
		CPULimitPercent:      NormalizeVMCPULimitPercent(reg.CPULimitPercent),
		CPUAffinity:          reg.CPUAffinity,
		FirstBootRebootMode:  NormalizeVMFirstBootRebootMode(reg.FirstBootRebootMode),
		NicModel:             NormalizeVMNicModel(reg.NicModel),
		StoragePoolID:        reg.StoragePoolID,
		PreserveFnOSDeviceID: reg.PreserveFnOSDeviceID,
		FnOSDeviceID:         reg.FnOSDeviceID,
		SwitchID:             reg.SwitchID,
		TrafficDownGB:        reg.TrafficDownGB,
		TrafficUpGB:          reg.TrafficUpGB,
		BandwidthDownMbps:    reg.BandwidthDownMbps,
		BandwidthUpMbps:      reg.BandwidthUpMbps,
		MaxPortForwards:      reg.MaxPortForwards,
		MaxSnapshots:         reg.MaxSnapshots,
		MaxRuntimeHours:      reg.MaxRuntimeHours,
		Status:               reg.Status,
		TaskID:               reg.TaskID,
		ErrorMessage:         reg.ErrorMessage,
		CreatedBy:            reg.CreatedBy,
		CreatedAt:            reg.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:            reg.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if reg.ConfirmedAt != nil {
		view.ConfirmedAt = reg.ConfirmedAt.Format("2006-01-02 15:04:05")
	}
	if reg.SwitchID > 0 {
		var sw model.VPCSwitch
		if err := model.DB.First(&sw, reg.SwitchID).Error; err == nil {
			view.SwitchName = sw.Name
			view.SwitchCIDR = sw.CIDR
		}
	}
	return view
}

func ListLightweightVMRegistrations(username string, includeActive bool) ([]LightweightVMRegistrationView, error) {
	username = strings.TrimSpace(username)
	query := model.DB.Model(&model.LightweightVMRegistration{}).Order("id ASC")
	if username != "" {
		query = query.Where("username = ?", username)
	}
	if !includeActive {
		query = query.Where("status <> ?", LightweightVMRegistrationStatusActive)
	}
	var regs []model.LightweightVMRegistration
	if err := query.Find(&regs).Error; err != nil {
		return nil, err
	}
	views := make([]LightweightVMRegistrationView, 0, len(regs))
	for _, reg := range regs {
		views = append(views, BuildLightweightVMRegistrationView(reg))
	}
	return views, nil
}

func DeleteLightweightVMRegistration(username string, id uint) error {
	var reg model.LightweightVMRegistration
	if err := model.DB.Where("id = ? AND username = ?", id, strings.TrimSpace(username)).First(&reg).Error; err != nil {
		return fmt.Errorf("注册记录不存在")
	}
	if reg.Status == LightweightVMRegistrationStatusProvisioning || reg.Status == LightweightVMRegistrationStatusActive {
		return fmt.Errorf("当前注册记录已进入开通流程，不能删除")
	}
	return model.DB.Delete(&reg).Error
}

// RemoveLightweightVMRegistrationByVMName 将已开通 VM 从注册 VM 列表中移除，不删除虚拟机本体。
func RemoveLightweightVMRegistrationByVMName(username string, vmName string) error {
	username = strings.TrimSpace(username)
	vmName = strings.TrimSpace(vmName)
	if username == "" {
		return fmt.Errorf("用户不能为空")
	}
	if vmName == "" {
		return fmt.Errorf("虚拟机名称不能为空")
	}
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在: %w", err)
	}
	if err := validateLightweightVMRegistrationUser(user); err != nil {
		return err
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var provisioningCount int64
		if err := tx.Model(&model.LightweightVMRegistration{}).
			Where("username = ? AND vm_name = ? AND status = ?", username, vmName, LightweightVMRegistrationStatusProvisioning).
			Count(&provisioningCount).Error; err != nil {
			return err
		}
		if provisioningCount > 0 {
			return fmt.Errorf("当前 VM 正在开通中，暂不能移除")
		}

		var quotaCount int64
		if err := tx.Model(&model.LightweightVMQuota{}).Where("username = ? AND vm_name = ?", username, vmName).Count(&quotaCount).Error; err != nil {
			return err
		}
		var regCount int64
		if err := tx.Model(&model.LightweightVMRegistration{}).Where("username = ? AND vm_name = ?", username, vmName).Count(&regCount).Error; err != nil {
			return err
		}
		if quotaCount == 0 && regCount == 0 {
			return fmt.Errorf("轻量云 VM 记录不存在")
		}

		if err := tx.Where("username = ? AND vm_name = ?", username, vmName).Delete(&model.LightweightVMRegistration{}).Error; err != nil {
			return err
		}
		if err := tx.Where("username = ? AND vm_name = ?", username, vmName).Delete(&model.LightweightVMQuota{}).Error; err != nil {
			return err
		}
		if err := tx.Where("username = ? AND vm_name = ?", username, vmName).Delete(&model.LightweightVMTrafficMonthly{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 从用户的VM访问列表中移除该VM
	if err := RemoveVMFromUser(username, vmName); err != nil {
		// 记录错误但不阻止操作完成
		logger.App.Warn("移除轻量云VM后从用户访问列表移除失败", "vm", vmName, "user", username, "error", err)
	}

	return nil
}

func UpdateLightweightVMQuotaByVMName(username string, req LightweightVMQuotaRequest) (*model.LightweightVMQuota, *LightweightVMRegistrationView, error) {
	username = strings.TrimSpace(username)
	req = NormalizeLightweightVMQuotaRequest(req)
	if username == "" {
		return nil, nil, fmt.Errorf("用户不能为空")
	}
	if req.VMName == "" {
		return nil, nil, fmt.Errorf("虚拟机名称不能为空")
	}
	var user model.User
	if err := model.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, nil, fmt.Errorf("用户不存在: %w", err)
	}
	if err := validateLightweightVMRegistrationUser(user); err != nil {
		return nil, nil, err
	}

	var quota *model.LightweightVMQuota
	var regView *LightweightVMRegistrationView
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var reg model.LightweightVMRegistration
		regErr := tx.Where("username = ? AND vm_name = ?", username, req.VMName).First(&reg).Error
		if regErr == nil {
			if reg.Status == LightweightVMRegistrationStatusProvisioning {
				return fmt.Errorf("当前 VM 正在开通中，暂不能修改配额")
			}
			reg.TrafficDownGB = req.TrafficDownGB
			reg.TrafficUpGB = req.TrafficUpGB
			reg.BandwidthDownMbps = req.BandwidthDownMbps
			reg.BandwidthUpMbps = req.BandwidthUpMbps
			reg.MaxPortForwards = req.MaxPortForwards
			reg.MaxSnapshots = req.MaxSnapshots
			reg.MaxRuntimeHours = req.MaxRuntimeHours
			if err := tx.Save(&reg).Error; err != nil {
				return fmt.Errorf("更新轻量云 VM 注册配额失败: %w", err)
			}
			view := BuildLightweightVMRegistrationView(reg)
			regView = &view
		} else if regErr != gorm.ErrRecordNotFound {
			return regErr
		}

		var existingQuota model.LightweightVMQuota
		quotaErr := tx.Where("username = ? AND vm_name = ?", username, req.VMName).First(&existingQuota).Error
		if quotaErr == nil {
			existingQuota.TrafficDownGB = req.TrafficDownGB
			existingQuota.TrafficUpGB = req.TrafficUpGB
			existingQuota.BandwidthDownMbps = req.BandwidthDownMbps
			existingQuota.BandwidthUpMbps = req.BandwidthUpMbps
			existingQuota.MaxPortForwards = req.MaxPortForwards
			existingQuota.MaxSnapshots = req.MaxSnapshots
			existingQuota.MaxRuntimeHours = req.MaxRuntimeHours
			if err := tx.Save(&existingQuota).Error; err != nil {
				return fmt.Errorf("更新轻量云 VM 运行配额失败: %w", err)
			}
			quota = fillLightweightVMQuotaRuntime(&existingQuota)
			return nil
		}
		if quotaErr != gorm.ErrRecordNotFound {
			return quotaErr
		}
		if regView == nil {
			return fmt.Errorf("未找到该轻量云 VM 注册或运行配额")
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if quota != nil {
		CheckLightweightVMTrafficAfterQuotaUpdate(req.VMName)
		SyncLightweightVMRuntimeQuotaState(req.VMName, time.Now())
		if refreshed, err := GetLightweightVMQuota(req.VMName); err == nil {
			quota = refreshed
		}
		RefreshVMCacheByNameAsync(req.VMName)
	}
	return quota, regView, nil
}

func BuildLightweightVMProvisionParams(regID uint, username string, credential LightweightVMConfirmRequest) (*LightweightVMProvisionParams, error) {
	var reg model.LightweightVMRegistration
	if err := model.DB.Where("id = ? AND username = ?", regID, strings.TrimSpace(username)).First(&reg).Error; err != nil {
		return nil, fmt.Errorf("待开通服务器不存在")
	}
	if reg.Status != LightweightVMRegistrationStatusPending && reg.Status != LightweightVMRegistrationStatusFailed {
		return nil, fmt.Errorf("当前服务器状态不允许确认开通")
	}
	credential.Username = NormalizeCloneUsernameForTemplate(reg.TemplateType, credential.Username)
	if err := ValidateCloneCredentialsForTemplate(reg.TemplateType, reg.Hostname, credential.Username, credential.Password, true); err != nil {
		return nil, err
	}
	return &LightweightVMProvisionParams{
		RegistrationID:     reg.ID,
		Username:           reg.Username,
		CredentialUsername: credential.Username,
		CredentialPassword: credential.Password,
		Operator:           strings.TrimSpace(username),
	}, nil
}

func MarkLightweightVMRegistrationTask(regID uint, taskID uint) {
	model.DB.Model(&model.LightweightVMRegistration{}).Where("id = ?", regID).Updates(map[string]interface{}{
		"task_id": taskID,
		"status":  LightweightVMRegistrationStatusProvisioning,
	})
}

func ParseLightweightVMProvisionParams(jsonStr string) (*LightweightVMProvisionParams, error) {
	var params LightweightVMProvisionParams
	if err := json.Unmarshal([]byte(jsonStr), &params); err != nil {
		return nil, err
	}
	return &params, nil
}

func ProvisionLightweightVMRegistration(ctx context.Context, params *LightweightVMProvisionParams, progressFn func(int, string)) (*CloneResult, error) {
	if progressFn == nil {
		progressFn = func(int, string) {}
	}
	var reg model.LightweightVMRegistration
	if err := model.DB.First(&reg, params.RegistrationID).Error; err != nil {
		return nil, fmt.Errorf("待开通服务器不存在")
	}
	if reg.Username != strings.TrimSpace(params.Username) {
		return nil, fmt.Errorf("开通用户与注册记录不一致")
	}
	if reg.Status != LightweightVMRegistrationStatusPending && reg.Status != LightweightVMRegistrationStatusFailed && reg.Status != LightweightVMRegistrationStatusProvisioning {
		return nil, fmt.Errorf("当前服务器状态不允许开通")
	}
	var user model.User
	if err := model.DB.Where("username = ?", reg.Username).First(&user).Error; err != nil {
		return nil, fmt.Errorf("用户不存在: %w", err)
	}
	if err := validateLightweightVMRegistrationUser(user); err != nil {
		return nil, err
	}
	now := time.Now()
	if err := model.DB.Model(&reg).Updates(map[string]interface{}{
		"status":        LightweightVMRegistrationStatusProvisioning,
		"error_message": "",
		"confirmed_at":  &now,
	}).Error; err != nil {
		return nil, err
	}
	fail := func(err error) (*CloneResult, error) {
		model.DB.Model(&model.LightweightVMRegistration{}).Where("id = ?", reg.ID).Updates(map[string]interface{}{
			"status":        LightweightVMRegistrationStatusFailed,
			"error_message": err.Error(),
		})
		return nil, err
	}
	var guestAgent *VMGuestAgentConfig
	if strings.TrimSpace(reg.GuestAgentJSON) != "" {
		guestAgent = &VMGuestAgentConfig{}
		if err := parseJSONText(reg.GuestAgentJSON, guestAgent); err != nil {
			return fail(fmt.Errorf("读取 Guest Agent 配置失败: %w", err))
		}
	}
	var smbios1 *VMSMBIOS1Config
	if strings.TrimSpace(reg.SMBIOS1JSON) != "" {
		smbios1 = &VMSMBIOS1Config{}
		if err := parseJSONText(reg.SMBIOS1JSON, smbios1); err != nil {
			return fail(fmt.Errorf("读取 SMBIOS 配置失败: %w", err))
		}
	}
	var memoryDynamic *VMMemoryDynamicRequest
	if strings.TrimSpace(reg.MemoryDynamicJSON) != "" {
		memoryDynamic = &VMMemoryDynamicRequest{}
		if err := parseJSONText(reg.MemoryDynamicJSON, memoryDynamic); err != nil {
			return fail(fmt.Errorf("读取动态内存配置失败: %w", err))
		}
	}
	meta := GetTemplateMeta(reg.Template)
	cloneParams := &CloneParams{
		Name:                 reg.VMName,
		Template:             reg.Template,
		TemplateType:         reg.TemplateType,
		CloneMode:            reg.CloneMode,
		VCPU:                 reg.VCPU,
		RAM:                  reg.RAM,
		DiskSize:             reg.DiskSize,
		DiskBus:              reg.DiskBus,
		Hostname:             reg.Hostname,
		User:                 params.CredentialUsername,
		Password:             params.CredentialPassword,
		Autostart:            reg.Autostart,
		Freeze:               reg.Freeze,
		APIC:                 reg.APIC,
		PAE:                  reg.PAE,
		RTCOffset:            reg.RTCOffset,
		RTCStartDate:         reg.RTCStartDate,
		GuestAgent:           guestAgent,
		SMBIOS1:              smbios1,
		TemplateRootPass:     meta.RootPassword,
		TemplateUser:         meta.TemplateUser,
		VideoModel:           reg.VideoModel,
		CPUTopologyMode:      reg.CPUTopologyMode,
		CPULimitPercent:      reg.CPULimitPercent,
		CPUAffinity:          reg.CPUAffinity,
		FirstBootRebootMode:  reg.FirstBootRebootMode,
		MemoryDynamic:        memoryDynamic,
		SwitchID:             user.DedicatedVPCSwitchID,
		StoragePoolID:        reg.StoragePoolID,
		PreserveFnOSDeviceID: reg.PreserveFnOSDeviceID,
		FnOSDeviceID:         reg.FnOSDeviceID,
		NicModel:             reg.NicModel,
		IsAdmin:              true,
	}
	progressFn(5, "正在创建轻量云服务器...")
	result, err := CloneVM(ctx, cloneParams, progressFn)
	if err != nil {
		if !isVMAlreadyExistsError(err) || !vmDomainExists(reg.VMName) {
			return fail(err)
		}
		progressFn(10, "检测到上次失败后保留的虚拟机，尝试继续初始化...")
		result, err = continueExistingLightweightVM(ctx, cloneParams, progressFn)
		if err != nil {
			return fail(err)
		}
	}
	if err := AddVMToUser(user.Username, reg.VMName); err != nil {
		return fail(fmt.Errorf("服务器已创建，但写入用户归属失败: %w", err))
	}
	quotaReq := LightweightVMQuotaRequest{
		VMName:            reg.VMName,
		TrafficDownGB:     reg.TrafficDownGB,
		TrafficUpGB:       reg.TrafficUpGB,
		BandwidthDownMbps: reg.BandwidthDownMbps,
		BandwidthUpMbps:   reg.BandwidthUpMbps,
		MaxPortForwards:   reg.MaxPortForwards,
		MaxSnapshots:      reg.MaxSnapshots,
		MaxRuntimeHours:   reg.MaxRuntimeHours,
	}
	if _, err := UpsertLightweightVMQuota(user.Username, quotaReq); err != nil {
		return fail(fmt.Errorf("服务器已创建，但写入轻量云配额失败: %w", err))
	}
	if err := EnsureLightweightVMNetwork(user.Username, reg.VMName); err != nil {
		return fail(fmt.Errorf("服务器已创建，但绑定专用 VPC 失败: %w", err))
	}
	if err := ApplyLightweightVMBandwidth(reg.VMName); err != nil {
		return fail(fmt.Errorf("服务器已创建，但应用带宽失败: %w", err))
	}
	if err := SaveVMCredential(reg.VMName, params.CredentialUsername, params.CredentialPassword, "lightweight_registration", params.Operator, false); err != nil {
		return fail(fmt.Errorf("服务器已创建，但保存登录凭据失败: %w", err))
	}
	if err := model.DB.Model(&model.LightweightVMRegistration{}).Where("id = ?", reg.ID).Updates(map[string]interface{}{
		"status":        LightweightVMRegistrationStatusActive,
		"error_message": "",
	}).Error; err != nil {
		return fail(fmt.Errorf("服务器已创建，但更新注册状态失败: %w", err))
	}
	return result, nil
}

func isVMAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "已存在") || strings.Contains(strings.ToLower(msg), "already exists")
}

func vmDomainExists(vmName string) bool {
	result := utils.ExecCommand("virsh", "dominfo", strings.TrimSpace(vmName))
	return result.ExitCode == 0
}

func continueExistingLightweightVM(ctx context.Context, params *CloneParams, progressFn func(int, string)) (*CloneResult, error) {
	if params == nil {
		return nil, fmt.Errorf("虚拟机参数为空")
	}
	stateResult := utils.ExecCommand("virsh", "domstate", params.Name)
	if stateResult.Error != nil {
		return nil, fmt.Errorf("读取虚拟机状态失败: %s", stateResult.Stderr)
	}
	if !strings.Contains(strings.ToLower(stateResult.Stdout), "running") {
		if err := StartVM(params.Name); err != nil {
			return nil, err
		}
	}
	FixOnReboot(params.Name)
	if err := checkCanceled(ctx, params.Name, ""); err != nil {
		return nil, err
	}
	progressFn(60, "等待虚拟机启动...")
	time.Sleep(5 * time.Second)
	ip := waitForIPWithContext(ctx, params.Name, linuxCloneIPWaitSeconds)
	tplType := strings.ToLower(strings.TrimSpace(params.TemplateType))
	if tplType == "" {
		tplType = "linux"
	}
	if tplType == "linux" {
		if ip == "" {
			return nil, fmt.Errorf("未获取到虚拟机 IP，Linux 初始化无法执行")
		}
		progressFn(70, "SSH 初始化中...")
		if err := initLinuxClone(params, ip, progressFn); err != nil {
			return nil, err
		}
		progressFn(96, "等待虚拟机网络刷新...")
		oldIP := ip
		time.Sleep(15 * time.Second)
		if newIP := getVMIP(params.Name, true); newIP != "" && newIP != oldIP {
			ip = newIP
		}
	}
	disk := getVMDiskInfo(params.Name)
	progressFn(100, "轻量云服务器初始化完成")
	return &CloneResult{
		VMName:   params.Name,
		IP:       ip,
		DiskPath: disk.path,
		Template: params.Template,
	}, nil
}

func FormatLightweightVMRegistrationList(regs []LightweightVMRegistrationView) string {
	if len(regs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, reg := range regs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, reg.VMName)
		fmt.Fprintf(&b, "   模板：%s（%s）\n", reg.Template, displayTemplateType(reg.TemplateType))
		fmt.Fprintf(&b, "   规格：%d 核 / %d GB 内存 / %d GB 磁盘\n", reg.VCPU, reg.RAM, reg.DiskSize)
		fmt.Fprintf(&b, "   网络：%s%s\n", emptyToDash(reg.SwitchName), switchCIDRSuffix(reg.SwitchCIDR))
		fmt.Fprintf(&b, "   流量：下行 %s / 上行 %s\n", quotaGBText(reg.TrafficDownGB), quotaGBText(reg.TrafficUpGB))
		fmt.Fprintf(&b, "   带宽：下行 %s / 上行 %s\n", quotaMbpsText(reg.BandwidthDownMbps), quotaMbpsText(reg.BandwidthUpMbps))
		fmt.Fprintf(&b, "   端口转发上限：%s\n", quotaCountText(reg.MaxPortForwards))
		fmt.Fprintf(&b, "   快照上限：%s\n", quotaCountText(reg.MaxSnapshots))
		fmt.Fprintf(&b, "   运行时长配额：%s\n", quotaHoursText(reg.MaxRuntimeHours))
	}
	return b.String()
}

func displayTemplateType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "windows":
		return "Windows"
	case "fnos":
		return "fnOS"
	case "other":
		return "其他"
	default:
		return "Linux"
	}
}

func emptyToDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}

func switchCIDRSuffix(cidr string) string {
	if strings.TrimSpace(cidr) == "" {
		return ""
	}
	return " / " + strings.TrimSpace(cidr)
}

func quotaGBText(value float64) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%.2f GB/月", value)
}

func quotaHoursText(value int) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d 小时", value)
}

func quotaMbpsText(value int) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d Mbps", value)
}

func quotaCountText(value int) string {
	if value <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d", value)
}
