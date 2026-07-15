package model

import (
	"time"
)

type VGPUProfile struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	PCIDevice     string    `gorm:"index;size:64" json:"pci_device"`
	ProfileName   string    `gorm:"size:128" json:"profile_name"`
	Description   string    `gorm:"size:512" json:"description"`
	MaxInstances  int       `json:"max_instances"`
	UsedInstances int       `json:"used_instances"`
	MemoryMB      int       `json:"memory_mb"`
	Available     bool      `json:"available"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type VGPUInstance struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UUID      string    `gorm:"uniqueIndex;size:36" json:"uuid"`
	ProfileID uint      `gorm:"index" json:"profile_id"`
	VMName    string    `gorm:"index;size:128" json:"vm_name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Profile VGPUProfile `gorm:"foreignKey:ProfileID" json:"profile,omitempty"`
}
