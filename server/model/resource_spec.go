package model

import (
	"time"
)

type ResourceSpec struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name" gorm:"type:varchar(100);uniqueIndex;not null"`
	CPUCores  int       `json:"cpu_cores" gorm:"not null"`
	MemoryGB  int       `json:"memory_gb" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ResourceSpec) TableName() string {
	return "resource_specs"
}

type CreateResourceSpecRequest struct {
	Name     string `json:"name" binding:"required"`
	CPUCores int    `json:"cpu_cores" binding:"required"`
	MemoryGB int    `json:"memory_gb" binding:"required"`
}

type UpdateResourceSpecRequest struct {
	Name     string `json:"name" binding:"required"`
	CPUCores int    `json:"cpu_cores" binding:"required"`
	MemoryGB int    `json:"memory_gb" binding:"required"`
}

type ResourceSpecListResponse struct {
	List     []ResourceSpec `json:"list"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}
