package model

import "time"

// VMNetworkInfo 虚拟机网络信息持久化模型
// 用于存储虚拟机的IP地址、MAC地址等网络信息，支持软删除
type VMNetworkInfo struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	VMName    string    `json:"vm_name" gorm:"index;size:128;not null"`           // 虚拟机名称
	InterfaceOrder int   `json:"interface_order" gorm:"not null;default:0"`       // 网口序号（0为主网口）
	IPAddress string    `json:"ip_address" gorm:"size:64"`                        // IP地址
	MacAddress string   `json:"mac_address" gorm:"size:64"`                       // MAC地址
	NicModel  string    `json:"nic_model" gorm:"size:64"`                         // 网卡型号（virtio/e1000e/rtl8139）
	NetworkType string  `json:"network_type" gorm:"size:32"`                      // 网络类型（nat/bridge）
	SwitchName string   `json:"switch_name" gorm:"size:128"`                      // 所属交换机名称
	BridgeName string   `json:"bridge_name" gorm:"size:128"`                      // 桥接网桥名称
	IsDeleted bool      `json:"is_deleted" gorm:"index;not null;default:false"`   // 软删除标记
	DeletedAt time.Time `json:"deleted_at" gorm:"index"`                          // 删除时间
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (VMNetworkInfo) TableName() string {
	return "vm_network_infos"
}
