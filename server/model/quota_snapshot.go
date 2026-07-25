package model

import (
	"time"

	"gorm.io/gorm"
)

// UserQuotaSnapshot 用户配额快照（缓存表，避免每次查询都实时计算）
type UserQuotaSnapshot struct {
	ID       uint   `json:"id" gorm:"primaryKey"`
	UserID   uint   `json:"user_id" gorm:"uniqueIndex;not null"`
	Username string `json:"username" gorm:"size:64;not null"`

	// VM 资源使用
	UsedCPU    int `json:"used_cpu" gorm:"default:0"`
	UsedMemory int `json:"used_memory" gorm:"default:0"` // MB
	UsedDisk   int `json:"used_disk" gorm:"default:0"`   // GB
	UsedVM     int `json:"used_vm" gorm:"default:0"`

	// 公网 IP 和端口转发
	UsedPublicIPs    int `json:"used_public_ips" gorm:"default:0"`
	UsedPortForwards int `json:"used_port_forwards" gorm:"default:0"`

	// 快照
	UsedSnapshots int `json:"used_snapshots" gorm:"default:0"`

	// 存储使用
	UsedStorage   int64  `json:"used_storage" gorm:"default:0"` // bytes
	UsedStorageGB string `json:"used_storage_gb" gorm:"size:32"`

	// 运行时长
	UsedRuntimeSeconds      int64  `json:"used_runtime_seconds" gorm:"default:0"`
	UsedRuntimeDisplay      string `json:"used_runtime_display" gorm:"size:32"`
	RemainingRuntimeSeconds int64  `json:"remaining_runtime_seconds" gorm:"default:0"`
	RemainingRuntimeDisplay string `json:"remaining_runtime_display" gorm:"size:32"`
	RuntimeQuotaReached     bool   `json:"runtime_quota_reached" gorm:"default:false"`

	// 流量使用
	UsedTrafficDown   int64  `json:"used_traffic_down" gorm:"default:0"`
	UsedTrafficUp     int64  `json:"used_traffic_up" gorm:"default:0"`
	UsedTrafficDownGB string `json:"used_traffic_down_gb" gorm:"size:32"`
	UsedTrafficUpGB   string `json:"used_traffic_up_gb" gorm:"size:32"`
	IsLimitedDown     bool   `json:"is_limited_down" gorm:"default:false"`
	IsLimitedUp       bool   `json:"is_limited_up" gorm:"default:false"`

	// 轻量化云类型的 VM 数量快照
	LightweightVMCount int `json:"lightweight_vm_count" gorm:"default:0"`

	// 数据新鲜度
	LastRefreshedAt *time.Time     `json:"last_refreshed_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-"`
}

// TableName 指定表名
func (UserQuotaSnapshot) TableName() string {
	return "user_quota_snapshots"
}
