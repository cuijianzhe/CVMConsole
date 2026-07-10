package vm

import (
	"kvm_console/model"
	"strings"
	"time"
)

// CreateOrUpdateVMNetworkInfo 创建或更新虚拟机网络信息
// 如果已存在相同 vm_name + interface_order 的记录，则更新；否则创建新记录
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
		existing.IPAddress = ip
		existing.MacAddress = mac
		existing.NicModel = nicModel
		existing.NetworkType = networkType
		existing.SwitchName = switchName
		existing.BridgeName = bridgeName
		return model.DB.Save(&existing).Error
	}

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