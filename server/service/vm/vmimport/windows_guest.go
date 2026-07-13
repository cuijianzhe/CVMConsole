package vmimport

import (
	"fmt"
	"os"
	"strings"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/service"
	"kvm_console/service/arch"
	vm_memory "kvm_console/service/vm/memory"
	"kvm_console/service/vm_xml"
	"kvm_console/utils"
)

// importVMWindowsDefine handles Windows VM XML construction and define for ImportVM
// 返回值：(error, bool) bool 表示是否创建了 Config Drive 需要在启动后弹出
func importVMWindowsDefine(params *ImportVMParams, destDiskPath, format string, ramMB int, memoryMeta *vm_memory.VMMemoryMetadata, srcDiskPath string, needUEFI bool) (error, bool) {
	// 获取宿主机架构 Profile，参数化 arch/machine/emulator/watchdog
	hostArch := arch.DetectHostArch()
	profile := arch.GetProfile(hostArch)
	archName := profile.Arch()
	machineType := profile.DefaultMachineType()
	emulatorPath := profile.EmulatorPath()
	watchdogModel := profile.DefaultWatchdogModel()
	isX8664 := archName == arch.ArchX8664

	// Hyper-V enlightenments 仅在 x86_64 架构上支持
	var hyperVBlock string
	if isX8664 {
		hyperVBlock = "    <hyperv mode='custom'>\n      <relaxed state='on'/><vapic state='on'/><spinlocks state='on' retries='8191'/>\n    </hyperv>\n    <timer name='pit' tickpolicy='delay'/>\n    <timer name='hpet' present='no'/><timer name='hypervclock' present='yes'/>\n    "
	}

	// 网络接口 XML：仅在有主网口交换机配置时才添加
	var networkXML string
	if params.SwitchID != 0 {
		macResult := utils.ExecShell(`printf '52:54:00:%02x:%02x:%02x' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))`)
		macAddr := strings.TrimSpace(macResult.Stdout)
		if macAddr == "" {
			macAddr = "52:54:00:aa:bb:cc"
		}
		networkXML = service.BuildOVSInterfaceXML(macAddr, params.NicModel) + "\n"
	}

	// Generate qcow2 NVRAM
	nvramClone := fmt.Sprintf("/var/lib/libvirt/qemu/nvram/%s_VARS.fd", params.Name)
	if err := vm_xml.CreateQCOW2NVRAMFromTemplate("/usr/share/OVMF/OVMF_VARS_4M.ms.fd", nvramClone); err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}

	ramKiB := ramMB * 1024

	rtcOffset := service.ResolveRTCOffset(params.RTCOffset, "windows")
	rtcStartDate := service.NormalizeRTCStartDate(params.RTCStartDate)
	clockOpenTag := fmt.Sprintf("<clock offset='%s'>", rtcOffset)
	if rtcStartDate != service.VMRTCStartDateNow {
		epoch, err := service.ParseRTCStartDateToEpoch(rtcStartDate)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
		rtcOffset = service.VMRTCOffsetAbsolute
		clockOpenTag = fmt.Sprintf("<clock offset='%s' start='%s'>", rtcOffset, epoch)
	}

	// 使用显式 loader/nvram，不使用 firmware='efi' 自动选择，
	// 避免 libvirt 自动填充 nvram format='raw' 与 qcow2 格式不匹配导致黑屏。
	loaderPath := vm_xml.ResolveOVMFLoaderPath(true)
	varsTemplate := vm_xml.ResolveOVMFVarsTemplatePath(true)

	// CloudbaseInit 初始化：仅在 init_type=windows 且有密码时执行
	// 导入磁盘场景跳过 virt-customize 注入（磁盘通常已安装 cloudbase-init，且 virt-customize 对非模板磁盘检测极慢）
	// 直接使用 Config Drive 提供元数据（hostname、密码等）
	var isoPath string
	var isoErr error
	if params.InitType == "windows" && params.Password != "" {
		isoPath, isoErr = service.CreateWindowsConfigDriveISO(params.Name, params.Hostname, params.Password)
		if isoErr != nil {
			logger.App.Warn("创建 Windows Config Drive ISO 失败，CloudbaseInit 将无法自动注入密码",
				"vm", params.Name, "error", isoErr)
		}
	}

	vmXML := fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='KiB'>%d</memory>
%s
  <os>
    <type arch='%s' machine='%s'>hvm</type>
    <loader readonly='yes' secure='yes' type='pflash'>%s</loader>
    <nvram template='%s' templateFormat='raw' format='qcow2'>%s</nvram>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/><apic/>
    <vmport state='off'/><smm state='on'/>
  </features>
  <cpu mode='host-passthrough' check='none' migratable='on'/>
  %s
    <timer name='rtc' tickpolicy='catchup'/>%s
  </clock>
  <on_poweroff>destroy</on_poweroff><on_reboot>restart</on_reboot><on_crash>destroy</on_crash>
  <pm><suspend-to-mem enabled='no'/><suspend-to-disk enabled='no'/></pm>
  <devices>
    <emulator>%s</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='%s' discard='unmap' detect_zeroes='unmap'/>
      <source file='%s'/><target dev='vda' bus='virtio'/>
    </disk>
    <controller type='usb' index='0' model='qemu-xhci' ports='15'/>
    <controller type='virtio-serial' index='0'/>
%s
    <input type='tablet' bus='usb'/>
    <tpm model='tpm-crb'><backend type='emulator' version='2.0'/></tpm>
    <graphics type='vnc' port='-1' autoport='yes' listen='0.0.0.0'>
      <listen type='address' address='0.0.0.0'/>
    </graphics>
    <video><model type='virtio' heads='1' primary='yes'/></video>
    <watchdog model='%s' action='reset'/>
    <memballoon model='virtio' freePageReporting='on'><stats period='5'/></memballoon>
  </devices>
</domain>`,
		params.Name, ramKiB, service.BuildVCPUTag(params.VCPU, params.MaxVCPU), archName, machineType, loaderPath, varsTemplate, nvramClone, clockOpenTag, hyperVBlock, emulatorPath, format, destDiskPath, networkXML, watchdogModel)

	var err error
	if memoryMeta != nil {
		vmXML, err = vm_memory.ApplyMemoryMetadataToDomainXML(vmXML, memoryMeta, false)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	vmXML, err = vm_xml.ApplyVMGuestAgentConfigToDomainXML(vmXML, params.GuestAgent)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = vm_xml.ApplySMBIOS1ConfigToDomainXML(vmXML, params.SMBIOS1, true)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = service.ApplyVMAPICToDomainXML(vmXML, params.APIC)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = vm_xml.ApplyVMPAEToDomainXML(vmXML, params.PAE)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML = vm_xml.ApplyVMVideoModelToDomainXML(vmXML, params.VideoModel, "windows")
	vmXML = vm_xml.ApplyWindowsGuestOptimizationsToDomainXML(vmXML)
	// 隐藏 KVM 标志
	if params.KVMHidden != nil {
		vmXML, err = vm_xml.ApplyKVMHiddenToDomainXML(vmXML, params.KVMHidden)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	// Hyper-V vendor_id 伪装
	if params.VendorID != "" {
		vmXML, err = vm_xml.ApplyVendorIDToHyperVBlock(vmXML, params.VendorID)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	topoVCPU := service.EffectiveTopologyVCPU(params.VCPU, params.MaxVCPU)
	vmXML = service.ApplyCPUTopologyModeToDomainXML(vmXML, params.CPUTopologyMode, "windows", topoVCPU)
	vmXML = service.ApplyVMCPULimitToDomainXML(vmXML, params.VCPU, params.CPULimitPercent)
	if params.CPUAffinity != "" {
		affinityCores, affErr := service.ParseCPUAffinity(params.CPUAffinity)
		if affErr != nil {
			_ = os.Remove(destDiskPath)
			return fmt.Errorf("CPU 亲和性格式错误: %w", affErr), false
		}
		if len(affinityCores) > 0 {
			if affErr := service.ValidateCPUAffinity(affinityCores); affErr != nil {
				_ = os.Remove(destDiskPath)
				return affErr, false
			}
		}
		vmXML = service.ApplyCPUAffinityToDomainXML(vmXML, topoVCPU, affinityCores)
	}
	vmXML, err = service.ApplyVPCSwitchToDomainXML(vmXML, params.SwitchID)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}

	// SPICE graphics（默认本地监听），与 VNC 共存；是否启用由 per-VM 开关决定，回退全局默认
	spiceEnabled := config.GlobalConfig.SpiceEnabledByDefault
	if params.SpiceEnabled != nil {
		spiceEnabled = *params.SpiceEnabled
	}
	if spiceEnabled {
		vmXML = service.InjectSPICEGraphicsToDomainXML(vmXML, "", "127.0.0.1")
		vmXML = service.EnsureQXLVideo(vmXML)
	}

	// 将 Config Drive ISO 挂载为 CD-ROM，供 CloudbaseInit 首次启动时读取
	if params.InitType == "windows" && isoPath != "" {
		vmXML = service.AddConfigDriveCDROMToXML(vmXML, isoPath, "virtio")
	}

	xmlPath := fmt.Sprintf("/tmp/_vm-import-%s.xml", params.Name)
	if err := os.WriteFile(xmlPath, []byte(vmXML), 0644); err != nil {
		_ = os.Remove(destDiskPath)
		return fmt.Errorf("写入虚拟机 XML 失败: %v", err), false
	}
	defer os.Remove(xmlPath)

	defineResult := utils.ExecCommand("virsh", "define", xmlPath)
	_ = os.Remove(xmlPath)
	if defineResult.Error != nil {
		_ = os.Remove(destDiskPath)
		return fmt.Errorf("定义虚拟机失败: %s", defineResult.Stderr), false
	}

	err = importVMPostDefine(params.Name, srcDiskPath, destDiskPath, params.CopyDisk, memoryMeta, params.Remark, params.Freeze, params.StartAfterImport)
	return err, isoPath != ""
}

// importDiskByPathWindowsDefine handles Windows VM XML construction and define for ImportDiskByPath
// 返回值：(error, bool) bool 表示是否创建了 Config Drive 需要在启动后弹出
func importDiskByPathWindowsDefine(params *ImportDiskByPathParams, destDiskPath, format string, ramMB int, memoryMeta *vm_memory.VMMemoryMetadata, mainDiskSrc string) (error, bool) {
	// 获取宿主机架构 Profile，参数化 arch/machine/emulator/watchdog
	hostArch := arch.DetectHostArch()
	profile := arch.GetProfile(hostArch)
	archName := profile.Arch()
	machineType := profile.DefaultMachineType()
	emulatorPath := profile.EmulatorPath()
	watchdogModel := profile.DefaultWatchdogModel()
	isX8664 := archName == arch.ArchX8664

	// Hyper-V enlightenments 仅在 x86_64 架构上支持
	var hyperVBlock string
	if isX8664 {
		hyperVBlock = "    <hyperv mode='custom'>\n      <relaxed state='on'/><vapic state='on'/><spinlocks state='on' retries='8191'/>\n    </hyperv>\n    <timer name='pit' tickpolicy='delay'/>\n    <timer name='hpet' present='no'/><timer name='hypervclock' present='yes'/>\n    "
	}

	// 网络接口 XML：仅在有主网口交换机配置时才添加
	var networkXML string
	if params.SwitchID != 0 {
		macResult := utils.ExecShell(`printf '52:54:00:%02x:%02x:%02x' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))`)
		macAddr := strings.TrimSpace(macResult.Stdout)
		if macAddr == "" {
			macAddr = "52:54:00:aa:bb:cc"
		}
		networkXML = service.BuildOVSInterfaceXML(macAddr, params.NicModel) + "\n"
	}

	nvramClone := fmt.Sprintf("/var/lib/libvirt/qemu/nvram/%s_VARS.fd", params.Name)
	if err := vm_xml.CreateQCOW2NVRAMFromTemplate("/usr/share/OVMF/OVMF_VARS_4M.ms.fd", nvramClone); err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}

	ramKiB := ramMB * 1024

	rtcOffset := service.ResolveRTCOffset(params.RTCOffset, "windows")
	rtcStartDate := service.NormalizeRTCStartDate(params.RTCStartDate)
	clockOpenTag := fmt.Sprintf("<clock offset='%s'>", rtcOffset)
	if rtcStartDate != service.VMRTCStartDateNow {
		epoch, err := service.ParseRTCStartDateToEpoch(rtcStartDate)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
		rtcOffset = service.VMRTCOffsetAbsolute
		clockOpenTag = fmt.Sprintf("<clock offset='%s' start='%s'>", rtcOffset, epoch)
	}

	// 使用显式 loader/nvram，不使用 firmware='efi' 自动选择
	loaderPath2 := vm_xml.ResolveOVMFLoaderPath(true)
	varsTemplate2 := vm_xml.ResolveOVMFVarsTemplatePath(true)

	// CloudbaseInit 初始化：仅在 init_type=windows 且有密码时执行
	// 导入磁盘场景跳过 virt-customize 注入（磁盘通常已安装 cloudbase-init，且 virt-customize 对非模板磁盘检测极慢）
	// 直接使用 Config Drive 提供元数据（hostname、密码等）
	var isoPath string
	var isoErr error
	if params.InitType == "windows" && params.Password != "" {
		isoPath, isoErr = service.CreateWindowsConfigDriveISO(params.Name, params.Hostname, params.Password)
		if isoErr != nil {
			logger.App.Warn("创建 Windows Config Drive ISO 失败，CloudbaseInit 将无法自动注入密码",
				"vm", params.Name, "error", isoErr)
		}
	}

	vmXML := fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='KiB'>%d</memory>
%s
  <os>
    <type arch='%s' machine='%s'>hvm</type>
    <loader readonly='yes' secure='yes' type='pflash'>%s</loader>
    <nvram template='%s' templateFormat='raw' format='qcow2'>%s</nvram>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/><apic/>
    <vmport state='off'/><smm state='on'/>
  </features>
  <cpu mode='host-passthrough' check='none' migratable='on'/>
  %s
    <timer name='rtc' tickpolicy='catchup'/>%s
  </clock>
  <on_poweroff>destroy</on_poweroff><on_reboot>restart</on_reboot><on_crash>destroy</on_crash>
  <pm><suspend-to-mem enabled='no'/><suspend-to-disk enabled='no'/></pm>
  <devices>
    <emulator>%s</emulator>
    <disk type='file' device='disk'>
      <driver name='qemu' type='%s' discard='unmap' detect_zeroes='unmap'/>
      <source file='%s'/><target dev='vda' bus='virtio'/>
    </disk>
    <controller type='usb' index='0' model='qemu-xhci' ports='15'/>
    <controller type='virtio-serial' index='0'/>
%s
    <input type='tablet' bus='usb'/>
    <tpm model='tpm-crb'><backend type='emulator' version='2.0'/></tpm>
    <graphics type='vnc' port='-1' autoport='yes' listen='0.0.0.0'>
      <listen type='address' address='0.0.0.0'/>
    </graphics>
    <video><model type='virtio' heads='1' primary='yes'/></video>
    <watchdog model='%s' action='reset'/>
    <memballoon model='virtio' freePageReporting='on'><stats period='5'/></memballoon>
  </devices>
</domain>`,
		params.Name, ramKiB, service.BuildVCPUTag(params.VCPU, params.MaxVCPU), archName, machineType, loaderPath2, varsTemplate2, nvramClone, clockOpenTag, hyperVBlock, emulatorPath, format, destDiskPath, networkXML, watchdogModel)

	var err error
	if memoryMeta != nil {
		vmXML, err = vm_memory.ApplyMemoryMetadataToDomainXML(vmXML, memoryMeta, false)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	vmXML, err = vm_xml.ApplyVMGuestAgentConfigToDomainXML(vmXML, params.GuestAgent)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = vm_xml.ApplySMBIOS1ConfigToDomainXML(vmXML, params.SMBIOS1, true)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = service.ApplyVMAPICToDomainXML(vmXML, params.APIC)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML, err = vm_xml.ApplyVMPAEToDomainXML(vmXML, params.PAE)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}
	vmXML = vm_xml.ApplyVMVideoModelToDomainXML(vmXML, params.VideoModel, "windows")
	vmXML = vm_xml.ApplyWindowsGuestOptimizationsToDomainXML(vmXML)
	// 隐藏 KVM 标志
	if params.KVMHidden != nil {
		vmXML, err = vm_xml.ApplyKVMHiddenToDomainXML(vmXML, params.KVMHidden)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	// Hyper-V vendor_id 伪装
	if params.VendorID != "" {
		vmXML, err = vm_xml.ApplyVendorIDToHyperVBlock(vmXML, params.VendorID)
		if err != nil {
			_ = os.Remove(destDiskPath)
			return err, false
		}
	}
	topoVCPU := service.EffectiveTopologyVCPU(params.VCPU, params.MaxVCPU)
	vmXML = service.ApplyCPUTopologyModeToDomainXML(vmXML, params.CPUTopologyMode, "windows", topoVCPU)
	vmXML = service.ApplyVMCPULimitToDomainXML(vmXML, params.VCPU, params.CPULimitPercent)
	if params.CPUAffinity != "" {
		var affErr error
		vmXML, affErr = service.ApplyCPUAffinityIfSet(vmXML, topoVCPU, params.CPUAffinity)
		if affErr != nil {
			_ = os.Remove(destDiskPath)
			return affErr, false
		}
	}
	vmXML, err = service.ApplyVPCSwitchToDomainXML(vmXML, params.SwitchID)
	if err != nil {
		_ = os.Remove(destDiskPath)
		return err, false
	}

	// SPICE graphics（默认本地监听），与 VNC 共存；是否启用由 per-VM 开关决定，回退全局默认
	spiceEnabled := config.GlobalConfig.SpiceEnabledByDefault
	if params.SpiceEnabled != nil {
		spiceEnabled = *params.SpiceEnabled
	}
	if spiceEnabled {
		vmXML = service.InjectSPICEGraphicsToDomainXML(vmXML, "", "127.0.0.1")
		vmXML = service.EnsureQXLVideo(vmXML)
	}

	// 将 Config Drive ISO 挂载为 CD-ROM，供 CloudbaseInit 首次启动时读取
	if params.InitType == "windows" && isoPath != "" {
		vmXML = service.AddConfigDriveCDROMToXML(vmXML, isoPath, "virtio")
	}

	xmlPath := fmt.Sprintf("/tmp/_vm-importd-%s.xml", params.Name)
	if err := os.WriteFile(xmlPath, []byte(vmXML), 0644); err != nil {
		_ = os.Remove(destDiskPath)
		return fmt.Errorf("写入虚拟机 XML 失败: %v", err), false
	}
	defer os.Remove(xmlPath)

	defineResult := utils.ExecCommand("virsh", "define", xmlPath)
	_ = os.Remove(xmlPath)
	if defineResult.Error != nil {
		_ = os.Remove(destDiskPath)
		return fmt.Errorf("定义虚拟机失败: %s", defineResult.Stderr), false
	}

	err = importVMPostDefine(params.Name, mainDiskSrc, destDiskPath, params.CopyDisk, memoryMeta, params.Remark, params.Freeze, params.StartAfterImport)
	return err, isoPath != ""
}
