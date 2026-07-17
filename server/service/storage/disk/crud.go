package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/utils"

	"github.com/digitalocean/go-libvirt"
)

// DiskInfo holds information about a virtual machine disk.
type DiskInfo struct {
	Device     string `json:"device"`      // device name (e.g. vda, vdb)
	Path       string `json:"path"`        // disk file path
	CapacityGB string `json:"capacity_gb"` // capacity (GB)
	UsedGB     string `json:"used_gb"`     // used (GB)
	Bus        string `json:"bus"`         // bus type
	Format     string `json:"format"`      // disk format qcow2/raw
	DeviceType string `json:"device_type"` // disk/cdrom
	HotSupport bool   `json:"hot_support"` // supports hot operations
	// IOPS limits (0 = unlimited)
	IOPSTotal IOPSField `json:"iops_total"` // total IOPS limit
	IOPSRead  IOPSField `json:"iops_read"`  // read IOPS limit
	IOPSWrite IOPSField `json:"iops_write"` // write IOPS limit
}

// IOPSField represents an optional IOPS value.
type IOPSField struct {
	Value int  `json:"value"`
	IsSet bool `json:"is_set"`
}

// DiskSimpleInfo holds brief disk information (for delete confirmation UI).
type DiskSimpleInfo struct {
	Device     string `json:"device"`      // device name
	Path       string `json:"path"`        // disk file path
	CapacityGB string `json:"capacity_gb"` // capacity (GB)
	Format     string `json:"format"`      // disk format
	IsSystem   bool   `json:"is_system"`   // whether this is the system disk (first disk)
	SizeBytes  int64  `json:"size_bytes"`  // actual file size in bytes
}

// diskXMLInfo holds extra disk information extracted from XML.
type diskXMLInfo struct {
	Format     string
	DeviceType string
	Bus        string
}

// ErrNoPCIESlots is returned when PCIe slots are exhausted, triggering SCSI fallback.
var ErrNoPCIESlots = fmt.Errorf("no_pcie_slots")

// ListDisks lists all disks of a virtual machine.
func ListDisks(vmName string) ([]DiskInfo, error) {
	state, _ := libvirt_rpc.GetDomainStateRPC(vmName)

	domainXML, err := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
	if err != nil {
		return nil, fmt.Errorf("获取磁盘列表失败: %w", err)
	}

	// parse block device list from XML (replaces virsh domblklist)
	blkList := libvirt_rpc.ParseDisksFromDomainXML(domainXML)

	// get detailed info for each disk from XML (format, device type, bus, IOPS)
	diskXMLMap := parseDiskXMLInfo(vmName)
	diskIOPSMap := ParseAllDiskIOPSTune(vmName)

	var disks []DiskInfo

	for _, blk := range blkList {
		device := blk.Target
		path := blk.Source

		// skip devices without target
		if device == "" {
			continue
		}

		disk := DiskInfo{
			Device: device,
			Path:   path,
		}

		// get info from XML
		if xmlInfo, ok := diskXMLMap[disk.Device]; ok {
			disk.Format = xmlInfo.Format
			disk.DeviceType = xmlInfo.DeviceType
			disk.Bus = xmlInfo.Bus
		}

		// skip disks with empty or "-" source (but keep empty CDROMs)
		if (path == "" || path == "-") && disk.DeviceType != "cdrom" {
			continue
		}
		// clean up empty CDROM path
		if path == "-" {
			disk.Path = ""
		}

		disk.HotSupport = disk.Bus == "virtio" || disk.Bus == "scsi"

		// capacity and usage
		if state == "running" && disk.Path != "" {
			capVal, allocVal, _, blkErr := libvirt_rpc.GetBlockInfoRPC(vmName, disk.Device)
			if blkErr == nil {
				disk.CapacityGB = fmt.Sprintf("%.2f", float64(capVal)/1024/1024/1024)
				disk.UsedGB = fmt.Sprintf("%.2f", float64(allocVal)/1024/1024/1024)
			}
		} else if disk.Path != "" {
			// offline: use qemu-img info
			qemuInfo := utils.ExecShell(fmt.Sprintf("qemu-img info --output=json -U %s 2>/dev/null", utils.ShellSingleQuote(disk.Path)))
			if qemuInfo.Error == nil {
				disk.CapacityGB = ParseQemuInfoGB(qemuInfo.Stdout, "virtual-size")
				disk.UsedGB = ParseQemuInfoGB(qemuInfo.Stdout, "actual-size")
				// if format is still empty, get it from qemu-img info
				if disk.Format == "" {
					disk.Format = ParseQemuInfoStr(qemuInfo.Stdout, "format")
				}
			}
		}

		// fill IOPS limits
		if iops, ok := diskIOPSMap[disk.Device]; ok {
			disk.IOPSTotal = IOPSField{Value: iops.TotalIopsSec, IsSet: true}
			disk.IOPSRead = IOPSField{Value: iops.ReadIopsSec, IsSet: true}
			disk.IOPSWrite = IOPSField{Value: iops.WriteIopsSec, IsSet: true}
		}

		disks = append(disks, disk)
	}

	return disks, nil
}

// parseDiskXMLInfo parses format, device type, and bus info from VM XML.
func parseDiskXMLInfo(vmName string) map[string]diskXMLInfo {
	result := make(map[string]diskXMLInfo)

	xmlStr, err := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
	if err != nil {
		return result
	}

	lines := strings.Split(xmlStr, "\n")
	var currentDev string
	var currentInfo diskXMLInfo
	inDisk := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// find <disk ... device='xxx'>
		if strings.HasPrefix(trimmed, "<disk ") {
			inDisk = true
			currentInfo = diskXMLInfo{}
			if strings.Contains(trimmed, "device='disk'") {
				currentInfo.DeviceType = "disk"
			} else if strings.Contains(trimmed, "device='cdrom'") {
				currentInfo.DeviceType = "cdrom"
			}
		}

		if inDisk {
			// <driver ... type='qcow2'/>
			if strings.Contains(trimmed, "<driver") && strings.Contains(trimmed, "type='") {
				parts := strings.Split(trimmed, "type='")
				if len(parts) > 1 {
					currentInfo.Format = strings.Split(parts[1], "'")[0]
				}
			}
			// <target dev='vda' bus='virtio'/>
			if strings.Contains(trimmed, "<target") {
				if strings.Contains(trimmed, "dev='") {
					parts := strings.Split(trimmed, "dev='")
					if len(parts) > 1 {
						currentDev = strings.Split(parts[1], "'")[0]
					}
				}
				if strings.Contains(trimmed, "bus='") {
					parts := strings.Split(trimmed, "bus='")
					if len(parts) > 1 {
						currentInfo.Bus = strings.Split(parts[1], "'")[0]
					}
				}
			}
			if strings.Contains(trimmed, "</disk>") {
				if currentDev != "" {
					result[currentDev] = currentInfo
				}
				inDisk = false
				currentDev = ""
			}
		}
	}

	return result
}

// ParseQemuInfoStr parses a string value from qemu-img info JSON (top-level field only).
func ParseQemuInfoStr(output, key string) string {
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return ""
	}
	raw, ok := data[key]
	if !ok {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return ""
	}
	return val
}

// ParseQemuInfoGB parses a capacity value from qemu-img info JSON (top-level field only,
// avoiding interference from same-named fields in children).
func ParseQemuInfoGB(output, key string) string {
	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return "-"
	}
	raw, ok := data[key]
	if !ok {
		return "-"
	}
	var bytes int64
	if err := json.Unmarshal(raw, &bytes); err != nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", float64(bytes)/1024/1024/1024)
}

// SetDiskBus changes the drive type of an existing disk (requires shutdown).
func SetDiskBus(vmName, device, newBus string) error {
	if err := EnsureNotMigrating(vmName, "修改磁盘驱动类型"); err != nil {
		return err
	}
	state, _ := libvirt_rpc.GetDomainStateRPC(vmName)
	if state == "running" {
		return fmt.Errorf("修改磁盘驱动类型需要先关机")
	}

	// get current XML
	xmlResult, err := libvirt_rpc.GetDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", err)
	}

	// compute new device name: keep letter suffix, replace prefix
	_ = device[:2]       // oldPrefix: vd/sd/hd
	letter := device[2:] // a/b/c...
	newPrefix := GetDevPrefix(newBus)
	newDev := newPrefix + letter

	// check if the new device name conflicts with existing disks (e.g. CDROMs on sata bus)
	existingDisks, listErr := ListDisks(vmName)
	if listErr == nil {
		usedDevs := make(map[string]bool)
		for _, d := range existingDisks {
			// skip the device being changed (it will be renamed)
			if d.Device == device {
				continue
			}
			usedDevs[d.Device] = true
		}
		if usedDevs[newDev] {
			// find next available letter
			found := false
			for _, l := range "bcdefghijklmnopqrstuvwxyz" {
				candidate := newPrefix + string(l)
				if !usedDevs[candidate] {
					newDev = candidate
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("没有可用的设备名（所有 %s* 均已被占用）", newPrefix)
			}
		}
	}

	// parse and modify XML
	xmlStr := xmlResult
	lines := strings.Split(xmlStr, "\n")
	var newLines []string
	inTargetDisk := false
	foundTarget := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// detect entering <disk> block
		if strings.HasPrefix(trimmed, "<disk ") {
			inTargetDisk = false
		}

		// detect target device
		if strings.Contains(trimmed, "<target") && strings.Contains(trimmed, "dev='"+device+"'") {
			inTargetDisk = true
			foundTarget = true
			// replace dev and bus
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			line = fmt.Sprintf("%s<target dev='%s' bus='%s'/>", indent, newDev, newBus)
		}

		// if inside target disk block, remove old address (let libvirt auto-assign)
		if inTargetDisk && strings.Contains(trimmed, "<address ") {
			continue
		}

		if inTargetDisk && strings.Contains(trimmed, "</disk>") {
			inTargetDisk = false
		}

		newLines = append(newLines, line)
	}

	if !foundTarget {
		return fmt.Errorf("未找到设备 %s", device)
	}

	newXML := strings.Join(newLines, "\n")
	if _, err := libvirt_rpc.DefineDomainXMLRPC(newXML); err != nil {
		return fmt.Errorf("修改磁盘驱动失败: %w", err)
	}

	return nil
}

// ResizeDisk expands a disk to the specified size in GB.
func ResizeDisk(vmName, device string, newSizeGB int) error {
	if err := EnsureNotMigrating(vmName, "扩容磁盘"); err != nil {
		return err
	}
	vmState, _ := libvirt_rpc.GetDomainStateRPC(vmName)

	// safety check: refuse resize if external snapshots exist
	hasExtSnap, extSnapNames, _ := CheckSnapshotSafety(vmName)
	if hasExtSnap {
		return fmt.Errorf("虚拟机存在外部快照（%s），扩容后恢复快照可能导致数据不一致。请先删除这些快照后再进行扩容操作",
			strings.Join(extSnapNames, "、"))
	}

	// get disk path (from XML)
	domainXML, xmlErr := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
	if xmlErr != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", xmlErr)
	}
	blkList := libvirt_rpc.ParseDisksFromDomainXML(domainXML)
	diskPath := ""
	for _, blk := range blkList {
		if blk.Target == device {
			diskPath = blk.Source
			break
		}
	}

	if vmState == "running" {
		// 在线扩容：仅扩容磁盘文件，需要用户在虚拟机内部手动扩展分区和文件系统
		newSizeBytes := uint64(newSizeGB) * 1024 * 1024 * 1024
		if err := libvirt_rpc.BlockResizeRPC(vmName, device, newSizeBytes, libvirt.DomainBlockResizeBytes); err != nil {
			return fmt.Errorf("热扩容失败: %w", err)
		}
		logger.App.Info("磁盘热扩容完成，请在虚拟机内部手动扩展分区和文件系统", "vm", vmName, "device", device)
	} else {
		// 离线扩容：先扩容磁盘文件，再自动扩展分区和文件系统
		if diskPath == "" {
			return fmt.Errorf("无法获取磁盘路径")
		}

		// Step 1: 扩容磁盘文件
		logger.App.Info("开始离线扩容磁盘文件", "vm", vmName, "device", device, "path", diskPath, "new_size_gb", newSizeGB)
		result := utils.ExecCommand("qemu-img", "resize", diskPath, fmt.Sprintf("%dG", newSizeGB))
		if result.Error != nil {
			return fmt.Errorf("扩容磁盘文件失败: %s", result.Stderr)
		}
		logger.App.Info("磁盘文件扩容完成", "vm", vmName, "device", device)

		// Step 2: 自动扩展分区和文件系统
		if err := autoExpandGuestDiskPartition(diskPath); err != nil {
			logger.App.Warn("自动扩容分区和文件系统失败，需要用户手动在虚拟机内部调整", "vm", vmName, "device", device, "error", err)
			// 自动扩容失败不阻断整体流程，磁盘文件已成功扩容
		} else {
			logger.App.Info("自动扩容分区和文件系统完成", "vm", vmName, "device", device)
		}
	}

	return nil
}

// autoExpandGuestDiskPartition 自动扩展虚拟机磁盘的分区和文件系统
// 支持 Windows (NTFS) 和 Linux (ext2/ext3/ext4)
func autoExpandGuestDiskPartition(diskPath string) error {
	// 检测磁盘分区布局
	layout, err := inspectGuestDiskLayout(diskPath)
	if err != nil {
		return fmt.Errorf("检测磁盘分区布局失败: %w", err)
	}

	if layout.SectorSize <= 0 || layout.DiskSectors <= 0 {
		return fmt.Errorf("磁盘扇区信息无效")
	}

	lastPartition := layout.lastPartition()
	if lastPartition == nil {
		return fmt.Errorf("未找到分区")
	}

	lastUsableSector := layout.lastUsableSector()
	lastPartitionEndSector := bytesToSectorEnd(lastPartition.EndBytes, layout.SectorSize)
	if lastUsableSector-lastPartitionEndSector < defaultPartitionAlignmentSectors {
		return fmt.Errorf("磁盘空间已充分利用，无需扩容")
	}

	// 根据文件系统类型选择扩容方式
	var expandErr error
	if strings.EqualFold(lastPartition.FileSystem, "ntfs") {
		// Windows NTFS 文件系统扩容
		expandErr = autoExpandWindowsDisk(context.Background(), diskPath, layout)
	} else if isExtFilesystem(lastPartition.FileSystem) {
		// Linux ext 文件系统扩容
		expandErr = expandLinuxExtPartition(context.Background(), diskPath, layout, *lastPartition, lastUsableSector)
	} else {
		return fmt.Errorf("不支持的文件系统类型: %s", lastPartition.FileSystem)
	}

	return expandErr
}

// autoExpandWindowsDisk 自动扩展 Windows 磁盘分区和文件系统
// 支持两种场景：
// 1. 系统分区是最后一个分区：直接扩展系统分区
// 2. 系统分区后有恢复分区：先移动恢复分区到磁盘末尾，再扩展系统分区
func autoExpandWindowsDisk(ctx context.Context, diskPath string, layout *guestDiskLayout) error {
	logger.App.Info("开始自动扩展 Windows 磁盘", "disk", diskPath)

	// 查找 Windows 系统分区（最大的 NTFS 分区，排除恢复分区）
	osPart := findWindowsOSPartition(layout)
	if osPart == nil {
		return fmt.Errorf("未找到可扩展的 Windows NTFS 系统分区")
	}
	logger.App.Info("找到 Windows 系统分区", "partition", osPart.Num, "size_bytes", osPart.SizeBytes)

	lastPartition := layout.lastPartition()
	if lastPartition == nil {
		return fmt.Errorf("未找到分区")
	}

	// 查找系统分区之后的恢复分区
	recoveryPart := findRecoveryPartitionAfter(layout, osPart)
	if recoveryPart != nil && recoveryPart.Num == lastPartition.Num && strings.EqualFold(recoveryPart.FileSystem, "ntfs") {
		// 场景：系统分区后有恢复分区，且恢复分区是最后一个分区
		logger.App.Info("系统分区后存在恢复分区，需要先移动恢复分区", "recovery_part", recoveryPart.Num)
		return moveRecoveryAndExpandWindowsPartition(ctx, diskPath, layout, *osPart, *recoveryPart)
	}

	if osPart.Num == lastPartition.Num {
		// 场景：系统分区是最后一个分区，直接扩展
		logger.App.Info("系统分区是最后一个分区，直接扩展", "partition", osPart.Num)
		return expandLastWindowsPartition(ctx, diskPath, layout, *osPart)
	}

	return fmt.Errorf("系统分区后存在非恢复分区，无法安全自动扩容")
}

// findWindowsOSPartition 查找 Windows 系统分区（最大的 NTFS 分区，排除恢复分区）
func findWindowsOSPartition(layout *guestDiskLayout) *guestDiskPartition {
	var selected *guestDiskPartition
	for i := range layout.Partitions {
		part := &layout.Partitions[i]
		if !strings.EqualFold(part.FileSystem, "ntfs") || isWindowsRecoveryPartition(part) {
			continue
		}
		if selected == nil || part.SizeBytes > selected.SizeBytes {
			selected = part
		}
	}
	return selected
}

// findRecoveryPartitionAfter 查找系统分区之后的恢复分区
func findRecoveryPartitionAfter(layout *guestDiskLayout, osPart *guestDiskPartition) *guestDiskPartition {
	for i := range layout.Partitions {
		part := &layout.Partitions[i]
		if part.StartBytes <= osPart.EndBytes {
			continue
		}
		if isWindowsRecoveryPartition(part) {
			return part
		}
	}
	return nil
}

// isWindowsRecoveryPartition 判断是否为 Windows 恢复分区
func isWindowsRecoveryPartition(part *guestDiskPartition) bool {
	const windowsRecoveryPartitionTypeGUID = "DE94BBA4-06D1-4D40-A16A-BFD50179D6AC"
	return strings.EqualFold(part.GPTType, windowsRecoveryPartitionTypeGUID) ||
		strings.Contains(strings.ToLower(part.Name), "recovery")
}

// moveRecoveryAndExpandWindowsPartition 移动恢复分区并扩展系统分区
func moveRecoveryAndExpandWindowsPartition(ctx context.Context, diskPath string, layout *guestDiskLayout, osPart, recoveryPart guestDiskPartition) error {
	lastUsableSector := layout.lastUsableSector()
	recoverySizeSectors := bytesToSectorsCeil(recoveryPart.SizeBytes, layout.SectorSize)
	newRecoveryStart := alignDown(lastUsableSector-recoverySizeSectors+1, defaultPartitionAlignmentSectors)
	newOSEnd := newRecoveryStart - 1
	if newOSEnd <= bytesToSectorEnd(osPart.EndBytes, layout.SectorSize) {
		return fmt.Errorf("没有足够空间扩展系统分区")
	}

	// 创建恢复分区备份文件
	backupPath := filepath.Join("/tmp", fmt.Sprintf("_kvm-console-recovery-%d.ntfsclone", time.Now().UnixNano()))
	logger.App.Info("创建恢复分区备份", "backup_path", backupPath)

	var commands []string
	commands = append(commands,
		"run",
		fmt.Sprintf("part-expand-gpt %s", layout.Device),
		// 备份恢复分区
		fmt.Sprintf("ntfsclone-out %s%d %s force:true", layout.Device, recoveryPart.Num, backupPath),
		// 删除恢复分区
		fmt.Sprintf("part-del %s %d", layout.Device, recoveryPart.Num),
		// 扩展系统分区
		fmt.Sprintf("part-resize %s %d %d", layout.Device, osPart.Num, newOSEnd),
		// 在磁盘末尾重新创建恢复分区
		fmt.Sprintf("part-add %s p %d -%d", layout.Device, newRecoveryStart, gptBackupReservedSectors),
		// 设置恢复分区的 GPT 类型
		fmt.Sprintf("part-set-gpt-type %s %d %s", layout.Device, recoveryPart.Num, coalesceGUID(recoveryPart.GPTType, "DE94BBA4-06D1-4D40-A16A-BFD50179D6AC")),
	)

	// 设置恢复分区的其他属性
	if recoveryPart.GPTGUID != "" {
		commands = append(commands, fmt.Sprintf("part-set-gpt-guid %s %d %s", layout.Device, recoveryPart.Num, recoveryPart.GPTGUID))
	}
	if recoveryPart.Name != "" {
		commands = append(commands, fmt.Sprintf("part-set-name %s %d %s", layout.Device, recoveryPart.Num, guestfishQuote(recoveryPart.Name)))
	}
	if recoveryPart.GPTAttributes != "" {
		commands = append(commands, fmt.Sprintf("part-set-gpt-attributes %s %d %s", layout.Device, recoveryPart.Num, recoveryPart.GPTAttributes))
	}

	// 重新读取分区表并恢复恢复分区数据
	commands = append(commands,
		fmt.Sprintf("blockdev-rereadpt %s", layout.Device),
		fmt.Sprintf("ntfsclone-in %s %s%d", backupPath, layout.Device, recoveryPart.Num),
		fmt.Sprintf("ntfsfix %s%d", layout.Device, recoveryPart.Num),
	)

	// 执行分区调整
	if err := runWritableGuestfishOperation(ctx, diskPath, commands, []string{backupPath}, "移动恢复分区并扩展系统分区", "操作超时"); err != nil {
		return fmt.Errorf("调整分区失败: %w", err)
	}

	// 扩展 NTFS 文件系统
	logger.App.Info("开始扩展 NTFS 文件系统", "disk", diskPath, "partition", osPart.Num)
	return runWritableGuestfishOperation(ctx, diskPath, []string{
		"run",
		fmt.Sprintf("debug sh %s", guestfishQuote(fmt.Sprintf("ntfsresize -f %s%d", layout.Device, osPart.Num))),
	}, nil, "Windows NTFS 扩容", "Windows NTFS 扩容超时")
}

// expandLastWindowsPartition 扩展最后一个 Windows 分区
func expandLastWindowsPartition(ctx context.Context, diskPath string, layout *guestDiskLayout, osPart guestDiskPartition) error {
	lastUsableSector := layout.lastUsableSector()
	currentEnd := bytesToSectorEnd(osPart.EndBytes, layout.SectorSize)
	if lastUsableSector <= currentEnd {
		return fmt.Errorf("没有足够空间扩展分区")
	}

	// 构建扩展分区命令
	commands := []string{"run"}
	if strings.EqualFold(layout.PartType, "gpt") {
		commands = append(commands, fmt.Sprintf("part-expand-gpt %s", layout.Device))
	}
	commands = append(commands,
		fmt.Sprintf("part-resize %s %d %d", layout.Device, osPart.Num, lastUsableSector),
		fmt.Sprintf("blockdev-rereadpt %s", layout.Device),
	)

	// 执行分区调整
	if err := runWritableGuestfishOperation(ctx, diskPath, commands, nil, "扩展 Windows 分区", "分区扩容超时"); err != nil {
		return fmt.Errorf("调整分区失败: %w", err)
	}

	// 扩展 NTFS 文件系统
	logger.App.Info("开始扩展 NTFS 文件系统", "disk", diskPath, "partition", osPart.Num)
	return runWritableGuestfishOperation(ctx, diskPath, []string{
		"run",
		fmt.Sprintf("debug sh %s", guestfishQuote(fmt.Sprintf("ntfsresize -f %s%d", layout.Device, osPart.Num))),
	}, nil, "Windows NTFS 扩容", "Windows NTFS 扩容超时")
}

// expandLinuxExtPartition 扩展 Linux ext 分区和文件系统
func expandLinuxExtPartition(ctx context.Context, diskPath string, layout *guestDiskLayout, part guestDiskPartition, lastUsableSector int64) error {
	logger.App.Info("开始扩展 Linux ext 分区", "disk", diskPath, "partition", part.Num, "filesystem", part.FileSystem)

	partDev := fmt.Sprintf("%s%d", layout.Device, part.Num)

	// 构建 guestfish 命令扩展分区和 ext 文件系统
	commands := []string{"run"}
	if strings.EqualFold(layout.PartType, "gpt") {
		commands = append(commands, fmt.Sprintf("part-expand-gpt %s", layout.Device))
	}
	commands = append(commands,
		fmt.Sprintf("part-resize %s %d %d", layout.Device, part.Num, lastUsableSector),
		fmt.Sprintf("blockdev-rereadpt %s", layout.Device),
		fmt.Sprintf("e2fsck-f %s", partDev),
		fmt.Sprintf("resize2fs %s", partDev),
	)

	return runWritableGuestfishOperation(ctx, diskPath, commands, nil, "Linux ext 分区扩容", "Linux ext 分区扩容超时")
}

// ==================== 以下为复制自 service 包的辅助函数 ====================

type guestDiskPartition struct {
	Num           int
	StartBytes    int64
	EndBytes      int64
	SizeBytes     int64
	FileSystem    string
	GPTType       string
	GPTGUID       string
	Name          string
	GPTAttributes string
}

type guestDiskLayout struct {
	Device      string
	PartType    string
	SectorSize  int64
	DiskSectors int64
	Partitions  []guestDiskPartition
}

const (
	defaultPartitionAlignmentSectors = int64(2048)
	gptBackupReservedSectors         = int64(34)
)

func inspectGuestDiskLayout(diskPath string) (*guestDiskLayout, error) {
	device := "/dev/sda"
	script := fmt.Sprintf(`guestfish --ro -a %s <<'GUESTFISH'
run
echo __PARTTYPE__
part-get-parttype %s
echo __SECTOR_SIZE__
blockdev-getss %s
echo __DISK_SECTORS__
blockdev-getsz %s
echo __PART_LIST__
part-list %s
echo __FILESYSTEMS__
list-filesystems
GUESTFISH`, utils.ShellSingleQuote(diskPath), device, device, device, device)

	result := utils.ExecShellWithTimeout(script, 2*time.Minute)
	if result.Error != nil {
		return nil, fmt.Errorf("%s", result.Stderr)
	}

	layout, err := parseGuestDiskLayout(result.Stdout, device)
	if err != nil {
		return nil, err
	}

	return layout, nil
}

func parseGuestDiskLayout(output, device string) (*guestDiskLayout, error) {
	layout := &guestDiskLayout{Device: device}
	section := ""
	partitions := make(map[int]*guestDiskPartition)
	fileSystems := make(map[string]string)

	partRe := regexp.MustCompile(`part_(num|start|end|size):\s+([0-9]+)`)
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "__") && strings.HasSuffix(line, "__") {
			section = line
			continue
		}

		switch section {
		case "__PARTTYPE__":
			layout.PartType = line
		case "__SECTOR_SIZE__":
			layout.SectorSize, _ = strconv.ParseInt(line, 10, 64)
		case "__DISK_SECTORS__":
			layout.DiskSectors, _ = strconv.ParseInt(line, 10, 64)
		case "__PART_LIST__":
			matches := partRe.FindStringSubmatch(line)
			if len(matches) != 3 {
				continue
			}
			value, _ := strconv.ParseInt(matches[2], 10, 64)
			if matches[1] == "num" {
				partitions[int(value)] = &guestDiskPartition{Num: int(value)}
				continue
			}
			part := latestPartition(partitions)
			if part == nil {
				continue
			}
			switch matches[1] {
			case "start":
				part.StartBytes = value
			case "end":
				part.EndBytes = value
			case "size":
				part.SizeBytes = value
			}
		case "__FILESYSTEMS__":
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				fileSystems[strings.TrimSuffix(fields[0], ":")] = strings.ToLower(fields[1])
			}
		}
	}

	for _, part := range partitions {
		part.FileSystem = fileSystems[fmt.Sprintf("%s%d", device, part.Num)]
		layout.Partitions = append(layout.Partitions, *part)
	}
	sort.Slice(layout.Partitions, func(i, j int) bool {
		return layout.Partitions[i].StartBytes < layout.Partitions[j].StartBytes
	})

	return layout, nil
}

func latestPartition(partitions map[int]*guestDiskPartition) *guestDiskPartition {
	var latest *guestDiskPartition
	for _, part := range partitions {
		if latest == nil || part.Num > latest.Num {
			latest = part
		}
	}
	return latest
}

func (layout *guestDiskLayout) lastPartition() *guestDiskPartition {
	if len(layout.Partitions) == 0 {
		return nil
	}
	return &layout.Partitions[len(layout.Partitions)-1]
}

func (layout *guestDiskLayout) lastUsableSector() int64 {
	if strings.EqualFold(layout.PartType, "gpt") {
		return layout.DiskSectors - gptBackupReservedSectors
	}
	return layout.DiskSectors - 1
}

func isExtFilesystem(fs string) bool {
	switch strings.ToLower(strings.TrimSpace(fs)) {
	case "ext2", "ext3", "ext4":
		return true
	default:
		return false
	}
}

func bytesToSectorEnd(endByte, sectorSize int64) int64 {
	if sectorSize <= 0 {
		return 0
	}
	return endByte / sectorSize
}

// bytesToSectorsCeil 将字节数向上取整转换为扇区数
func bytesToSectorsCeil(bytes, sectorSize int64) int64 {
	if sectorSize <= 0 {
		return 0
	}
	return (bytes + sectorSize - 1) / sectorSize
}

// alignDown 将值向下对齐到指定的对齐边界
func alignDown(value, alignment int64) int64 {
	if alignment <= 0 {
		return value
	}
	return value - value%alignment
}

// coalesceGUID 如果值为空则返回默认值，否则返回原值
func coalesceGUID(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// guestfishQuote 对 guestfish 命令参数进行引号转义
func guestfishQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func runWritableGuestfishOperation(ctx context.Context, diskPath string, commands []string, cleanupPaths []string, operationName, timeoutMessage string) error {
	var b strings.Builder
	b.WriteString("set +e\n")
	b.WriteString(fmt.Sprintf("guestfish -a %s <<'GUESTFISH'\n", utils.ShellSingleQuote(diskPath)))
	for _, command := range commands {
		b.WriteString(command)
		b.WriteByte('\n')
	}
	b.WriteString("GUESTFISH\n")
	b.WriteString("guestfish_status=$?\n")
	for _, cleanupPath := range cleanupPaths {
		b.WriteString(fmt.Sprintf("rm -f %s\n", utils.ShellSingleQuote(cleanupPath)))
	}
	b.WriteString("exit $guestfish_status\n")

	result := utils.ExecShellContextWithTimeout(ctx, b.String(), 10*time.Minute)
	if result.Error != nil {
		for _, cleanupPath := range cleanupPaths {
			_ = os.Remove(cleanupPath)
		}
		if ctx != nil && ctx.Err() != nil {
			return fmt.Errorf("%s已取消", operationName)
		}
		if strings.TrimSpace(result.Stderr) == "命令执行超时" {
			return fmt.Errorf("%s", timeoutMessage)
		}
		return fmt.Errorf("%s", result.Stderr)
	}
	return nil
}

// RemoveDisk detaches a disk from a VM and optionally deletes the file.
func RemoveDisk(vmName, device string, deleteFile bool) error {
	if err := EnsureNotMigrating(vmName, "删除磁盘"); err != nil {
		return err
	}
	vmState, _ := libvirt_rpc.GetDomainStateRPC(vmName)

	// get disk path and full disk XML from domain definition
	domainXML, xmlErr := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
	if xmlErr != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", xmlErr)
	}
	blkList := libvirt_rpc.ParseDisksFromDomainXML(domainXML)
	diskPath := ""
	for _, blk := range blkList {
		if blk.Target == device {
			diskPath = blk.Source
			break
		}
	}

	// extract the full <disk> XML block for the target device
	// using complete XML ensures the detach succeeds for both live and config,
	// avoiding the issue where virsh domblklist still shows the disk after detach
	fullDiskXML, extractErr := ExtractFullDiskXML(domainXML, device)
	if extractErr != nil {
		logger.Libvirt.Warn("提取完整磁盘XML失败，使用简化XML作为fallback", "device", device, "error", extractErr)
		// fallback: use simplified XML if extraction fails (should not happen normally)
		fullDiskXML = fmt.Sprintf("<disk type='file' device='disk'>\n  <target dev='%s'/>\n</disk>", device)
	}

	// detach disk using full XML definition
	var detachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		// virsh detach-disk --persistent = live + config
		detachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := libvirt_rpc.DetachDeviceFlagsRPC(vmName, fullDiskXML, detachFlags); err != nil {
		return fmt.Errorf("分离磁盘 %s 失败: %w", device, err)
	}

	// verify the disk has been removed (only for running VMs, where detach is async)
	if vmState == "running" {
		for i := 0; i < 10; i++ {
			time.Sleep(time.Second)
			if !DiskDeviceExists(vmName, device) {
				break
			}
			if i == 9 {
				return fmt.Errorf("热删除磁盘超时: 设备 %s 仍然存在", device)
			}
		}
	}

	// delete file
	if deleteFile && diskPath != "" {
		_ = os.Remove(diskPath)
	}

	return nil
}
