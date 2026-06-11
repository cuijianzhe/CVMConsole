package pool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"kvm_console/model"
)

var storageIDSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func buildStoragePoolTree(devices []lsblkDevice, mounts map[string]findmntInfo, dfUsage map[string]mountUsage, aliases map[string]string, configs map[string]model.HostStoragePool) []HostStoragePoolInfo {
	result := make([]HostStoragePoolInfo, 0, len(devices))
	for _, dev := range devices {
		result = append(result, buildStoragePoolNode(dev, mounts, dfUsage, aliases, configs))
	}
	return result
}

func buildStoragePoolNode(dev lsblkDevice, mounts map[string]findmntInfo, dfUsage map[string]mountUsage, aliases map[string]string, configs map[string]model.HostStoragePool) HostStoragePoolInfo {
	devicePath := dev.Path
	if devicePath == "" && dev.KName != "" {
		devicePath = "/dev/" + dev.KName
	}
	idSource := aliases[devicePath]
	if idSource == "" {
		idSource = devicePath
	}
	id := normalizeStorageDeviceID(idSource)
	cfg, configured := configs[id]
	mountpoints := normalizeMountpoints([]string(dev.Mountpoints))

	node := HostStoragePoolInfo{
		ID:          id,
		Name:        dev.Name,
		DevicePath:  devicePath,
		KName:       dev.KName,
		Type:        dev.Type,
		Size:        dev.Size,
		FSType:      dev.FSType,
		FSVersion:   dev.FSVersion,
		Label:       dev.Label,
		UUID:        dev.UUID,
		Mountpoints: mountpoints,
		Model:       strings.TrimSpace(dev.Model),
		Serial:      strings.TrimSpace(dev.Serial),
		Rota:        dev.Rota,
		Removable:   dev.Removable,
		Readonly:    dev.Readonly,
		Tran:        dev.Tran,
		PKName:      dev.PKName,
		Configured:  configured,
	}
	if configured {
		node.DisplayName = cfg.DisplayName
		node.Enabled = cfg.Enabled
		node.IsDefault = cfg.IsDefault
		node.MountPath = cfg.MountPath
	}
	if node.DisplayName == "" {
		node.DisplayName = defaultStorageDisplayName(node)
	}
	if node.MountPath == "" && len(mountpoints) > 0 {
		node.MountPath = mountpoints[0]
	}
	if node.MountPath != "" {
		node.VMDir = filepath.Join(node.MountPath, "vm-disks")
		if cloneDir := configuredCloneDir(); isPathUnderMount(cloneDir, node.MountPath) {
			node.VMDir = cloneDir
		}
	}
	applyUsage(&node, mounts, dfUsage)

	for _, child := range dev.Children {
		node.Children = append(node.Children, buildStoragePoolNode(child, mounts, dfUsage, aliases, configs))
	}
	node.SystemDisk = isSystemStorageNode(node)
	node.CanFormat, node.StatusReason = canFormatStorageNode(node)
	node.CanUseForVM = canUseStorageNode(node)
	if !node.CanUseForVM && node.StatusReason == "" {
		node.StatusReason = "硬盘未挂载，无法作为虚拟机存储位置"
	}
	if node.Enabled && !node.CanUseForVM {
		node.Enabled = false
	}

	// 检测已有数据警告（仅对可格式化的整盘显示）
	if node.CanFormat && node.Type == "disk" {
		if hasData, warn := detectExistingData(node); hasData {
			node.HasExistingData = true
			node.ExistingDataWarning = warn
		}
	}

	return node
}

func applyUsage(node *HostStoragePoolInfo, mounts map[string]findmntInfo, dfUsage map[string]mountUsage) {
	for _, mp := range node.Mountpoints {
		if usage, ok := dfUsage[mp]; ok {
			node.Size = usage.Size
			node.Used = usage.Used
			node.Available = usage.Available
			break
		}
		if info, ok := mounts[mp]; ok {
			if info.Size > 0 {
				node.Size = info.Size
			}
			node.Used = info.Used
			node.Available = info.Avail
			break
		}
	}
	if node.Size > 0 && node.Used > 0 {
		node.UsePercent = int(float64(node.Used) / float64(node.Size) * 100)
	}
}

func canFormatStorageNode(node HostStoragePoolInfo) (bool, string) {
	if node.Readonly {
		return false, "设备为只读状态"
	}
	if node.Type != "disk" && node.Type != "part" {
		return false, "只支持格式化整块硬盘或分区"
	}
	if node.Type == "loop" || node.Type == "rom" || node.Removable {
		return false, "不支持格式化 loop、光驱或可移动设备"
	}
	// 排除 device-mapper 设备
	if node.Type == "dm" || strings.HasPrefix(node.Name, "dm-") {
		return false, "不支持格式化 device-mapper 设备"
	}
	// 排除内存盘
	nameLower := strings.ToLower(node.Name)
	if strings.HasPrefix(nameLower, "ram") || strings.HasPrefix(nameLower, "zram") {
		return false, "不支持格式化内存盘设备"
	}
	// 排除 LVM 物理卷
	fstype := strings.ToLower(node.FSType)
	if fstype == "lvm2_member" {
		return false, "该设备已被用作 LVM 物理卷"
	}
	// 排除 mdraid 成员
	if fstype == "linux_raid_member" {
		return false, "该设备已加入 mdraid 阵列"
	}
	// 排除 ZFS 存储池成员
	if fstype == "zfs_member" {
		return false, "该设备已加入 ZFS 存储池"
	}
	// 检查子设备中是否有 LVM/raid/zfs 成员
	if hasChildWithFSType(node, "lvm2_member", "linux_raid_member", "zfs_member") {
		reason := "该设备的子分区已加入"
		var parts []string
		if hasChildWithFSType(node, "lvm2_member") {
			parts = append(parts, "LVM")
		}
		if hasChildWithFSType(node, "linux_raid_member") {
			parts = append(parts, "mdraid")
		}
		if hasChildWithFSType(node, "zfs_member") {
			parts = append(parts, "ZFS")
		}
		return false, reason + strings.Join(parts, "/") + "，无法格式化"
	}
	if len(node.Mountpoints) > 0 || hasMountedChild(node) {
		return false, "设备或其分区当前已挂载"
	}
	if node.SystemDisk {
		return false, "系统关键磁盘禁止格式化"
	}
	return true, ""
}

func canUseStorageNode(node HostStoragePoolInfo) bool {
	if node.Readonly || node.Type == "rom" || node.Type == "loop" {
		return false
	}
	return node.MountPath != "" && len(node.Mountpoints) > 0
}

func validateFormatTarget(pool HostStoragePoolInfo) error {
	if ok, reason := canFormatStorageNode(pool); !ok {
		return fmt.Errorf("该硬盘不能格式化: %s", reason)
	}
	return nil
}

func isSystemStorageNode(node HostStoragePoolInfo) bool {
	for _, mp := range node.Mountpoints {
		switch mp {
		case "/", "/boot", "/boot/efi", "/usr", "/var", "/home":
			return true
		}
	}
	for _, child := range node.Children {
		if isSystemStorageNode(child) {
			return true
		}
	}
	return false
}

func hasMountedChild(node HostStoragePoolInfo) bool {
	for _, child := range node.Children {
		if len(child.Mountpoints) > 0 || hasMountedChild(child) {
			return true
		}
	}
	return false
}

// hasChildWithFSType 递归检查节点或其子节点是否存在指定 FSType。
func hasChildWithFSType(node HostStoragePoolInfo, fstypes ...string) bool {
	for _, child := range node.Children {
		for _, ft := range fstypes {
			if strings.EqualFold(child.FSType, ft) {
				return true
			}
		}
		if hasChildWithFSType(child, fstypes...) {
			return true
		}
	}
	return false
}

// detectExistingData 检测磁盘是否已有分区表或文件系统（仅对可格式化的整盘生效）。
func detectExistingData(node HostStoragePoolInfo) (bool, string) {
	if node.Type != "disk" {
		return false, ""
	}
	if len(node.Mountpoints) > 0 || node.SystemDisk {
		return false, ""
	}
	// 跳过已被 LVM/raid/zfs 使用的磁盘（已有专门的错误提示）
	if hasChildWithFSType(node, "lvm2_member", "linux_raid_member", "zfs_member") {
		return false, ""
	}
	// 检测是否存在分区表或已有文件系统
	hasData := len(node.Children) > 0 || node.FSType != ""
	if hasData {
		return true, "该磁盘上检测到已有分区或文件系统，继续操作可能导致数据丢失。"
	}
	return false, ""
}

func normalizeMountpoints(items []string) []string {
	var result []string
	seen := make(map[string]bool)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || item == "[SWAP]" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

func normalizeStorageDeviceID(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "/dev/disk/by-id/")
	raw = strings.TrimPrefix(raw, "/dev/disk/by-uuid/")
	raw = strings.TrimPrefix(raw, "/dev/")
	raw = storageIDSanitizer.ReplaceAllString(raw, "-")
	raw = strings.Trim(raw, "-")
	if raw == "" {
		raw = "unknown"
	}
	return raw
}

func defaultStorageMountPath(id string) string {
	return filepath.Join(hostStorageRoot, normalizeStorageDeviceID(id))
}

func defaultStorageDisplayName(pool HostStoragePoolInfo) string {
	if pool.Label != "" {
		return pool.Label
	}
	if pool.Model != "" {
		return strings.TrimSpace(fmt.Sprintf("%s %s", pool.Model, pool.Name))
	}
	if pool.DevicePath != "" {
		return pool.DevicePath
	}
	return pool.Name
}

func isPathUnderMount(pathValue, mountPath string) bool {
	pathValue = filepath.Clean(strings.TrimSpace(pathValue))
	mountPath = filepath.Clean(strings.TrimSpace(mountPath))
	if pathValue == "." || mountPath == "." {
		return false
	}
	if mountPath == string(os.PathSeparator) {
		return filepath.IsAbs(pathValue)
	}
	return pathValue == mountPath || strings.HasPrefix(pathValue, mountPath+string(os.PathSeparator))
}
