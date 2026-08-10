package vm

import (
	"fmt"
	"strings"

	"kvm_console/model"
	"kvm_console/utils"
)

// RenameVM 重命名虚拟机（域必须处于关机状态，且新名称不能已存在）
// 执行 virsh domrename 后将同步更新数据库中所有引用旧名称的记录
func RenameVM(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return fmt.Errorf("虚拟机名称不能为空")
	}
	if oldName == newName {
		return nil // 名称未变化，无需操作
	}

	// 检查域是否存在
	if !DomainExists(oldName) {
		return fmt.Errorf("虚拟机 %s 不存在", oldName)
	}

	// 检查域状态（必须是 shut off）
	state := GetDomainState(oldName)
	if state != "shut off" {
		return fmt.Errorf("虚拟机必须处于关机状态才能重命名，当前状态: %s", state)
	}

	// 检查新名称是否已被占用
	if DomainExists(newName) {
		return fmt.Errorf("名称 %s 已被其他虚拟机使用", newName)
	}

	// 执行 libvirt 域重命名
	result := utils.ExecCommand("virsh", "domrename", oldName, newName)
	if result.Error != nil {
		errMsg := strings.TrimSpace(result.Stderr)
		if errMsg == "" {
			errMsg = result.Error.Error()
		}
		return fmt.Errorf("重命名虚拟机失败: %s", errMsg)
	}

	// 更新数据库中所有引用旧名称的记录
	updateVMNameInDB(oldName, newName)

	return nil
}

// updateVMNameInDB 更新所有数据库中引用旧 VM 名称的记录
func updateVMNameInDB(oldName, newName string) {
	tables := []string{
		"vm_network_infos",                 // 虚拟机网络信息
		"vm_schedules",                     // 虚拟机定时任务
		"vm_credentials",                   // 虚拟机凭据
		"vm_stats_records",                 // 虚拟机统计记录
		"vpc_vm_bindings",                  // VPC 绑定的虚拟机
		"vpc_security_groups",              // VPC 安全组 VM 关联
		"public_ip_bindings",               // 公网 IP 绑定
		"port_forward_ips",                 // 端口转发
		"bridge_static_hosts",              // 网桥静态 DHCP 绑定
		"vm_locks",                         // 虚拟机锁
		"vgpu_instances",                   // vGPU 实例分配
		"scheduler_events",                 // 调度事件记录
		"lightweight_vm_quotas",            // 轻量云配额
		"lightweight_vm_traffic_monthlies", // 轻量云月度流量
		"lightweight_vm_registrations",     // 轻量云登记
	}

	for _, table := range tables {
		if err := model.DB.Table(table).Where("vm_name = ?", oldName).Update("vm_name", newName).Error; err != nil {
			// 某些表可能没有匹配记录，或表不存在（如轻量云模块未启用），静默跳过
			continue
		}
	}
}
