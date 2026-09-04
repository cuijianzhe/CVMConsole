package service

import (
	"context"
	"fmt"
	"strings"

	"kvm_console/config"
	"kvm_console/model"
	portsecurity "kvm_console/service/network/portsecurity"
	ovspkg "kvm_console/service/ovs"
	vmpkg "kvm_console/service/vm"
)

func init() {
	portsecurity.HookGetVMMACByOrder = vmpkg.GetVMMACByOrder
	ovspkg.HookGetVMPortSecurityStatus = func(vmName string) (bool, map[string]ovspkg.VMPortSecurityStatus, error) {
		status, err := portsecurity.GetStatus()
		if err != nil {
			return IsPortSecurityEnabled(), nil, err
		}
		ports := make(map[string]ovspkg.VMPortSecurityStatus)
		for _, item := range status.Ports {
			if item.VMName != vmName {
				continue
			}
			ports[item.Port] = ovspkg.VMPortSecurityStatus{
				Mode:                  item.Mode,
				AllowedIPv4Addresses:  append([]string{}, item.AllowedIPv4Addresses...),
				AllowedIPv6Addresses:  append([]string{}, item.AllowedIPv6Addresses...),
				NeighborMeterID:       item.NeighborMeterID,
				BroadcastMeterID:      item.BroadcastMeterID,
				PolicingKpps:          item.PolicingKpps,
				PolicingBurstKPackets: item.PolicingBurstKPackets,
				DropPackets:           item.DropPackets,
				NeighborDropPackets:   item.NeighborDropPackets,
				BroadcastDropPackets:  item.BroadcastDropPackets,
				Applied:               item.Applied,
				Isolated:              item.Isolated,
				LastError:             item.LastError,
			}
		}
		return status.Enabled, ports, nil
	}
}

type PortSecurityIssue = portsecurity.Issue
type PortSecurityBridgeCapability = portsecurity.BridgeCapability
type PortSecurityPortStatus = portsecurity.PortStatus
type PortSecurityPreflightResult = portsecurity.PreflightResult
type PortSecurityStatus = portsecurity.Status
type PortSecurityTaskParams = portsecurity.TaskParams

func IsPortSecurityEnabled() bool {
	return config.GlobalConfig != nil && config.GlobalConfig.PortSecurityEnabled
}

// IsAdministratorAccount 按当前数据库角色识别管理员账号，兼容已修改默认管理员用户名的环境。
func IsAdministratorAccount(username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if config.GlobalConfig != nil && username == strings.TrimSpace(config.GlobalConfig.DefaultAdminUser) {
		return true
	}
	if model.DB == nil {
		return false
	}
	var count int64
	model.DB.Model(&model.User{}).Where("username = ? AND role = ?", username, "admin").Count(&count)
	return count > 0
}

func PreflightPortSecurity() (*PortSecurityPreflightResult, error) {
	return portsecurity.Preflight()
}

func GetPortSecurityStatus() (*PortSecurityStatus, error) {
	return portsecurity.GetStatus()
}

func ReconcilePortSecurity() (*PortSecurityStatus, error) {
	return portsecurity.Reconcile()
}

func ReconcileVMPortSecurity(vmName string) error {
	return portsecurity.ReconcileVM(vmName)
}

func TriggerPortSecurityReconcile() {
	portsecurity.TriggerReconcile()
}

func StartPortSecurityReconciler() {
	portsecurity.StartReconciler()
}

func ExecutePortSecurityTask(ctx context.Context, params PortSecurityTaskParams, progress func(int, string)) (string, error) {
	return portsecurity.ExecuteTask(ctx, params, progress)
}

// PrepareVMPortSecurityBinding 在虚拟机首次启动前建立 VPC 绑定并保存主网卡身份资料。
// 注意：VPC 绑定记录（vpc_vm_bindings）在端口安全关闭时也必须建立——
// 创建/导入流程在启动前还需要基于该记录执行 DHCP 静态绑定（指定 IP）、
// 流量统计与安全组关联；绑定本身是幂等的（已存在则更新）。
// 允许地址清单仅在端口安全开启时才写入。
func PrepareVMPortSecurityBinding(owner, vmName string, switchID, securityGroupID uint, allowedIPv4, allowedIPv6 string) error {
	if switchID == 0 {
		return nil
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = FindVMOwner(vmName)
	}
	// 普通用户未指定安全组时回退到其默认安全组（BindVMToVPC 不接受 SG=0）
	if !IsAdministratorAccount(owner) && securityGroupID == 0 {
		var g model.VPCSecurityGroup
		if err := model.DB.Where("username = ? AND is_default = ?", owner, true).First(&g).Error; err == nil {
			securityGroupID = g.ID
		}
	}
	var err error
	if IsAdministratorAccount(owner) {
		err = BindVMToVPCAsAdmin(vmName, switchID, securityGroupID)
	} else {
		err = BindVMToVPC(owner, vmName, switchID, securityGroupID)
	}
	if err != nil {
		return err
	}
	// 端口安全关闭时无需登记允许地址清单
	if config.GlobalConfig == nil || !config.GlobalConfig.PortSecurityEnabled {
		return nil
	}
	if err := UpdateVMInterfaceAllowedAddresses(vmName, 0, allowedIPv4, allowedIPv6); err != nil {
		return fmt.Errorf("保存主网卡允许地址失败: %w", err)
	}
	return nil
}
