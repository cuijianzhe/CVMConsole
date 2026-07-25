package model

import (
	"time"
)

const (
	IOPSModeTotal     = "TOTAL"
	IOPSModeReadWrite = "READ_WRITE"
	CapacityUnitGB    = "GB"
	CapacityUnitTB    = "TB"
)

type CloudDiskSpec struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"type:varchar(100);uniqueIndex;not null"`
	CapacityGB  int       `json:"capacity_gb" gorm:"not null"`
	IOPSMode    string    `json:"iops_mode" gorm:"type:varchar(20);default:'READ_WRITE'"`
	TotalIOPS   int       `json:"total_iops" gorm:"default:0"`
	ReadIOPS    int       `json:"read_iops" gorm:"default:0"`
	WriteIOPS   int       `json:"write_iops" gorm:"default:0"`
	Description string    `json:"description" gorm:"type:varchar(200);default:''"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (CloudDiskSpec) TableName() string {
	return "cloud_disk_specs"
}

type CreateCloudDiskSpecRequest struct {
	Name        string `json:"name" binding:"required"`
	CapacityGB  int    `json:"capacity_gb" binding:"required"`
	IOPSMode    string `json:"iops_mode"`
	TotalIOPS   int    `json:"total_iops"`
	ReadIOPS    int    `json:"read_iops"`
	WriteIOPS   int    `json:"write_iops"`
	Description string `json:"description"`
}

type UpdateCloudDiskSpecRequest struct {
	Name        string `json:"name" binding:"required"`
	CapacityGB  int    `json:"capacity_gb" binding:"required"`
	IOPSMode    string `json:"iops_mode"`
	TotalIOPS   int    `json:"total_iops"`
	ReadIOPS    int    `json:"read_iops"`
	WriteIOPS   int    `json:"write_iops"`
	Description string `json:"description"`
}

type CloudDiskSpecListResponse struct {
	List     []CloudDiskSpec `json:"list"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}
