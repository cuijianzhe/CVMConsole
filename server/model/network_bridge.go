package model

import "time"

// NetworkBridge 记录面板管理的宿主机网桥。
type NetworkBridge struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	Name          string    `json:"name" gorm:"uniqueIndex;not null;size:64"`
	Mode          string    `json:"mode" gorm:"not null;size:16"` // nat/bridge
	UplinkIF      string    `json:"uplink_if" gorm:"size:64"`
	MigrateHostIP bool      `json:"migrate_host_ip" gorm:"default:false"`
	HostAddrs     string    `json:"host_addrs" gorm:"size:512"`  // 换行分隔的 CIDR 地址列表，如 "192.168.11.54/24"
	HostGateway   string    `json:"host_gateway" gorm:"size:64"` // 默认网关 IP
	HostMetric    string    `json:"host_metric" gorm:"size:16"`  // 路由 metric
	HostDNS       string    `json:"host_dns" gorm:"size:512"`    // 空格分隔的 DNS 服务器 IP，如 "192.168.10.1 223.5.5.5"
	IsDefault     bool      `json:"is_default" gorm:"default:false"`
	DHCPCIDR      string    `json:"dhcp_cidr" gorm:"size:32"`    // DHCP 网段 CIDR，如 "10.100.5.0/22"
	DHCPStart     string    `json:"dhcp_start" gorm:"size:45"`   // DHCP 起始 IP
	DHCPEnd       string    `json:"dhcp_end" gorm:"size:45"`     // DHCP 结束 IP
	DHCPGateway   string    `json:"dhcp_gateway" gorm:"size:45"` // DHCP 网关 IP
	DHCPDNS       string    `json:"dhcp_dns" gorm:"size:512"`    // DHCP DNS 服务器（空格分隔）
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (NetworkBridge) TableName() string {
	return "network_bridges"
}

// BridgeStaticHost 内存结构，用于文件操作（向后兼容）
type BridgeStaticHost struct {
	VMName string
	MAC    string
	IP     string
}

// BridgeStaticHostDB 数据库模型，存储 DHCP 静态绑定
type BridgeStaticHostDB struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	BridgeName string    `json:"bridge_name" gorm:"index;not null;size:64"` // 网桥名称
	VMName     string    `json:"vm_name" gorm:"index;not null;size:64"`     // 虚拟机名称
	MAC        string    `json:"mac" gorm:"not null;size:18"`               // MAC 地址，如 52:54:00:1a:71:24
	IP         string    `json:"ip" gorm:"index;not null;size:45"`          // 静态 IP 地址
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (BridgeStaticHostDB) TableName() string {
	return "bridge_static_hosts"
}

// BridgeDHCPLease DHCP 租约结构
type BridgeDHCPLease struct {
	ExpiryTime string
	ExpiryUnix int64
	MAC        string
	IP         string
	Hostname   string
	ClientID   string
}
