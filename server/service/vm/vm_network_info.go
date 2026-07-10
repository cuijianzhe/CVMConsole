package vm

import (
	"kvm_console/model"
	"strings"
	"time"
)

// CreateOrUpdateVMNetworkInfo 创建或更新虚拟机网络信息
// 参数：
//   vmName: 虚拟机名称
//   interfaceOrder: 网口序号（0为主网口，1、2...为附加网口）
//   ip: IP地址
//   mac: MAC地址
//   nicModel: 网卡型号（virtio/e1000e/rtl8139等）
//   networkType: 网络类型（nat/bridge）
//   switchName: 所属交换机名称
//   bridgeName: 桥接网桥名称
// 返回：错误信息
func CreateOrUpdateVMNetworkInfo(vmName string, interfaceOrder int, ip, mac, nicModel, networkType, switchName, bridgeName string) error {
	if model.DB == nil {
		return nil
	}
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil
	}

	var existing model.VMNetworkInfo
	err := model.DB.Where("vm_name = ? AND interface_order = ? AND is_deleted = ?", vmName, interfaceOrder, false).
		First(&existing).Error

	if err == nil {
		// 更新已存在的记录
		existing.IPAddress = ip
		existing.MacAddress = mac
		existing.NicModel = nicModel
		existing.NetworkType = networkType
		existing.SwitchName = switchName
		existing.BridgeName = bridgeName
		return model.DB.Save(&existing).Error
	}

	// 创建新记录
	info := model.VMNetworkInfo{
		VMName:         vmName,
		InterfaceOrder: interfaceOrder,
		IPAddress:      ip,
		MacAddress:     mac,
		NicModel:       nicModel,
		NetworkType:    networkType,
		SwitchName:     switchName,
		BridgeName:     bridgeName,
		IsDeleted:      false,
	}
	return model.DB.Create(&info).Error
}

// BatchCreateOrUpdateVMNetworkInfos 批量创建或更新虚拟机网络信息
// 用于处理多网卡场景，一次性保存所有网口的网络信息
func BatchCreateOrUpdateVMNetworkInfos(vmName string, infos []model.VMNetworkInfo) error {
	if model.DB == nil || len(infos) == 0 {
		return nil
	}
	for _, info := range infos {
		if err := CreateOrUpdateVMNetworkInfo(vmName, info.InterfaceOrder, info.IPAddress,
			info.MacAddress, info.NicModel, info.NetworkType, info.SwitchName, info.BridgeName); err != nil {
			return err
		}
	}
	return nil
}

// GetVMNetworkInfoByVMName 获取指定虚拟机的所有网络信息（未删除的）
// 返回的列表按网口序号升序排列
func GetVMNetworkInfoByVMName(vmName string) ([]model.VMNetworkInfo, error) {
	if model.DB == nil {
		return nil, nil
	}
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil, nil
	}
	var infos []model.VMNetworkInfo
	err := model.DB.Where("vm_name = ? AND is_deleted = ?", vmName, false).
		Order("interface_order ASC").
		Find(&infos).Error
	return infos, err
}

// GetVMNetworkInfoByMAC 通过 MAC 地址查找网络信息
// MAC地址不区分大小写
func GetVMNetworkInfoByMAC(mac string) (*model.VMNetworkInfo, error) {
	if model.DB == nil {
		return nil, nil
	}
	mac = strings.TrimSpace(strings.ToLower(mac))
	if mac == "" {
		return nil, nil
	}
	var info model.VMNetworkInfo
	err := model.DB.Where("mac_address = ? AND is_deleted = ?", mac, false).
		First(&info).Error
	return &info, err
}

// SoftDeleteVMNetworkInfoByVMName 软删除指定虚拟机的所有网络信息
// 软删除不会物理删除数据，只是标记为已删除，保留历史记录用于审计
func SoftDeleteVMNetworkInfoByVMName(vmName string) error {
	if model.DB == nil {
		return nil
	}
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil
	}
	return model.DB.Model(&model.VMNetworkInfo{}).
		Where("vm_name = ? AND is_deleted = ?", vmName, false).
		Updates(map[string]interface{}{
			"is_deleted": true,
			"deleted_at": time.Now(),
		}).Error
}

// SoftDeleteVMNetworkInfoByMAC 软删除指定 MAC 地址的网络信息
// 用于删除单个网口的网络信息（如删除附加网卡时）
func SoftDeleteVMNetworkInfoByMAC(mac string) error {
	if model.DB == nil {
		return nil
	}
	mac = strings.TrimSpace(strings.ToLower(mac))
	if mac == "" {
		return nil
	}
	return model.DB.Model(&model.VMNetworkInfo{}).
		Where("mac_address = ? AND is_deleted = ?", mac, false).
		Updates(map[string]interface{}{
			"is_deleted": true,
			"deleted_at": time.Now(),
		}).Error
}

// UpdateVMNetworkIP 更新虚拟机网络信息的 IP 地址
// 通过虚拟机名称和网口序号定位记录
func UpdateVMNetworkIP(vmName string, interfaceOrder int, ip string) error {
	if model.DB == nil {
		return nil
	}
	vmName = strings.TrimSpace(vmName)
	if vmName == "" {
		return nil
	}
	return model.DB.Model(&model.VMNetworkInfo{}).
		Where("vm_name = ? AND interface_order = ? AND is_deleted = ?", vmName, interfaceOrder, false).
		Update("ip_address", ip).Error
}

// UpdateVMNetworkIPByMAC 通过 MAC 地址更新 IP 地址
// 当从 DHCP 租约或 ARP 表获取到新的 IP 地址时使用此方法更新
func UpdateVMNetworkIPByMAC(mac, ip string) error {
	if model.DB == nil {
		return nil
	}
	mac = strings.TrimSpace(strings.ToLower(mac))
	if mac == "" {
		return nil
	}
	return model.DB.Model(&model.VMNetworkInfo{}).
		Where("mac_address = ? AND is_deleted = ?", mac, false).
		Update("ip_address", ip).Error
}
