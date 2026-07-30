package host

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kvm_console/logger"
	"kvm_console/model"

	"gorm.io/gorm"
)

// mdevBasePaths vGPU mdev 设备扫描路径
// /sys/bus/pci/devices：传统 Intel GVT-g 等
// /sys/class/mdev_bus：NVIDIA vGPU 等（内核通过 class 接口暴露）
var mdevBasePaths = []string{
	"/sys/bus/pci/devices",
	"/sys/class/mdev_bus",
}

type VGPUProfileInfo struct {
	PCIDevice    string `json:"pci_device"`
	ProfileName  string `json:"profile_name"`
	Description  string `json:"description"`
	MaxInstances int    `json:"max_instances"`
	MemoryMB     int    `json:"memory_mb"`
}

func DiscoverVGPUProfiles() ([]VGPUProfileInfo, error) {
	var profiles []VGPUProfileInfo

	for _, basePath := range mdevBasePaths {
		// 使用 Glob 扫描而非 filepath.Walk，因为 sysfs 下设备目录多为符号链接，
		// filepath.Walk 不跟踪符号链接，会导致无法进入设备目录
		pattern := filepath.Join(basePath, "*", "mdev_supported_types")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, mdevTypesPath := range matches {
			// 从路径提取 PCI 设备地址，如 0000:01:00.0
			pciDevice := filepath.Base(filepath.Dir(mdevTypesPath))
			if !strings.Contains(pciDevice, ":") {
				continue
			}
			types, err := os.ReadDir(mdevTypesPath)
			if err != nil {
				continue
			}
			for _, t := range types {
				if !t.IsDir() {
					continue
				}
				profileName := t.Name()
				profilePath := filepath.Join(mdevTypesPath, profileName)
				descPath := filepath.Join(profilePath, "name")
				maxPath := filepath.Join(profilePath, "available_instances")
				description := readFileContent(descPath)
				maxInstances := readIntFromFile(maxPath)
				memSize := parseMemoryFromDescription(description)
				if description != "" && maxInstances > 0 {
					profiles = append(profiles, VGPUProfileInfo{
						PCIDevice:    pciDevice,
						ProfileName:  profileName,
						Description:  description,
						MaxInstances: maxInstances,
						MemoryMB:     memSize,
					})
				}
			}
		}
	}

	return profiles, nil
}

// resolveMdevPath 根据 PCI 设备地址查找 mdev_supported_types 所在的基础路径
func resolveMdevPath(pciDevice string) string {
	for _, basePath := range mdevBasePaths {
		mdevPath := filepath.Join(basePath, pciDevice, "mdev_supported_types")
		if _, err := os.Stat(mdevPath); err == nil {
			return mdevPath
		}
	}
	// 回退到传统路径
	return filepath.Join(mdevBasePaths[0], pciDevice, "mdev_supported_types")
}

func readFileContent(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readIntFromFile(path string) int {
	content := readFileContent(path)
	var val int
	fmt.Sscanf(content, "%d", &val)
	return val
}

func parseMemoryFromDescription(desc string) int {
	desc = strings.ToLower(desc)
	for _, unit := range []struct {
		unit       string
		multiplier int
	}{
		{"gb", 1024},
		{"mb", 1},
	} {
		idx := strings.Index(desc, unit.unit)
		if idx > 0 {
			start := idx - 1
			for start >= 0 && (desc[start] >= '0' && desc[start] <= '9' || desc[start] == '.') {
				start--
			}
			start++
			var num float64
			fmt.Sscanf(desc[start:idx], "%f", &num)
			return int(num * float64(unit.multiplier))
		}
	}
	return 0
}

func UpdateVGPUProfilesInDB() error {
	profiles, err := DiscoverVGPUProfiles()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		var existing model.VGPUProfile
		result := model.DB.Where("pci_device = ? AND profile_name = ?", p.PCIDevice, p.ProfileName).First(&existing)
		if result.Error != nil {
			newProfile := model.VGPUProfile{
				PCIDevice:     p.PCIDevice,
				ProfileName:   p.ProfileName,
				Description:   p.Description,
				MaxInstances:  p.MaxInstances,
				UsedInstances: 0,
				MemoryMB:      p.MemoryMB,
				Available:     true,
			}
			if err := model.DB.Create(&newProfile).Error; err != nil {
				logger.App.Warn("创建 vGPU profile 失败", "error", err)
			}
		} else {
			existing.Description = p.Description
			existing.MaxInstances = p.MaxInstances
			existing.MemoryMB = p.MemoryMB
			existing.Available = true
			if err := model.DB.Save(&existing).Error; err != nil {
				logger.App.Warn("更新 vGPU profile 失败", "error", err)
			}
		}
	}
	model.DB.Model(&model.VGPUProfile{}).Where("available = true").Update("available", false)
	for _, p := range profiles {
		model.DB.Model(&model.VGPUProfile{}).Where("pci_device = ? AND profile_name = ?", p.PCIDevice, p.ProfileName).Update("available", true)
	}
	return nil
}

func GetVGPUProfiles() ([]model.VGPUProfile, error) {
	var profiles []model.VGPUProfile
	err := model.DB.Where("available = true").Order("pci_device, profile_name").Find(&profiles).Error
	return profiles, err
}

func CreateVGPUInstance(profileID uint) (*model.VGPUInstance, error) {
	var profile model.VGPUProfile
	if err := model.DB.Where("id = ? AND available = true", profileID).First(&profile).Error; err != nil {
		return nil, fmt.Errorf("vGPU profile 不存在或不可用")
	}
	if profile.UsedInstances >= profile.MaxInstances {
		return nil, fmt.Errorf("该 vGPU profile 已达到最大实例数")
	}
	uuid := generateUUID()
	createPath := filepath.Join(resolveMdevPath(profile.PCIDevice), profile.ProfileName, "create")
	if err := os.WriteFile(createPath, []byte(uuid), 0644); err != nil {
		return nil, fmt.Errorf("创建 mdev 设备失败: %w", err)
	}
	instance := model.VGPUInstance{
		UUID:      uuid,
		ProfileID: profileID,
		Active:    true,
	}
	if err := model.DB.Create(&instance).Error; err != nil {
		DestroyVGPUInstanceByUUID(uuid)
		return nil, fmt.Errorf("保存 vGPU 实例失败: %w", err)
	}
	model.DB.Model(&profile).Update("used_instances", profile.UsedInstances+1)
	logger.App.Info("创建 vGPU 实例成功", "uuid", uuid, "profile", profile.ProfileName)
	return &instance, nil
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func DestroyVGPUInstanceByUUID(uuid string) error {
	var instance model.VGPUInstance
	if err := model.DB.Where("uuid = ?", uuid).First(&instance).Error; err != nil {
		return fmt.Errorf("vGPU 实例不存在")
	}
	if instance.VMName != "" {
		return fmt.Errorf("vGPU 实例正在被虚拟机使用")
	}
	// 扫描所有 mdev 基础路径查找需要删除的设备
	for _, basePath := range mdevBasePaths {
		removePattern := filepath.Join(basePath, "*", "mdev_supported_types", "*", uuid)
		matches, _ := filepath.Glob(removePattern)
		for _, path := range matches {
			if err := os.Remove(path); err != nil {
				logger.App.Warn("删除 mdev 设备失败", "path", path, "error", err)
			}
		}
	}
	model.DB.Model(&model.VGPUProfile{}).Where("id = ?", instance.ProfileID).UpdateColumn("used_instances", gorm.Expr("used_instances - 1"))
	return model.DB.Delete(&instance).Error
}

func GetVGPUInstances() ([]model.VGPUInstance, error) {
	var instances []model.VGPUInstance
	err := model.DB.Preload("Profile").Order("created_at DESC").Find(&instances).Error
	return instances, err
}

func AttachVGPUToVM(instanceID uint, vmName string) error {
	var instance model.VGPUInstance
	if err := model.DB.Where("id = ? AND active = true", instanceID).First(&instance).Error; err != nil {
		return fmt.Errorf("vGPU 实例不存在或未激活")
	}
	if instance.VMName != "" {
		return fmt.Errorf("vGPU 实例已被其他虚拟机使用")
	}
	return model.DB.Model(&instance).Update("vm_name", vmName).Error
}

func DetachVGPUFromVM(instanceID uint) error {
	var instance model.VGPUInstance
	if err := model.DB.Where("id = ?", instanceID).First(&instance).Error; err != nil {
		return fmt.Errorf("vGPU 实例不存在")
	}
	return model.DB.Model(&instance).Update("vm_name", "").Error
}

func GetVGPUInstanceByVMName(vmName string) (*model.VGPUInstance, error) {
	var instance model.VGPUInstance
	err := model.DB.Preload("Profile").Where("vm_name = ?", vmName).First(&instance).Error
	return &instance, err
}

func GenerateVGPUHostdevXML(uuid string) string {
	return fmt.Sprintf(`<hostdev mode='subsystem' type='mdev' managed='no'>
  <source>
    <address uuid='%s'/>
  </source>
</hostdev>`, uuid)
}
