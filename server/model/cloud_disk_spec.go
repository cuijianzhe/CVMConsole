package model

import (
	"time"
)

const (
	DiskTypeSystem    = "SYSTEM"
	DiskTypeData      = "DATA"
	DiskFormatQCOW2   = "QCOW2"
	DiskFormatRAW     = "RAW"
	IOPSModeTotal     = "TOTAL"
	IOPSModeReadWrite = "READ_WRITE"
	CapacityUnitGB    = "GB"
	CapacityUnitTB    = "TB"
)

type CloudDiskSpec struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	Name            string    `json:"name" gorm:"type:varchar(100);uniqueIndex;not null"`
	DiskType        string    `json:"disk_type" gorm:"type:varchar(20);default:'DATA'"`
	CapacityGB      int       `json:"capacity_gb" gorm:"not null"`
	StorageLocation string    `json:"storage_location" gorm:"type:varchar(200);default:''"`
	DiskFormat      string    `json:"disk_format" gorm:"type:varchar(20);default:'QCOW2'"`
	IOPSMode        string    `json:"iops_mode" gorm:"type:varchar(20);default:'READ_WRITE'"`
	TotalIOPS       int       `json:"total_iops" gorm:"default:0"`
	ReadIOPS        int       `json:"read_iops" gorm:"default:0"`
	WriteIOPS       int       `json:"write_iops" gorm:"default:0"`
	Description     string    `json:"description" gorm:"type:varchar(200);default:''"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (CloudDiskSpec) TableName() string {
	return "cloud_disk_specs"
}

type CreateCloudDiskSpecRequest struct {
	Name            string `json:"name" binding:"required"`
	DiskType        string `json:"disk_type"`
	CapacityGB      int    `json:"capacity_gb" binding:"required"`
	StorageLocation string `json:"storage_location"`
	DiskFormat      string `json:"disk_format"`
	IOPSMode        string `json:"iops_mode"`
	TotalIOPS       int    `json:"total_iops"`
	ReadIOPS        int    `json:"read_iops"`
	WriteIOPS       int    `json:"write_iops"`
	Description     string `json:"description"`
}

type UpdateCloudDiskSpecRequest struct {
	Name            string `json:"name" binding:"required"`
	DiskType        string `json:"disk_type"`
	CapacityGB      int    `json:"capacity_gb" binding:"required"`
	StorageLocation string `json:"storage_location"`
	DiskFormat      string `json:"disk_format"`
	IOPSMode        string `json:"iops_mode"`
	TotalIOPS       int    `json:"total_iops"`
	ReadIOPS        int    `json:"read_iops"`
	WriteIOPS       int    `json:"write_iops"`
	Description     string `json:"description"`
}

type CloudDiskSpecListResponse struct {
	List     []CloudDiskSpec `json:"list"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}
