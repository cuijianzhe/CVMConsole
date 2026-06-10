package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/digitalocean/go-libvirt"
	"kvm_console/config"
	"kvm_console/utils"
)

// DiskInfo 磁盘信息
type DiskInfo struct {
	Device     string `json:"device"`      // 设备名（如 vda, vdb）
	Path       string `json:"path"`        // 磁盘文件路径
	CapacityGB string `json:"capacity_gb"` // 容量（GB）
	UsedGB     string `json:"used_gb"`     // 占用（GB）
	Bus        string `json:"bus"`         // 总线类型
	Format     string `json:"format"`      // 磁盘格式 qcow2/raw
	DeviceType string `json:"device_type"` // disk/cdrom
	HotSupport bool   `json:"hot_support"` // 是否支持热操作
	// IOPS 限制（0 表示无限制）
	IOPSTotal iopsField `json:"iops_total"` // 总 IOPS 限制
	IOPSRead  iopsField `json:"iops_read"`  // 读 IOPS 限制
	IOPSWrite iopsField `json:"iops_write"` // 写 IOPS 限制
}

type iopsField struct {
	Value int  `json:"value"`
	IsSet bool `json:"is_set"`
}

// DiskSimpleInfo 磁盘简要信息（用于删除确认界面）
type DiskSimpleInfo struct {
	Device     string `json:"device"`      // 设备名
	Path       string `json:"path"`        // 磁盘文件路径
	CapacityGB string `json:"capacity_gb"` // 容量（GB）
	Format     string `json:"format"`      // 磁盘格式
	IsSystem   bool   `json:"is_system"`   // 是否是系统盘（第一个磁盘）
	SizeBytes  int64  `json:"size_bytes"`  // 磁盘文件实际占用字节数
}

// diskXMLInfo 从 XML 中提取的磁盘附加信息
type diskXMLInfo struct {
	Format     string
	DeviceType string
	Bus        string
}

// ListDisks 列出虚拟机磁盘
func ListDisks(vmName string) ([]DiskInfo, error) {
	state, _ := getDomainStateRPC(vmName)

	domainXML, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return nil, fmt.Errorf("获取磁盘列表失败: %w", err)
	}

	// 从 XML 解析块设备列表（替代 virsh domblklist）
	blkList := parseDisksFromDomainXML(domainXML)

	// 从 XML 获取每个磁盘的详细信息（格式、设备类型、总线、IOPS）
	diskXMLMap := parseDiskXMLInfo(vmName)
	diskIOPSMap := ParseAllDiskIOPSTune(vmName)

	var disks []DiskInfo

	for _, blk := range blkList {
		device := blk.Target
		path := blk.Source

		// 跳过没有 target 的设备
		if device == "" {
			continue
		}

		disk := DiskInfo{
			Device: device,
			Path:   path,
		}

		// 从 XML 中获取信息
		if xmlInfo, ok := diskXMLMap[disk.Device]; ok {
			disk.Format = xmlInfo.Format
			disk.DeviceType = xmlInfo.DeviceType
			disk.Bus = xmlInfo.Bus
		}

		// 跳过 source 为空或 "-" 的普通磁盘（但保留空光驱）
		if (path == "" || path == "-") && disk.DeviceType != "cdrom" {
			continue
		}
		// 空光驱清理 path
		if path == "-" {
			disk.Path = ""
		}

		disk.HotSupport = disk.Bus == "virtio" || disk.Bus == "scsi"

		// 容量和占用
		if state == "running" && disk.Path != "" {
			capVal, allocVal, _, blkErr := getBlockInfoRPC(vmName, disk.Device)
			if blkErr == nil {
				disk.CapacityGB = fmt.Sprintf("%.2f", float64(capVal)/1024/1024/1024)
				disk.UsedGB = fmt.Sprintf("%.2f", float64(allocVal)/1024/1024/1024)
			}
		} else if disk.Path != "" {
			// 关机时也能用 qemu-img info 获取
			qemuInfo := utils.ExecShell(fmt.Sprintf("qemu-img info --output=json -U %s 2>/dev/null", utils.ShellSingleQuote(disk.Path)))
			if qemuInfo.Error == nil {
				disk.CapacityGB = parseQemuInfoGB(qemuInfo.Stdout, "virtual-size")
				disk.UsedGB = parseQemuInfoGB(qemuInfo.Stdout, "actual-size")
				// 如果 format 还是空，从 qemu-img info 获取
				if disk.Format == "" {
					disk.Format = parseQemuInfoStr(qemuInfo.Stdout, "format")
				}
			}
		}

		// 填充 IOPS 限制信息
		if iops, ok := diskIOPSMap[disk.Device]; ok {
			disk.IOPSTotal = iopsField{Value: iops.TotalIopsSec, IsSet: true}
			disk.IOPSRead = iopsField{Value: iops.ReadIopsSec, IsSet: true}
			disk.IOPSWrite = iopsField{Value: iops.WriteIopsSec, IsSet: true}
		}

		disks = append(disks, disk)
	}

	return disks, nil
}

// parseDiskXMLInfo 从虚拟机 XML 解析所有磁盘的格式、设备类型、总线信息
func parseDiskXMLInfo(vmName string) map[string]diskXMLInfo {
	result := make(map[string]diskXMLInfo)

	xmlStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return result
	}

	lines := strings.Split(xmlStr, "\n")
	var currentDev string
	var currentInfo diskXMLInfo
	inDisk := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 找到 <disk ... device='xxx'>
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

// parseQemuInfoStr 从 qemu-img info JSON 中解析字符串值（仅读取顶层字段）
func parseQemuInfoStr(output, key string) string {
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

// AttachExistingDisk 挂载已有磁盘文件到虚拟机
func AttachExistingDisk(vmName, diskPath, bus string) (string, error) {
	if err := EnsureVMNotMigrating(vmName, "挂载磁盘"); err != nil {
		return "", err
	}
	if bus == "" {
		bus = "virtio"
	}

	// 检查文件是否存在
	checkResult := utils.ExecShell(fmt.Sprintf("test -f %s && echo ok", utils.ShellSingleQuote(diskPath)))
	if checkResult.Stdout != "ok" {
		return "", fmt.Errorf("磁盘文件不存在: %s", diskPath)
	}

	// 检查文件可读性（确保 libvirt 有权限）
	readResult := utils.ExecShell(fmt.Sprintf("test -r %s && echo ok", utils.ShellSingleQuote(diskPath)))
	if strings.TrimSpace(readResult.Stdout) != "ok" {
		return "", fmt.Errorf("磁盘文件不可读（权限不足）: %s，请确保文件归 libvirt-qemu:kvm 所有", diskPath)
	}

	// 如果文件在用户存储目录中，移动到默认磁盘目录
	storageMountPoint := GetStorageMountPoint()
	if storageMountPoint != "" && strings.HasPrefix(diskPath, storageMountPoint) {
		destDir := config.GlobalConfig.CloneDir
		filename := filepath.Base(diskPath)
		destPath := filepath.Join(destDir, filename)

		// 检查目标文件是否已存在，加时间戳避免冲突
		destCheck := utils.ExecShell(fmt.Sprintf("test -f %s && echo exists", utils.ShellSingleQuote(destPath)))
		if strings.TrimSpace(destCheck.Stdout) == "exists" {
			ts := time.Now().Format("20060102150405")
			ext := filepath.Ext(filename)
			nameOnly := strings.TrimSuffix(filename, ext)
			destPath = filepath.Join(destDir, fmt.Sprintf("%s_%s%s", nameOnly, ts, ext))
		}

		// 移动文件
		mvResult := utils.ExecShell(fmt.Sprintf("mv %s %s", utils.ShellSingleQuote(diskPath), utils.ShellSingleQuote(destPath)))
		if mvResult.Error != nil {
			return "", fmt.Errorf("移动磁盘文件到默认目录失败: %s", mvResult.Stderr)
		}
		// 设置权限
		utils.ExecCommand("chown", "libvirt-qemu:kvm", destPath)
		diskPath = destPath
	}

	vmState, _ := getDomainStateRPC(vmName)

	// 检测磁盘格式
	format := "qcow2"
	infoResult := utils.ExecCommand("qemu-img", "info", "--output=json", diskPath)
	if infoResult.Error == nil {
		detected := parseQemuInfoStr(infoResult.Stdout, "format")
		if detected != "" {
			format = detected
		}
	}

	// 根据总线类型确定设备前缀
	devPrefix := getDevPrefix(bus)

	// 查找可用设备名
	existingDisks, _ := ListDisks(vmName)
	usedDevs := make(map[string]bool)
	for _, d := range existingDisks {
		usedDevs[d.Device] = true
	}

	nextDev := ""
	for _, letter := range "bcdefghijklmnop" {
		dev := devPrefix + string(letter)
		if !usedDevs[dev] {
			nextDev = dev
			break
		}
	}
	if nextDev == "" {
		return "", fmt.Errorf("没有可用的设备名")
	}

	// 使用 attach-device + XML 方式挂载，以支持 discard 和 detect_zeroes 参数
	diskXML := fmt.Sprintf(
		"<disk type='file' device='disk'>\n"+
			"  <driver name='qemu' type='%s' discard='unmap' detect_zeroes='unmap'/>\n"+
			"  <source file='%s'/>\n"+
			"  <target dev='%s' bus='%s'/>\n"+
			"</disk>",
		format, diskPath, nextDev, bus)

	// 运行中热添加时，q35/PCIe 机型需要补全 PCI 地址以使用空闲的 pcie-root-port
	if vmState == "running" {
		hotplugXML, err := buildDiskHotplugXML(vmName, diskXML)
		if err != nil {
			// PCIe 插槽不足，尝试降级为 scsi 总线（需已有 virtio-scsi 控制器）
			if bus == "virtio" && strings.Contains(err.Error(), ErrNoPCIESlots.Error()) {
				scsiDev, scsiErr := tryFallbackDiskToSCSI(vmName, diskPath, format, existingDisks, vmState)
				if scsiErr == nil {
					return scsiDev, nil
				}
			}
			return "", err
		}
		diskXML = hotplugXML
	}

	// attach-device: running=live+config(3), stopped=config(2)
	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, diskXML, attachFlags); err != nil {
		return "", fmt.Errorf("挂载磁盘失败: %w", err)
	}

	return nextDev, nil
}

// AddDisk 添加磁盘（兼容接口，默认使用 virtio 总线）
func AddDisk(vmName string, sizeGB int, format string) (string, error) {
	return AddDiskWithBus(vmName, sizeGB, format, "virtio")
}

// AddDiskWithBus 添加磁盘（支持指定总线类型）
// bus 支持: virtio, scsi, sata, ide
func AddDiskWithBus(vmName string, sizeGB int, format, bus string) (string, error) {
	return AddDiskWithBusInDir(vmName, sizeGB, format, bus, config.GlobalConfig.CloneDir)
}

// AddDiskWithBusInDir 添加磁盘到指定目录（创建 VM 时用于跟随所选存储池）。
func AddDiskWithBusInDir(vmName string, sizeGB int, format, bus, diskDir string) (string, error) {
	if err := EnsureVMNotMigrating(vmName, "添加磁盘"); err != nil {
		return "", err
	}
	if format == "" {
		format = "qcow2"
	}
	if bus == "" {
		bus = "virtio"
	}
	if strings.TrimSpace(diskDir) == "" {
		diskDir = config.GlobalConfig.CloneDir
	}
	vmState, _ := getDomainStateRPC(vmName)

	// 根据总线类型确定设备前缀
	devPrefix := getDevPrefix(bus)

	// 查找可用设备名
	existingDisks, _ := ListDisks(vmName)
	usedDevs := make(map[string]bool)
	for _, d := range existingDisks {
		usedDevs[d.Device] = true
	}

	nextDev := ""
	for _, letter := range "bcdefghijklmnop" {
		dev := devPrefix + string(letter)
		if !usedDevs[dev] {
			nextDev = dev
			break
		}
	}
	if nextDev == "" {
		return "", fmt.Errorf("没有可用的设备名")
	}

	if err := os.MkdirAll(diskDir, 0755); err != nil {
		return "", fmt.Errorf("创建磁盘目录失败: %w", err)
	}
	diskPath := fmt.Sprintf("%s/%s-%s.%s", diskDir, vmName, nextDev, format)

	// 创建磁盘
	result := utils.ExecCommand("qemu-img", "create", "-f", format, diskPath, fmt.Sprintf("%dG", sizeGB))
	if result.Error != nil {
		return "", fmt.Errorf("创建磁盘失败: %s", result.Stderr)
	}

	// 使用 attach-device + XML 方式挂载，以支持 discard 和 detect_zeroes 参数
	diskXML := fmt.Sprintf(
		"<disk type='file' device='disk'>\n"+
			"  <driver name='qemu' type='%s' discard='unmap' detect_zeroes='unmap'/>\n"+
			"  <source file='%s'/>\n"+
			"  <target dev='%s' bus='%s'/>\n"+
			"</disk>",
		format, diskPath, nextDev, bus)

	// 运行中热添加时，q35/PCIe 机型需要补全 PCI 地址以使用空闲的 pcie-root-port
	if vmState == "running" {
		hotplugXML, err := buildDiskHotplugXML(vmName, diskXML)
		if err != nil {
			// PCIe 插槽不足，尝试降级为 scsi 总线（需已有 virtio-scsi 控制器）
			if bus == "virtio" && strings.Contains(err.Error(), ErrNoPCIESlots.Error()) {
				scsiDev, scsiErr := tryFallbackDiskToSCSI(vmName, diskPath, format, existingDisks, vmState)
				if scsiErr == nil {
					return scsiDev, nil
				}
			}
			utils.ExecShell(fmt.Sprintf("rm -f %s", utils.ShellSingleQuote(diskPath)))
			return "", err
		}
		diskXML = hotplugXML
	}

	// attach-device: running=live+config(3), stopped=config(2)
	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, diskXML, attachFlags); err != nil {
		utils.ExecShell(fmt.Sprintf("rm -f %s", utils.ShellSingleQuote(diskPath)))
		return "", fmt.Errorf("挂载磁盘失败: %w", err)
	}

	return nextDev, nil
}

// AddExtraDisksForVM 按额外磁盘配置批量创建并挂载数据盘。
func AddExtraDisksForVM(vmName string, disks []ExtraDiskParam, defaultDir, defaultBus string, isAdmin bool, progressFn func(int, string)) error {
	if len(disks) == 0 {
		return nil
	}
	if progressFn == nil {
		progressFn = func(int, string) {}
	}
	for i, disk := range disks {
		if disk.Size <= 0 {
			continue
		}
		format := strings.TrimSpace(disk.Format)
		if format == "" {
			format = "qcow2"
		}
		if !isAdmin {
			format = "qcow2"
		}
		bus := NormalizeVMDiskBus(disk.Bus)
		if bus == "" {
			bus = NormalizeVMDiskBus(defaultBus)
		}
		if bus == "" {
			bus = "virtio"
		}
		diskDir := strings.TrimSpace(defaultDir)
		if strings.TrimSpace(disk.StoragePoolID) != "" {
			resolvedDir, _, err := ResolveVMStorageDir(disk.StoragePoolID, isAdmin)
			if err != nil {
				return fmt.Errorf("解析额外磁盘 %d 存储位置失败: %w", i+1, err)
			}
			diskDir = resolvedDir
		}
		progressFn(0, fmt.Sprintf("正在挂载额外磁盘 %d...", i+1))
		if _, err := AddDiskWithBusInDir(vmName, disk.Size, format, bus, diskDir); err != nil {
			return fmt.Errorf("挂载额外磁盘 %d 失败: %w", i+1, err)
		}
	}
	return nil
}

// buildDiskHotplugXML 根据虚拟机机型为热添加磁盘补全 PCI 地址。
// 当 VM 为 q35/PCIe 机型时，磁盘必须挂载到空闲的 pcie-root-port 下游总线上，
// 否则 libvirt 会报 "No more available PCI slots"。
// 当 PCIe 插槽已满时，返回 ErrNoPCIESlots 错误供上层降级处理。
func buildDiskHotplugXML(vmName string, diskXML string) (string, error) {
	xmlStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		// 无法获取 XML 时直接返回原始 XML，让 libvirt 自行分配
		return diskXML, nil
	}
	if !hasPCIERootController(xmlStr) {
		// 非 PCIe 机型（i440fx 等），不需要手动分配 PCI 地址
		return diskXML, nil
	}

	freeBus, err := findFreePCIERootPortBus(vmName)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoPCIESlots, err)
	}

	// 在 </disk> 前插入 PCI 地址
	addrLine := fmt.Sprintf("  <address type='pci' domain='0x0000' bus='0x%02x' slot='0x00' function='0x0'/>", freeBus)
	diskXML = strings.Replace(diskXML, "</disk>", addrLine+"\n</disk>", 1)
	return diskXML, nil
}

// ErrNoPCIESlots PCIe 插槽已满时的错误标记，用于触发 scsi 降级逻辑。
var ErrNoPCIESlots = fmt.Errorf("no_pcie_slots")

// tryFallbackDiskToSCSI 当 virtio 磁盘热添加因 PCIe 插槽不足失败时，
// 尝试降级为 scsi 总线以使用已有的 virtio-scsi 控制器。
// 返回降级后的设备名，或错误。
func tryFallbackDiskToSCSI(vmName, diskPath, format string, existingDisks []DiskInfo, vmState string) (string, error) {
	// 1. 确认 VM 有 virtio-scsi 控制器
	xmlStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil || !hasSCSIController(xmlStr) {
		// 尝试创建 virtio-scsi 控制器（需要 PCIe 插槽，通常也会失败）
		if err := ensureHotplugCDROMController(vmName); err != nil {
			return "", fmt.Errorf("PCIe 插槽已满且无可用的 virtio-scsi 控制器（尝试创建也失败: %v）。请先关机后再添加磁盘", err)
		}
	}

	// 2. 计算 scsi 设备名
	usedDevs := make(map[string]bool)
	for _, d := range existingDisks {
		usedDevs[d.Device] = true
	}
	nextDev := ""
	for _, letter := range "abcdefghijklmnop" {
		dev := "sd" + string(letter)
		if !usedDevs[dev] {
			nextDev = dev
			break
		}
	}
	if nextDev == "" {
		return "", fmt.Errorf("没有可用的 scsi 设备名")
	}

	// 3. 构建 scsi 总线 XML（无需 PCIe 地址）
	diskXML := fmt.Sprintf(
		"<disk type='file' device='disk'>\n"+
			"  <driver name='qemu' type='%s' discard='unmap' detect_zeroes='unmap'/>\n"+
			"  <source file='%s'/>\n"+
			"  <target dev='%s' bus='scsi'/>\n"+
			"</disk>",
		format, diskPath, nextDev)

	// attach-device: running=live+config(3), stopped=config(2)
	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, diskXML, attachFlags); err != nil {
		return "", fmt.Errorf("scsi 降级挂载失败: %w", err)
	}

	return nextDev, nil
}

// getDevPrefix 根据总线类型返回设备前缀
func getDevPrefix(bus string) string {
	switch bus {
	case "virtio":
		return "vd"
	case "scsi", "sata":
		return "sd"
	case "ide":
		return "hd"
	default:
		return "vd"
	}
}

// SetDiskBus 修改已有磁盘的驱动类型（需要关机）
func SetDiskBus(vmName, device, newBus string) error {
	if err := EnsureVMNotMigrating(vmName, "修改磁盘驱动类型"); err != nil {
		return err
	}
	state, _ := getDomainStateRPC(vmName)
	if state == "running" {
		return fmt.Errorf("修改磁盘驱动类型需要先关机")
	}

	// 获取当前 XML
	xmlResult, err := getDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", err)
	}

	// 计算新的设备名：保留原字母后缀，替换前缀
	oldPrefix := device[:2] // vd/sd/hd
	letter := device[2:]    // a/b/c...
	newPrefix := getDevPrefix(newBus)
	newDev := newPrefix + letter
	_ = oldPrefix // 避免未使用

	// 解析并修改 XML
	xmlStr := xmlResult
	lines := strings.Split(xmlStr, "\n")
	var newLines []string
	inTargetDisk := false
	foundTarget := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检测进入 <disk> 块
		if strings.HasPrefix(trimmed, "<disk ") {
			inTargetDisk = false
		}

		// 检测目标设备
		if strings.Contains(trimmed, "<target") && strings.Contains(trimmed, "dev='"+device+"'") {
			inTargetDisk = true
			foundTarget = true
			// 替换 dev 和 bus
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			line = fmt.Sprintf("%s<target dev='%s' bus='%s'/>", indent, newDev, newBus)
		}

		// 如果在目标磁盘块内，修改 address 行（删除旧的地址让 libvirt 自动分配）
		if inTargetDisk && strings.Contains(trimmed, "<address ") {
			// 跳过旧的 address，让 libvirt define 时自动重新分配
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
	if _, err := defineDomainXMLRPC(newXML); err != nil {
		return fmt.Errorf("修改磁盘驱动失败: %w", err)
	}

	return nil
}

// CheckVMSnapshotSafety 检查虚拟机的快照状态是否允许磁盘操作
// 返回: hasExternalSnap(是否有外部快照), snapNames(外部快照名列表), err
func CheckVMSnapshotSafety(vmName string) (bool, []string, error) {
	snapshots, err := ListSnapshots(vmName)
	if err != nil {
		return false, nil, err
	}

	var externalSnaps []string
	for _, snap := range snapshots {
		if snap.State == "disk-snapshot" || snap.Location == "external" {
			externalSnaps = append(externalSnaps, snap.Name)
		}
	}

	return len(externalSnaps) > 0, externalSnaps, nil
}

// ResizeDisk 磁盘扩容
func ResizeDisk(vmName, device string, newSizeGB int) error {
	if err := EnsureVMNotMigrating(vmName, "扩容磁盘"); err != nil {
		return err
	}
	vmState, _ := getDomainStateRPC(vmName)

	// 安全检查：如果存在外部快照，拒绝扩容
	hasExtSnap, extSnapNames, _ := CheckVMSnapshotSafety(vmName)
	if hasExtSnap {
		return fmt.Errorf("虚拟机存在外部快照（%s），扩容后恢复快照可能导致数据不一致。请先删除这些快照后再进行扩容操作",
			strings.Join(extSnapNames, "、"))
	}

	// 获取磁盘路径（从 XML 解析）
	domainXML, xmlErr := getDomainXMLRPC(vmName, 0)
	if xmlErr != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", xmlErr)
	}
	blkList := parseDisksFromDomainXML(domainXML)
	diskPath := ""
	for _, blk := range blkList {
		if blk.Target == device {
			diskPath = blk.Source
			break
		}
	}

	if vmState == "running" {
		newSizeBytes := uint64(newSizeGB) * 1024 * 1024 * 1024
		if err := blockResizeRPC(vmName, device, newSizeBytes, libvirt.DomainBlockResizeBytes); err != nil {
			return fmt.Errorf("热扩容失败: %w", err)
		}
	} else {
		if diskPath == "" {
			return fmt.Errorf("无法获取磁盘路径")
		}
		result := utils.ExecCommand("qemu-img", "resize", diskPath, fmt.Sprintf("%dG", newSizeGB))
		if result.Error != nil {
			return fmt.Errorf("扩容失败: %s", result.Stderr)
		}
	}

	return nil
}

// RemoveDisk 删除磁盘
func RemoveDisk(vmName, device string, deleteFile bool) error {
	if err := EnsureVMNotMigrating(vmName, "删除磁盘"); err != nil {
		return err
	}
	vmState, _ := getDomainStateRPC(vmName)

	// 获取磁盘路径（从 XML 解析）
	domainXML, xmlErr := getDomainXMLRPC(vmName, 0)
	if xmlErr != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", xmlErr)
	}
	blkList := parseDisksFromDomainXML(domainXML)
	diskPath := ""
	for _, blk := range blkList {
		if blk.Target == device {
			diskPath = blk.Source
			break
		}
	}

	// 分离磁盘
	var detachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		// virsh detach-disk --persistent = live + config
		detachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	// 构建 detach 需要的 disk XML
	diskDetXML := fmt.Sprintf("<disk type='file' device='disk'>\n  <target dev='%s'/>\n</disk>", device)
	if err := detachDeviceFlagsRPC(vmName, diskDetXML, detachFlags); err != nil {
		return fmt.Errorf("分离磁盘失败: %w", err)
	}

	// 删除文件
	if deleteFile && diskPath != "" {
		utils.ExecShell(fmt.Sprintf("rm -f %s", utils.ShellSingleQuote(diskPath)))
	}

	return nil
}

// parseQemuInfoGB 从 qemu-img info JSON 解析容量（仅读取顶层字段，避免被 children 中的同名字段干扰）
func parseQemuInfoGB(output, key string) string {
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

// GetVMQcow2Disks 获取虚拟机的所有 qcow2 磁盘（用于删除确认界面）
func GetVMQcow2Disks(vmName string) ([]DiskSimpleInfo, error) {
	disks, err := ListDisks(vmName)
	if err != nil {
		return nil, err
	}

	var result []DiskSimpleInfo
	firstDisk := true // 标记第一个磁盘为系统盘

	for _, disk := range disks {
		// 只返回 qcow2 格式的磁盘（跳过 cdrom 和其他格式）
		if disk.DeviceType == "cdrom" || disk.Path == "" {
			continue
		}
		// 只关注 qcow2 格式
		if disk.Format != "qcow2" {
			continue
		}

		info := DiskSimpleInfo{
			Device:     disk.Device,
			Path:       disk.Path,
			CapacityGB: disk.CapacityGB,
			Format:     disk.Format,
			IsSystem:   firstDisk,
		}

		// 通过 du 获取文件实际占用字节数
		duResult := utils.ExecShell(fmt.Sprintf("du -b %s 2>/dev/null | awk '{print $1}'", utils.ShellSingleQuote(disk.Path)))
		if duResult.Error == nil {
			info.SizeBytes, _ = strconv.ParseInt(strings.TrimSpace(duResult.Stdout), 10, 64)
		}

		result = append(result, info)
		firstDisk = false
	}

	return result, nil
}

// GetDiskFilePath 获取磁盘设备对应的文件路径
func GetDiskFilePath(vmName, device string) string {
	domainXML, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return ""
	}
	blkList := parseDisksFromDomainXML(domainXML)
	for _, blk := range blkList {
		if blk.Target == device {
			return blk.Source
		}
	}
	return ""
}

// TransferDiskFile 将磁盘文件转移到用户存储的虚拟磁盘目录
func TransferDiskFile(diskPath, username string) error {
	diskDir := GetUserDiskDir(username)
	// 确保目标目录存在
	utils.ExecCommand("mkdir", "-p", diskDir)

	filename := filepath.Base(diskPath)
	destPath := filepath.Join(diskDir, filename)

	// 如果目标文件已存在，加上时间戳避免冲突
	checkResult := utils.ExecShell(fmt.Sprintf("test -f %s && echo exists", utils.ShellSingleQuote(destPath)))
	if strings.TrimSpace(checkResult.Stdout) == "exists" {
		ts := time.Now().Format("20060102150405")
		ext := filepath.Ext(filename)
		nameOnly := strings.TrimSuffix(filename, ext)
		destPath = filepath.Join(diskDir, fmt.Sprintf("%s_%s%s", nameOnly, ts, ext))
	}

	// 移动文件
	mvResult := utils.ExecShell(fmt.Sprintf("mv %s %s", utils.ShellSingleQuote(diskPath), utils.ShellSingleQuote(destPath)))
	if mvResult.Error != nil {
		return fmt.Errorf("转移磁盘文件失败: %s", mvResult.Stderr)
	}

	// 设置文件权限
	utils.ExecCommand("chown", "libvirt-qemu:kvm", destPath)

	return nil
}

// ==================== CD/DVD 管理 ====================

// ChangeCDROM 更换/插入 CD/DVD 光盘
// forceNew: 为 true 时强制新增光驱设备，不替换已有的
func ChangeCDROM(vmName, isoPath, device string, forceNew bool) error {
	if err := EnsureVMNotMigrating(vmName, "更换光盘"); err != nil {
		return err
	}
	vmState, _ := getDomainStateRPC(vmName)

	// 强制新增模式：直接添加新光驱设备
	if forceNew {
		return attachNewCDROM(vmName, isoPath, vmState)
	}

	if device == "" {
		// 自动查找 cdrom 设备
		device = findCDROMDevice(vmName)
		if device == "" {
			// 没有现有的 cdrom 设备，添加一个新的
			return attachNewCDROM(vmName, isoPath, vmState)
		}
	}

	// 通过 attachDeviceFlagsRPC 更换 CDROM 介质
	// 构建 CDROM 设备 XML，source 指向新的 ISO
	cdromXML := fmt.Sprintf(
		"<disk type='file' device='cdrom'>\n"+
			"  <driver name='qemu' type='raw'/>\n"+
			"  <source file='%s'/>\n"+
			"  <target dev='%s' bus='sata'/>\n"+
			"</disk>", isoPath, device)

	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, cdromXML, attachFlags); err != nil {
		return fmt.Errorf("插入光盘失败: %w", err)
	}
	return nil
}

// EjectCDROM 弹出 CD/DVD 光盘（保留设备）
func EjectCDROM(vmName, device string) error {
	if err := EnsureVMNotMigrating(vmName, "弹出光盘"); err != nil {
		return err
	}
	vmState, _ := getDomainStateRPC(vmName)

	if device == "" {
		device = findCDROMDevice(vmName)
		if device == "" {
			return fmt.Errorf("未找到 CD/DVD 设备")
		}
	}

	// 通过 attachDeviceFlagsRPC 弹出 CDROM 介质（source 留空）
	cdromXML := fmt.Sprintf(
		"<disk type='file' device='cdrom'>\n"+
			"  <driver name='qemu' type='raw'/>\n"+
			"  <target dev='%s' bus='sata'/>\n"+
			"</disk>", device)

	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, cdromXML, attachFlags); err != nil {
		// 忽略"没有介质"的错误
		if strings.Contains(err.Error(), "doesn't have media") ||
			strings.Contains(err.Error(), "is not removable") {
			return nil
		}
		return fmt.Errorf("弹出光盘失败: %w", err)
	}
	return nil
}

// RemoveCDROM 完全移除 CD/DVD 设备（通过编辑 XML）
func RemoveCDROM(vmName, device string) error {
	if err := EnsureVMNotMigrating(vmName, "移除光驱"); err != nil {
		return err
	}
	vmState, _ := getDomainStateRPC(vmName)

	if device == "" {
		device = findCDROMDevice(vmName)
		if device == "" {
			return fmt.Errorf("未找到 CD/DVD 设备")
		}
	}

	// 运行中的虚拟机不能直接移除 cdrom 设备（virsh 不支持 detach cdrom）
	// 需要通过编辑 XML 来移除
	if vmState == "running" {
		// 先弹出介质
		EjectCDROM(vmName, device)
	}

	// 获取当前 XML
	xmlResult, err := getDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", err)
	}

	// 从 XML 中移除对应的 cdrom disk 节点
	xmlStr := xmlResult
	lines := strings.Split(xmlStr, "\n")
	var newLines []string
	var diskBuffer []string
	inCdromDisk := false
	isCdrom := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检测 cdrom disk 块的开始
		if strings.Contains(trimmed, "<disk ") && strings.Contains(trimmed, "device='cdrom'") {
			inCdromDisk = true
			isCdrom = false
			diskBuffer = []string{line}
			continue
		}

		if inCdromDisk {
			diskBuffer = append(diskBuffer, line)
			// 检查是否包含目标设备名
			if strings.Contains(trimmed, "<target") && strings.Contains(trimmed, "dev='"+device+"'") {
				isCdrom = true
			}
			// 如果到达 </disk>，决定是否保留
			if strings.Contains(trimmed, "</disk>") {
				inCdromDisk = false
				if isCdrom {
					// 这是我们要删除的 cdrom 节点，丢弃整个缓冲区
				} else {
					// 保留这个 disk 节点
					newLines = append(newLines, diskBuffer...)
				}
				diskBuffer = nil
			}
			continue
		}

		newLines = append(newLines, line)
	}

	newXML := strings.Join(newLines, "\n")
	if _, err := defineDomainXMLRPC(newXML); err != nil {
		return fmt.Errorf("移除光驱失败: %w", err)
	}

	return nil
}

// findCDROMDevice 查找虚拟机的第一个 cdrom 设备名
func findCDROMDevice(vmName string) string {
	devices := findAllCDROMDevices(vmName)
	if len(devices) > 0 {
		return devices[0]
	}
	return ""
}

// findAllCDROMDevices 查找虚拟机的所有 cdrom 设备名
func findAllCDROMDevices(vmName string) []string {
	xmlStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return nil
	}

	var devices []string
	lines := strings.Split(xmlStr, "\n")
	inCdrom := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "device='cdrom'") {
			inCdrom = true
		}
		if inCdrom && strings.Contains(trimmed, "<target") {
			parts := strings.Split(trimmed, "dev='")
			if len(parts) > 1 {
				dev := strings.Split(parts[1], "'")[0]
				devices = append(devices, dev)
			}
		}
		if inCdrom && strings.Contains(trimmed, "</disk>") {
			inCdrom = false
		}
	}
	return devices
}

// attachNewCDROM 附加新的 cdrom 设备
func attachNewCDROM(vmName, isoPath, vmState string) error {
	existingDisks, _ := ListDisks(vmName)
	bus := selectNewCDROMBus(vmState, existingDisks)
	if vmState == "running" {
		if err := ensureHotplugCDROMController(vmName); err != nil {
			return err
		}
	}

	usedDevs := make(map[string]bool)
	for _, d := range existingDisks {
		usedDevs[d.Device] = true
	}

	devPrefix := getDevPrefix(bus)
	nextDev := ""
	for _, letter := range "abcdefghijklmnop" {
		dev := devPrefix + string(letter)
		if !usedDevs[dev] {
			nextDev = dev
			break
		}
	}
	if nextDev == "" {
		return fmt.Errorf("没有可用的 %s 光驱设备名", strings.ToUpper(bus))
	}

	// 通过 attachDeviceFlagsRPC 添加 CDROM 设备
	cdromXML := fmt.Sprintf(
		"<disk type='file' device='cdrom'>\n"+
			"  <driver name='qemu' type='raw'/>\n"+
			"  <source file='%s'/>\n"+
			"  <target dev='%s' bus='%s'/>\n"+
			"  <readonly/>\n"+
			"</disk>", isoPath, nextDev, bus)

	var attachFlags uint32 = 2 // VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	if vmState == "running" {
		attachFlags = 3 // VIR_DOMAIN_DEVICE_MODIFY_LIVE | VIR_DOMAIN_DEVICE_MODIFY_CONFIG
	}
	if err := attachDeviceFlagsRPC(vmName, cdromXML, attachFlags); err != nil {
		if vmState == "running" && strings.Contains(err.Error(), "cannot be hotplugged") {
			return fmt.Errorf("当前虚拟机不支持通过 %s 总线热添加光驱，请先关机后再添加", strings.ToUpper(bus))
		}
		return fmt.Errorf("添加光驱失败: %w", err)
	}
	return nil
}

// ensureHotplugCDROMController 确保运行中的虚拟机具备可热添加光驱的控制器。
// SetVMPCIERootPorts 修改已有虚拟机预留的 pcie-root-port 数量（需要关机）。
// targetCount 为期望的 pcie-root-port 总数（不含 pcie-root 本身）。
// 当 targetCount 为 0 时，从 XML 中移除所有已存在的额外 pcie-root-port。
func SetVMPCIERootPorts(vmName string, targetCount int) error {
	if err := EnsureVMNotMigrating(vmName, "修改 PCIe 端口数"); err != nil {
		return err
	}
	state, _ := getDomainStateRPC(vmName)
	if state == "running" {
		return fmt.Errorf("修改 PCIe 端口数量需要先关机")
	}

	xmlResult, err := getDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %w", err)
	}
	if !hasPCIERootController(xmlResult) {
		return fmt.Errorf("当前虚拟机不是 PCIe (q35/virt) 机型，不需要手动管理 pcie-root-port")
	}

	// 解析 XML，识别 system 端口（libvirt 自动生成的）和 extra 端口（我们预留的）
	// system 端口：虚拟机启动时必须的端口（由 libvirt 按设备数量自动生成）
	// extra 端口：我们在创建虚拟机时注入 XML 中额外预留的空端口

	lines := strings.Split(xmlResult, "\n")

	// 先找到所有 pcie-root-port 控制器块
	type portBlock struct {
		index     int
		startLine int
		endLine   int
		isExtra   bool // true 表示是额外预留的空端口（无下游设备）
	}
	var portBlocks []portBlock

	inPort := false
	currentPort := portBlock{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, `<controller type='pci'`) && strings.Contains(trimmed, `model='pcie-root-port'`) {
			inPort = true
			currentPort = portBlock{startLine: i}
			// 提取 index 属性
			if idxMatch := regexp.MustCompile(`index='(\d+)'`).FindStringSubmatch(trimmed); len(idxMatch) > 1 {
				currentPort.index, _ = strconv.Atoi(idxMatch[1])
			}
			continue
		}

		if inPort {
			if strings.Contains(trimmed, "</controller>") {
				currentPort.endLine = i
				// 判断是否为额外预留的端口：target chassis 值大于系统端口常见范围
				// 系统自动生成的端口 chassis 在 1-7 左右，额外预留的在较高值
				portBlocks = append(portBlocks, currentPort)
				inPort = false
				currentPort = portBlock{}
			}
		}
	}

	if len(portBlocks) == 0 {
		return fmt.Errorf("未找到 pcie-root-port 控制器")
	}

	currentCount := len(portBlocks)
	if currentCount == targetCount {
		return nil // 无需修改
	}

	if targetCount > currentCount {
		// 需要增加端口：在最后一个 pcie-root-port 之后追加新端口
		lastPortBlock := portBlocks[len(portBlocks)-1]
		insertPos := lastPortBlock.endLine + 1

		nextIndex := lastPortBlock.index + 1
		nextChassis := lastPortBlock.index + 1
		// 从最后的 <address> 推算下一个 slot 和 function
		lastAddrLines := lines[lastPortBlock.startLine:lastPortBlock.endLine]
		nextSlot := 2   // pcie-root 上的默认 slot
		nextFn := 0      // function 编号

		for _, l := range lastAddrLines {
			if strings.Contains(l, "slot=") {
				if slotMatch := regexp.MustCompile(`slot='0x([0-9a-f]+)'`).FindStringSubmatch(l); len(slotMatch) > 1 {
					s, _ := strconv.ParseInt(slotMatch[1], 16, 64)
					nextSlot = int(s)
				}
			}
			if strings.Contains(l, "function=") {
				if fnMatch := regexp.MustCompile(`function='0x([0-9a-f]+)'`).FindStringSubmatch(l); len(fnMatch) > 1 {
					f, _ := strconv.ParseInt(fnMatch[1], 16, 64)
					nextFn = int(f) + 1
				}
			}
		}
		// function 超过 7 时换到下一个 slot，function 归零
		if nextFn > 7 {
			nextSlot++
			nextFn = 0
		}

		// 生成新的空端口 XML
		var newPorts []string
		currentSlot := nextSlot
		currentFn := nextFn
		for i := 0; i < targetCount-currentCount; i++ {
			idx := nextIndex + i
			chs := nextChassis + i

			newPort := fmt.Sprintf(
				`    <controller type='pci' index='%d' model='pcie-root-port'>
      <model name='pcie-root-port'/>
      <target chassis='%d' port='0x%x'/>
      <address type='pci' domain='0x0000' bus='0x00' slot='0x%02x' function='0x%x'/>
    </controller>`,
				idx, chs, 0x10+idx, currentSlot, currentFn)

			newPorts = append(newPorts, newPort)

			// 下一个 function，超过 7 换 slot
			currentFn++
			if currentFn > 7 {
				currentSlot++
				currentFn = 0
			}
		}

		newLines := make([]string, 0, len(lines)+len(newPorts))
		newLines = append(newLines, lines[:insertPos]...)
		newLines = append(newLines, newPorts...)
		newLines = append(newLines, lines[insertPos:]...)
		lines = newLines

	} else {
		// 需要减少端口：从后往前删除多余的空端口
		// 只删除 index 较大的端口（这些是我们预留的）
		removeCount := currentCount - targetCount
		removed := 0
		for i := len(portBlocks) - 1; i >= 0 && removed < removeCount; i-- {
			block := portBlocks[i]
			// 跳过系统关键端口（index 0-6 通常是系统自动生成的）
			if block.index <= 6 && removed >= removeCount {
				break
			}
			// 标记要删除的行
			for j := block.startLine; j <= block.endLine; j++ {
				lines[j] = "" // 占位删除
			}
			removed++
		}
		if removed < removeCount {
			return fmt.Errorf("无法删除足够的端口：请求减少 %d 个，但只能安全删除 %d 个（index>6 的空端口）。请先关机后通过 virsh edit 手动调整", removeCount, removed)
		}
	}

	newXML := strings.Join(lines, "\n")
	// 压缩连续空行
	emptyLineRe := regexp.MustCompile(`\n\s*\n\s*\n`)
	newXML = emptyLineRe.ReplaceAllString(newXML, "\n\n")

	if _, err := defineDomainXMLRPC(newXML); err != nil {
		return fmt.Errorf("修改 PCIe 端口数量失败: %w", err)
	}

	return nil
}

// GetVMPCIERootPorts 获取虚拟机当前的 pcie-root-port 数量
func GetVMPCIERootPorts(vmName string) (int, error) {
	xmlStr, err := getDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return 0, fmt.Errorf("获取虚拟机 XML 失败: %w", err)
	}
	if !hasPCIERootController(xmlStr) {
		return 0, nil // 非 PCIe 机型，返回 0
	}

	count := 0
	for _, line := range strings.Split(xmlStr, "\n") {
		if strings.Contains(line, `<controller type='pci'`) && strings.Contains(line, `model='pcie-root-port'`) {
			count++
		}
	}
	return count, nil
}

func ensureHotplugCDROMController(vmName string) error {
	liveXMLStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return fmt.Errorf("获取运行中虚拟机 XML 失败: %w", err)
	}
	configXMLStr, err := getDomainXMLRPC(vmName, libvirt.DomainXMLInactive)
	if err != nil {
		return fmt.Errorf("获取持久化虚拟机 XML 失败: %w", err)
	}

	hasLiveController := hasSCSIController(liveXMLStr)
	hasConfigController := hasSCSIController(configXMLStr)
	if hasLiveController && hasConfigController {
		return nil
	}

	controllerXML, buildErr := buildHotplugSCSIControllerXML(vmName, liveXMLStr)
	if buildErr != nil {
		return buildErr
	}

	if !hasLiveController {
		if err := attachDeviceFlagsRPC(vmName, controllerXML, 1); err != nil { // 1=VIR_DOMAIN_DEVICE_MODIFY_LIVE
			if !strings.Contains(err.Error(), "Duplicate ID") && !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("为热添加光驱准备 SCSI 控制器失败: %w", err)
			}
		}
	}

	if !hasConfigController {
		if err := attachDeviceFlagsRPC(vmName, controllerXML, 2); err != nil { // 2=VIR_DOMAIN_DEVICE_MODIFY_CONFIG
			if !strings.Contains(err.Error(), "Duplicate ID") && !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("为持久化配置写入 SCSI 控制器失败: %w", err)
			}
		}
	}

	return nil
}

func hasSCSIController(xmlContent string) bool {
	return strings.Contains(xmlContent, "<controller type='scsi'") ||
		strings.Contains(xmlContent, `<controller type="scsi"`)
}

func buildHotplugSCSIControllerXML(vmName, xmlContent string) (string, error) {
	if !hasPCIERootController(xmlContent) {
		return "<controller type='scsi' model='virtio-scsi'/>", nil
	}

	freeBus, err := findFreePCIERootPortBus(vmName)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"<controller type='scsi' model='virtio-scsi'><address type='pci' domain='0x0000' bus='0x%02x' slot='0x00' function='0x0'/></controller>",
		freeBus,
	), nil
}

func hasPCIERootController(xmlContent string) bool {
	return strings.Contains(xmlContent, "model='pcie-root'") ||
		strings.Contains(xmlContent, `model="pcie-root"`)
}

func findFreePCIERootPortBus(vmName string) (int, error) {
	infoPCIResult, err := qemuMonitorCommandRPC(vmName, "info pci", domainQemuMonitorCommandHmp)
	if err != nil {
		return 0, fmt.Errorf("获取运行中虚拟机 PCI 拓扑失败: %w", err)
	}

	freeBuses := parseFreePCIERootPortBuses(infoPCIResult)
	if len(freeBuses) == 0 {
		return 0, fmt.Errorf("当前虚拟机没有空闲的 pcie-root-port 热插槽，请先关机后再添加光驱")
	}

	return freeBuses[len(freeBuses)-1], nil
}

func parseFreePCIERootPortBuses(infoPCI string) []int {
	rootPortBusRegex := regexp.MustCompile(`secondary bus (\d+)`)
	busHeaderRegex := regexp.MustCompile(`Bus\s+(\d+),\s+device\s+\d+,\s+function\s+\d+:`)

	available := make(map[int]bool)
	used := make(map[int]bool)

	lines := strings.Split(infoPCI, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if strings.Contains(line, "PCI bridge: PCI device 1b36:000c") {
			continue
		}

		if match := rootPortBusRegex.FindStringSubmatch(line); len(match) >= 2 {
			bus, err := strconv.Atoi(match[1])
			if err == nil && bus > 0 {
				available[bus] = true
			}
			continue
		}

		if match := busHeaderRegex.FindStringSubmatch(line); len(match) >= 2 {
			bus, err := strconv.Atoi(match[1])
			if err == nil && bus > 0 {
				used[bus] = true
			}
		}
	}

	for bus := range used {
		delete(available, bus)
	}

	if len(available) == 0 {
		return nil
	}

	result := make([]int, 0, len(available))
	for bus := range available {
		result = append(result, bus)
	}
	sort.Ints(result)
	return result
}

func selectNewCDROMBus(vmState string, existingDisks []DiskInfo) string {
	if vmState == "running" {
		return "scsi"
	}
	for _, disk := range existingDisks {
		if disk.DeviceType == "cdrom" && strings.TrimSpace(disk.Bus) != "" {
			return strings.TrimSpace(disk.Bus)
		}
	}
	return "sata"
}

// ==================== 磁盘 IOPS 限制 ====================

// DiskIOPSTune 磁盘 IOPS 限制配置
type DiskIOPSTune struct {
	TotalIopsSec int `json:"total_iops_sec"` // 总 IOPS 限制（0 表示不限制）
	ReadIopsSec  int `json:"read_iops_sec"`  // 读 IOPS 限制（0 表示不限制）
	WriteIopsSec int `json:"write_iops_sec"` // 写 IOPS 限制（0 表示不限制）
}

// SetDiskIOPSTune 设置虚拟机磁盘的 IOPS 限制（实时生效并持久化）
// vmName: 虚拟机名称, dev: 磁盘设备名（如 vda）
// iops: IOPS 限制配置，为 nil 时清除限制
func SetDiskIOPSTune(vmName, dev string, iops *DiskIOPSTune) error {
	if err := EnsureVMNotMigrating(vmName, "设置磁盘IOPS限制"); err != nil {
		return err
	}

	vmState, _ := getDomainStateRPC(vmName)
	totalIops := 0
	readIops := 0
	writeIops := 0
	if iops != nil {
		totalIops = iops.TotalIopsSec
		readIops = iops.ReadIopsSec
		writeIops = iops.WriteIopsSec
	}

	// 构建 TypedParam 列表
	// libvirt 不允许 total_iops_sec 与 read/write_iops_sec 同时设置，二者互斥
	var params []libvirt.TypedParam
	if totalIops > 0 {
		if readIops > 0 || writeIops > 0 {
			return fmt.Errorf("总 IOPS 与读/写 IOPS 不能同时设置，请只设置其中一种")
		}
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneTotalIopsSec,
			Value: *libvirt.NewTypedParamValueInt(int32(totalIops)),
		})
	} else {
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneReadIopsSec,
			Value: *libvirt.NewTypedParamValueInt(int32(readIops)),
		})
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneWriteIopsSec,
			Value: *libvirt.NewTypedParamValueInt(int32(writeIops)),
		})
	}

	var tuneFlags uint32 = 2 // VIR_DOMAIN_AFFECT_CONFIG
	if vmState == "running" {
		tuneFlags = 3 // VIR_DOMAIN_AFFECT_LIVE | VIR_DOMAIN_AFFECT_CONFIG
	}
	if err := setBlkIOParametersRPC(vmName, dev, params, tuneFlags); err != nil {
		return fmt.Errorf("设置磁盘 IOPS 限制失败: %w", err)
	}

	return nil
}

// GetDiskIOPSTune 从 libvirt 获取指定磁盘的 IOPS 设置
func GetDiskIOPSTune(vmName, dev string) (*DiskIOPSTune, error) {
	params, err := getBlkIOParametersRPC(vmName, dev, 0)
	if err != nil {
		return nil, fmt.Errorf("获取磁盘 IOPS 信息失败: %w", err)
	}

	iops := &DiskIOPSTune{}
	for _, p := range params {
		switch p.Field {
		case libvirt.DomainBlockIotuneTotalIopsSec:
			if v, ok := p.Value.I.(int32); ok {
				iops.TotalIopsSec = int(v)
			}
		case libvirt.DomainBlockIotuneReadIopsSec:
			if v, ok := p.Value.I.(int32); ok {
				iops.ReadIopsSec = int(v)
			}
		case libvirt.DomainBlockIotuneWriteIopsSec:
			if v, ok := p.Value.I.(int32); ok {
				iops.WriteIopsSec = int(v)
			}
		}
	}

	return iops, nil
}

// ParseAllDiskIOPSTune 解析虚拟机 XML 中所有磁盘的 IOPS 配置
func ParseAllDiskIOPSTune(vmName string) map[string]*DiskIOPSTune {
	result := make(map[string]*DiskIOPSTune)

	xmlStr, err := getDomainXMLRPC(vmName, 0)
	if err != nil {
		return result
	}

	lines := strings.Split(xmlStr, "\n")
	var currentDev string
	inDisk := false
	inIOTune := false
	var currentIOPS *DiskIOPSTune

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "<disk ") {
			inDisk = true
			inIOTune = false
			currentDev = ""
			currentIOPS = nil
		}

		if inDisk {
			if strings.Contains(trimmed, "<target") && strings.Contains(trimmed, "dev='") {
				parts := strings.Split(trimmed, "dev='")
				if len(parts) > 1 {
					currentDev = strings.Split(parts[1], "'")[0]
				}
			}

			if strings.HasPrefix(trimmed, "<iotune>") {
				inIOTune = true
				currentIOPS = &DiskIOPSTune{}
			}
			if inIOTune && currentIOPS != nil {
				if strings.Contains(trimmed, "total_iops_sec") {
					currentIOPS.TotalIopsSec = parseIOPSElement(trimmed, "total_iops_sec")
				}
				if strings.Contains(trimmed, "read_iops_sec") {
					currentIOPS.ReadIopsSec = parseIOPSElement(trimmed, "read_iops_sec")
				}
				if strings.Contains(trimmed, "write_iops_sec") {
					currentIOPS.WriteIopsSec = parseIOPSElement(trimmed, "write_iops_sec")
				}
			}
			if strings.HasPrefix(trimmed, "</iotune>") {
				inIOTune = false
			}

			if strings.Contains(trimmed, "</disk>") {
				if currentDev != "" && currentIOPS != nil {
					if currentIOPS.TotalIopsSec > 0 || currentIOPS.ReadIopsSec > 0 || currentIOPS.WriteIopsSec > 0 {
						result[currentDev] = currentIOPS
					}
				}
				inDisk = false
				inIOTune = false
			}
		}
	}

	return result
}

// parseIOPSElement 从 iotune 行中解析数值
func parseIOPSElement(line, elementName string) int {
	parts := strings.Split(line, elementName)
	if len(parts) > 1 {
		rest := parts[1]
		rest = strings.TrimSpace(rest)
		rest = strings.TrimPrefix(rest, ">")
		if idx := strings.Index(rest, "<"); idx >= 0 {
			rest = rest[:idx]
		}
		if val, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
			return val
		}
	}
	return 0
}
