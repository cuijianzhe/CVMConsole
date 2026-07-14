package clone

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/service/arch"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/service/vm/memory"
	"kvm_console/service/vm_xml"
	"kvm_console/utils"
)

// buildWindowsCloudbaseInitConf 返回为 CloudbaseInit 主服务配置的 cloudbase-init.conf 内容。
// 配置 ConfigDriveService 作为元数据源，并启用完整的初始化插件列表，
// 覆盖模板中可能缺失 metadata_services/plugins 的默认配置。
func buildWindowsCloudbaseInitConf() string {
	return `[DEFAULT]
username=Administrator
groups=Administrators
inject_user_password=false
config_drive_raw_hhd=true
config_drive_cdrom=true
config_drive_vfat=true
bsdtar_path=C:\Program Files\Cloudbase Solutions\Cloudbase-Init\bin\bsdtar.exe
mtools_path=C:\Program Files\Cloudbase Solutions\Cloudbase-Init\bin\
verbose=true
debug=true
log_dir=C:\Program Files\Cloudbase Solutions\Cloudbase-Init\log\
log_file=cloudbase-init.log
default_log_levels=comtypes=INFO,suds=INFO,iso8601=WARN,requests=WARN
logging_serial_port_settings=COM1,115200,N,8
mtu_use_dhcp_config=true
ntp_use_dhcp_config=true
local_scripts_path=C:\Program Files\Cloudbase Solutions\Cloudbase-Init\LocalScripts\
metadata_services=cloudbaseinit.metadata.services.configdrive.ConfigDriveService,cloudbaseinit.metadata.services.base.EmptyMetadataService
plugins=cloudbaseinit.plugins.common.mtu.MTUPlugin,cloudbaseinit.plugins.windows.ntpclient.NTPClientPlugin,cloudbaseinit.plugins.common.sethostname.SetHostNamePlugin,cloudbaseinit.plugins.windows.createuser.CreateUserPlugin,cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin,cloudbaseinit.plugins.windows.extendvolumes.ExtendVolumesPlugin,cloudbaseinit.plugins.common.userdata.UserDataPlugin,cloudbaseinit.plugins.common.localscripts.LocalScriptsPlugin
first_logon_behaviour=no
rename_admin_user=false
allow_reboot=false
check_latest_version=false
`
}

// buildWindowsPantherUnattendXML 根据 Windows 版本生成 Unattend.xml 内容。
// specialize pass: 禁用 AutoLogon + 设置临时密码（仅在用户指定密码时）
// oobeSystem pass: 跳过 OOBE 向导（Server 2025 / Windows 11 需要额外的 UserAccounts + AutoLogon）
// hasPassword: 用户是否指定了密码；为 false 时不设置临时密码，保留镜像原有密码
func buildWindowsPantherUnattendXML(category string, hasPassword bool) string {
	needOOBEBypass := strings.EqualFold(category, "WindowsServer2025") ||
		strings.EqualFold(category, "Windows11")

	// Windows Server 2025 oobeSystem pass 必须同时包含：
	// 1. UserAccounts/AdministratorPassword → 告知 Windows 管理员密码已配置
	// 2. AutoLogon(LogonCount=1) → 强制跳过 Server 版的密码设置屏幕
	oobeContent := `
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <NetworkLocation>Work</NetworkLocation>
        <ProtectYourPC>1</ProtectYourPC>
        <SkipMachineOOBE>true</SkipMachineOOBE>
        <SkipUserOOBE>true</SkipUserOOBE>
      </OOBE>`
	if needOOBEBypass && hasPassword {
		oobeContent = `
      <UserAccounts>
        <AdministratorPassword>
          <Value>Temp@BootInit#1</Value>
          <PlainText>true</PlainText>
        </AdministratorPassword>
      </UserAccounts>
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <NetworkLocation>Work</NetworkLocation>
        <ProtectYourPC>1</ProtectYourPC>
        <SkipMachineOOBE>true</SkipMachineOOBE>
        <SkipUserOOBE>true</SkipUserOOBE>
      </OOBE>`
	}

	// specialize pass 命令列表：始终禁用 AutoLogon，仅在用户指定密码时设置临时密码
	specializeCommands := `
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>cmd.exe /c reg add "HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon" /v AutoAdminLogon /t REG_SZ /d 0 /f</Path>
          <Description>Disable AutoLogon left over from template creation</Description>
          <WillReboot>Never</WillReboot>
        </RunSynchronousCommand>`
	if hasPassword {
		specializeCommands += `
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Path>cmd.exe /c net user Administrator "Temp@BootInit#1" /logonpasswordchg:no /active:yes</Path>
          <Description>Set temp password to prevent passwordless auto-login before cloudbase-init sets the real password</Description>
          <WillReboot>Never</WillReboot>
        </RunSynchronousCommand>`
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="generalize">
    <component name="Microsoft-Windows-PnpSysprep" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
      <PersistAllDeviceInstalls>true</PersistAllDeviceInstalls>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">%s
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Deployment" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
      <RunSynchronous>%s
      </RunSynchronous>
    </component>
  </settings>
</unattend>`, oobeContent, specializeCommands)
}

// detectWindowsNTFSPartition 检测磁盘镜像中的 Windows NTFS 系统分区。
// 使用 guestfish 列举文件系统，返回包含 /Windows 目录的 NTFS 分区设备路径。
// 如果检测失败，返回空字符串。
func detectWindowsNTFSPartition(diskPath string) string {
	// 列出所有文件系统
	result := utils.ExecCommandLongRunning("guestfish", "--ro", "-a", diskPath,
		"run", ":", "list-filesystems")
	if result.Error != nil {
		logger.App.Warn("检测 Windows 分区失败: guestfish list-filesystems", "error", result.Stderr)
		return ""
	}

	// 解析输出，寻找 NTFS 分区（格式: /dev/sdaX: ntfs）
	var ntfsPartitions []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "ntfs") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) >= 1 {
				ntfsPartitions = append(ntfsPartitions, strings.TrimSpace(parts[0]))
			}
		}
	}

	if len(ntfsPartitions) == 0 {
		logger.App.Warn("未发现 NTFS 分区", "disk", diskPath)
		return ""
	}

	// 尝试每个 NTFS 分区，找到含 /Windows 目录的系统分区
	for _, part := range ntfsPartitions {
		checkResult := utils.ExecCommandLongRunning("guestfish", "--ro", "-a", diskPath,
			"run", ":", "mount-ro", part, "/", ":", "is-dir", "/Windows")
		if checkResult.Error == nil && strings.TrimSpace(checkResult.Stdout) == "true" {
			logger.App.Info("检测到 Windows 系统分区", "disk", diskPath, "partition", part)
			return part
		}
	}

	// 未找到含 /Windows 的分区，回退使用第一个 NTFS 分区
	logger.App.Warn("未找到含 /Windows 的 NTFS 分区，使用第一个 NTFS 分区", "partition", ntfsPartitions[0])
	return ntfsPartitions[0]
}

// InjectWindowsCloudbaseInitFilesExported 是 injectWindowsCloudbaseInitFiles 的导出版本
// 通过 virt-customize 向克隆磁盘注入 CloudbaseInit 配置文件
// password 参数用于控制 inject_user_password 设置：为空时不注入密码，非空时注入密码
func InjectWindowsCloudbaseInitFilesExported(vmName, cloneDisk, category, password string, progressFn func(int, string)) {
	injectWindowsCloudbaseInitFiles(vmName, cloneDisk, category, password, progressFn)
}

// injectWindowsCloudbaseInitFiles 通过 virt-customize 向克隆磁盘注入配置文件：
//  1. /Program Files/Cloudbase Solutions/Cloudbase-Init/conf/Unattend.xml
//  2. /Windows/Panther/unattend.xml
//  3. /Program Files/Cloudbase Solutions/Cloudbase-Init/conf/cloudbase-init.conf
//
// 当 libguestfs 的 OS 自动检测失败时（Windows Server 2025 已知问题），
// 自动回退为显式挂载 NTFS 分区（通过 -m 参数），绕过 OS 检测。
// 注入失败仅记录警告，不中断克隆流程。
// password 参数用于控制 inject_user_password 设置：为空时不注入密码，非空时注入密码
func injectWindowsCloudbaseInitFiles(vmName, cloneDisk, category string, password string, progressFn func(int, string)) {
	if progressFn == nil {
		progressFn = func(int, string) {}
	}
	progressFn(35, "注入 CloudbaseInit 配置文件...")

	confContent := buildWindowsCloudbaseInitConf()
	if password != "" {
		confContent = strings.Replace(confContent, "inject_user_password=false", "inject_user_password=true", 1)
	}
	confPath := fmt.Sprintf("/tmp/_cbi-conf-%s.conf", vmName)
	_ = os.WriteFile(confPath, []byte(confContent), 0600)
	defer func() { _ = os.Remove(confPath) }()

	unattendContent := buildWindowsPantherUnattendXML(category, password != "")
	unattendPath := fmt.Sprintf("/tmp/_cbi-unattend-%s.xml", vmName)
	_ = os.WriteFile(unattendPath, []byte(unattendContent), 0600)
	defer func() { _ = os.Remove(unattendPath) }()

	// 构造注入参数（上传到三个关键路径）
	uploadArgs := []string{
		"--mkdir", "/Windows/Panther/Unattend",
		"--upload", unattendPath + `:/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/Unattend.xml`,
		"--upload", unattendPath + ":/Windows/Panther/unattend.xml",
		"--upload", confPath + `:/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/cloudbase-init.conf`,
		"--quiet",
	}

	// 第一次尝试：使用默认 OS 检测（适用于大多数 Windows 版本）
	args := append([]string{"-a", cloneDisk, "--no-network"}, uploadArgs...)
	injectResult := utils.ExecCommandLongRunning("virt-customize", args...)

	if injectResult.Error != nil && strings.Contains(injectResult.Stderr, "no operating system") {
		// OS 检测失败（Windows Server 2025 已知问题），回退为 guestfish 显式挂载分区
		logger.App.Info("virt-customize OS 检测失败，尝试 guestfish 显式挂载 NTFS 分区", "vm", vmName)
		progressFn(36, "检测 Windows 分区...")

		winPart := detectWindowsNTFSPartition(cloneDisk)
		if winPart == "" {
			progressFn(38, "CloudbaseInit 配置文件注入失败，无法检测 Windows 分区")
			logger.App.Warn("注入 CloudbaseInit 配置失败: 无法检测 Windows 分区", "vm", vmName)
			return
		}

		// 使用 guestfish 显式挂载分区，绕过 OS 检测
		// ntfsfix 清除可能的脏标记（重装场景中 VM 可能未正常关机）
		progressFn(37, "通过 guestfish 注入文件...")
		injectResult = utils.ExecCommandLongRunning("guestfish", "--rw", "-a", cloneDisk,
			"run", ":",
			"ntfsfix", winPart, ":",
			"mount", winPart, "/", ":",
			"mkdir-p", "/Windows/Panther/Unattend", ":",
			"upload", unattendPath, "/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/Unattend.xml", ":",
			"upload", unattendPath, "/Windows/Panther/unattend.xml", ":",
			"upload", confPath, "/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/cloudbase-init.conf")
	}

	if injectResult.Error != nil {
		progressFn(38, "CloudbaseInit 配置文件注入失败，首次启动可能需要手动设置")
		logger.App.Warn("注入 CloudbaseInit 配置失败", "vm", vmName, "error", injectResult.Stderr)
	} else {
		logger.App.Info("CloudbaseInit 配置文件注入成功", "vm", vmName)
	}
}

func windowsSystemDiskTargetDev(bus string) string {
	switch D.NormalizeVMDiskBus(bus) {
	case "sata", "scsi":
		return "sda"
	case "ide":
		return "hda"
	default:
		return "vda"
	}
}

func windowsDiskControllerXML(bus string) string {
	switch D.NormalizeVMDiskBus(bus) {
	case "sata":
		return "    <controller type='sata' index='0'/>\n"
	case "scsi":
		return "    <controller type='scsi' index='0' model='virtio-scsi'/>\n"
	default:
		return ""
	}
}

// setWindowsCloudbaseInitPasswordInjection 设置 CloudbaseInit 的密码注入功能。
// usePassword: true=启用密码注入（设为 true，Cloudbase-Init 使用 Config Drive 中的密码）
//
//	false=禁用密码注入（设为 false 且移除 SetUserPasswordPlugin，保留镜像原有密码）
//
// 使用 guestfish 直接修改文件，比 virt-customize 快得多。
// 修改失败仅记录警告，不中断克隆流程。
func setWindowsCloudbaseInitPasswordInjection(cloneDisk string, usePassword bool) {
	winPartition := detectWindowsNTFSPartition(cloneDisk)
	if winPartition == "" {
		logger.App.Warn("未找到 Windows 分区，跳过设置密码注入", "disk", cloneDisk)
		return
	}

	confPaths := []string{
		"/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/cloudbase-init.conf",
		"/Program Files/Cloudbase Solutions/Cloudbase-Init/conf/cloudbase-init-unattend.conf",
	}

	for _, confPath := range confPaths {
		result := utils.ExecCommandLongRunning("guestfish", "--rw", "-a", cloneDisk,
			"run", ":",
			"mount", winPartition, "/", ":",
			"cat", confPath)
		if result.Error != nil {
			continue
		}

		originalContent := result.Stdout
		modifiedContent := originalContent

		if usePassword {
			if strings.Contains(originalContent, "inject_user_password=false") {
				modifiedContent = strings.Replace(modifiedContent, "inject_user_password=false", "inject_user_password=true", 1)
			} else if !strings.Contains(modifiedContent, "inject_user_password=true") {
				modifiedContent = modifiedContent + "\n" + "inject_user_password=true\n"
			}
			// 确保 SetUserPasswordPlugin 在插件列表中（从之前的移除中恢复）
			if !strings.Contains(modifiedContent, "cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin") {
				// 在 plugins 行末尾添加 SetUserPasswordPlugin
				modifiedContent = strings.Replace(modifiedContent,
					"plugins=",
					"plugins=cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin,",
					1)
			}
		} else {
			if strings.Contains(modifiedContent, "inject_user_password=true") {
				modifiedContent = strings.Replace(modifiedContent, "inject_user_password=true", "inject_user_password=false", 1)
			} else if !strings.Contains(modifiedContent, "inject_user_password=false") {
				modifiedContent = modifiedContent + "\n" + "inject_user_password=false\n"
			}
			modifiedContent = strings.Replace(modifiedContent, ",cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin", "", -1)
			modifiedContent = strings.Replace(modifiedContent, "cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin,", "", -1)
			modifiedContent = strings.Replace(modifiedContent, "cloudbaseinit.plugins.common.setuserpassword.SetUserPasswordPlugin", "", -1)
		}

		if modifiedContent == originalContent {
			continue
		}

		tmpFile := fmt.Sprintf("/tmp/_cbi_set_%d.conf", time.Now().UnixNano())
		if err := os.WriteFile(tmpFile, []byte(modifiedContent), 0600); err != nil {
			logger.App.Warn("写入临时配置文件失败", "error", err)
			continue
		}
		defer func() { _ = os.Remove(tmpFile) }()

		writeResult := utils.ExecCommandLongRunning("guestfish", "--rw", "-a", cloneDisk,
			"run", ":",
			"mount", winPartition, "/", ":",
			"upload", tmpFile, confPath)
		if writeResult.Error != nil {
			logger.App.Warn("写回 cloudbase-init.conf 失败", "disk", cloneDisk, "error", writeResult.Stderr)
			continue
		}

		if usePassword {
			logger.App.Info("已启用 CloudbaseInit 密码注入（用户指定了密码）", "disk", cloneDisk, "conf", confPath)
		} else {
			logger.App.Info("已禁用 CloudbaseInit 密码注入（用户未指定密码）", "disk", cloneDisk, "conf", confPath)
		}
	}
}

// SetWindowsCloudbaseInitPasswordInjectionExported 是 setWindowsCloudbaseInitPasswordInjection 的导出版本
func SetWindowsCloudbaseInitPasswordInjectionExported(cloneDisk string, usePassword bool) {
	setWindowsCloudbaseInitPasswordInjection(cloneDisk, usePassword)
}

// DisableWindowsCloudbaseInitPasswordInjectionExported 是 setWindowsCloudbaseInitPasswordInjection 的导出版本（兼容旧调用）
func DisableWindowsCloudbaseInitPasswordInjectionExported(cloneDisk string) {
	setWindowsCloudbaseInitPasswordInjection(cloneDisk, false)
}

// disableWindowsCloudbaseInitPasswordInjection 禁用 CloudbaseInit 的密码注入功能（兼容旧调用）
func disableWindowsCloudbaseInitPasswordInjection(cloneDisk string) {
	setWindowsCloudbaseInitPasswordInjection(cloneDisk, false)
}

// cloneWindows Windows 克隆逻辑
// cloneDir: 虚拟机磁盘所在的存储目录，额外磁盘也会创建在此目录
func cloneWindows(ctx context.Context, params *CloneParams, cloneDisk string, ramMB int, memoryMeta *memory.VMMemoryMetadata, needUEFI bool, isNoInit bool, progressFn func(int, string), cloneDir string) error {
	templateDir := config.GlobalConfig.TemplateDir

	// 获取宿主机架构 Profile，参数化 arch/machine/emulator/watchdog
	hostArch := arch.DetectHostArch()
	profile := arch.GetProfile(hostArch)
	archName := profile.Arch()
	machineType := profile.DefaultMachineType()
	emulatorPath := profile.EmulatorPath()
	watchdogModel := profile.DefaultWatchdogModel()
	isX8664 := archName == arch.ArchX8664

	// Hyper-V enlightenments 仅在 x86_64 架构上支持
	var hyperVBlock, hyperVTimerBlock string
	if isX8664 {
		hyperVBlock = "    <hyperv mode='custom'>\n      <relaxed state='on'/><vapic state='on'/><spinlocks state='on' retries='8191'/>\n    </hyperv>\n    "
		hyperVTimerBlock = "<timer name='pit' tickpolicy='delay'/>\n    <timer name='hpet' present='no'/><timer name='hypervclock' present='yes'/>"
	}

	var isoPath string
	var isoErr error
	if !isNoInit && (params.Hostname != "" || params.Password != "") {
		// 创建 Config Drive ISO（包含实例 hostname、admin_pass、instance-id）
		// 模板磁盘已预安装 cloudbase-init，只通过 Config Drive 传递配置，不使用 virt-customize（太慢）
		isoPath, isoErr = createWindowsConfigDriveISO(params.Name, params.Hostname, params.Password, params.User)
		if isoErr != nil {
			logger.App.Warn("创建 Windows Config Drive ISO 失败，CloudbaseInit 将无法自动注入配置",
				"vm", params.Name, "error", isoErr)
		}
	}

	// 使用 guestfish 快速修改 cloudbase-init.conf（不使用 virt-customize，避免10分钟等待）
	// 根据用户是否提供密码，设置 inject_user_password：
	// - 有密码：设为 true，Cloudbase-Init 使用 Config Drive 中的密码
	// - 无密码：设为 false，Cloudbase-Init 不修改密码，保留镜像原有密码
	if !isNoInit {
		setWindowsCloudbaseInitPasswordInjection(cloneDisk, params.Password != "")
	}

	nvramClone := ""
	if needUEFI {
		nvramTemplate := filepath.Join(templateDir, "win2k22-nvram.fd")
		nvramClone = fmt.Sprintf("/var/lib/libvirt/qemu/nvram/%s_VARS.fd", params.Name)

		if utils.FileExists(nvramTemplate) {
			if err := vm_xml.CreateQCOW2NVRAMFromTemplate(nvramTemplate, nvramClone); err != nil {
				return err
			}
		} else {
			if err := vm_xml.CreateQCOW2NVRAMFromTemplate("/usr/share/OVMF/OVMF_VARS_4M.ms.fd", nvramClone); err != nil {
				return err
			}
		}
	}

	progressFn(40, "生成 Windows VM XML...")

	// 网络接口 XML：仅在有主网口交换机配置时才添加
	var networkXML string
	if params.SwitchID != 0 {
		macResult := utils.ExecShell(`printf '52:54:00:%02x:%02x:%02x' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))`)
		macAddr := strings.TrimSpace(macResult.Stdout)
		if macAddr == "" {
			macAddr = "52:54:00:aa:bb:cc"
		}
		networkXML = D.BuildOVSInterfaceXML(macAddr, params.NicModel) + "\n"
	}

	ramKiB := ramMB * 1024
	diskBus := D.NormalizeVMDiskBus(params.DiskBus)
	if diskBus == "" {
		diskBus = "virtio"
	}
	diskTargetDev := windowsSystemDiskTargetDev(diskBus)
	diskControllerXML := windowsDiskControllerXML(diskBus)
	osXML := fmt.Sprintf(`  <os>
    <type arch='%s' machine='%s'>hvm</type>
    <boot dev='hd'/>
  </os>`, archName, machineType)
	smmXML := ""
	tpmXML := ""
	if needUEFI {
		// 使用显式 loader/nvram 模式，不使用 firmware='efi' 自动选择，
		// 避免 libvirt 自动填充 nvram format='raw' 与 qcow2 格式不匹配导致黑屏。
		loaderPath := vm_xml.ResolveOVMFLoaderPath(true)
		varsTemplate := vm_xml.ResolveOVMFVarsTemplatePath(true)
		osXML = fmt.Sprintf(`  <os>
    <type arch='%s' machine='%s'>hvm</type>
    <loader readonly='yes' secure='yes' type='pflash'>%s</loader>
    <nvram template='%s' templateFormat='raw' format='qcow2'>%s</nvram>
    <boot dev='hd'/>
  </os>`, archName, machineType, loaderPath, varsTemplate, nvramClone)
		smmXML = "<smm state='on'/>"
		tpmXML = "    <tpm model='tpm-crb'><backend type='emulator' version='2.0'/></tpm>\n"
	}

	rtcOffset := D.ResolveRTCOffset(params.RTCOffset, "windows")
	rtcStartDate := D.NormalizeRTCStartDate(params.RTCStartDate)
	clockOpenTag := fmt.Sprintf("<clock offset='%s'>", rtcOffset)
	if rtcStartDate != D.VMRTCStartDateNow {
		epoch, err := D.ParseRTCStartDateToEpoch(rtcStartDate)
		if err != nil {
			return err
		}
		rtcOffset = D.VMRTCOffsetAbsolute
		clockOpenTag = fmt.Sprintf("<clock offset='%s' start='%s'>", rtcOffset, epoch)
	}
	vmXML := fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='KiB'>%d</memory>
%s
%s
  <features>
    <acpi/><apic/>
    %s<vmport state='off'/>%s
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
      <driver name='qemu' type='qcow2' discard='unmap' detect_zeroes='unmap'/>
      <source file='%s'/><target dev='%s' bus='%s'/>
    </disk>
    <controller type='usb' index='0' model='qemu-xhci' ports='15'/>
    <controller type='virtio-serial' index='0'/>
%s
%s
    <input type='tablet' bus='usb'/>
%s
    <graphics type='vnc' port='-1' autoport='yes' listen='0.0.0.0'>
      <listen type='address' address='0.0.0.0'/>
    </graphics>
    <video><model type='virtio' heads='1' primary='yes'/></video>
    <watchdog model='%s' action='reset'/>
    <memballoon model='virtio' freePageReporting='on'><stats period='5'/></memballoon>
  </devices>
</domain>`,
		params.Name, ramKiB, D.BuildVCPUTag(params.VCPU, params.MaxVCPU), osXML, smmXML, hyperVBlock, clockOpenTag, hyperVTimerBlock, emulatorPath, cloneDisk, diskTargetDev, diskBus, diskControllerXML, networkXML, tpmXML, watchdogModel)
	var err error
	if memoryMeta != nil {
		vmXML, err = memory.ApplyMemoryMetadataToDomainXML(vmXML, memoryMeta, false)
		if err != nil {
			return err
		}
	}
	vmXML, err = vm_xml.ApplyVMGuestAgentConfigToDomainXML(vmXML, params.GuestAgent)
	if err != nil {
		return err
	}
	vmXML, err = vm_xml.ApplySMBIOS1ConfigToDomainXML(vmXML, params.SMBIOS1, true)
	if err != nil {
		return err
	}
	vmXML, err = D.ApplyVMAPICToDomainXML(vmXML, params.APIC)
	if err != nil {
		return err
	}
	vmXML, err = vm_xml.ApplyVMPAEToDomainXML(vmXML, params.PAE)
	if err != nil {
		return err
	}
	vmXML = vm_xml.ApplyVMVideoModelToDomainXML(vmXML, params.VideoModel, "windows")
	vmXML = vm_xml.ApplyWindowsGuestOptimizationsToDomainXML(vmXML)
	topoVCPU := D.EffectiveTopologyVCPU(params.VCPU, params.MaxVCPU)
	vmXML = D.ApplyCPUTopologyModeToDomainXML(vmXML, params.CPUTopologyMode, "windows", topoVCPU)
	vmXML = D.ApplyVMCPULimitToDomainXML(vmXML, params.VCPU, params.CPULimitPercent)
	if params.CPUAffinity != "" {
		var affErr error
		vmXML, affErr = D.ApplyCPUAffinityIfSet(vmXML, topoVCPU, params.CPUAffinity)
		if affErr != nil {
			return affErr
		}
	}
	firstBootColdReboot := D.ShouldUseWindowsFirstBootColdReboot(params.FirstBootRebootMode, "windows")
	if firstBootColdReboot {
		vmXML = D.ApplyFirstBootRebootModeToDomainXML(vmXML, params.FirstBootRebootMode)
	}
	vmXML, err = D.ApplyVPCSwitchToDomainXML(vmXML, params.SwitchID)
	if err != nil {
		return err
	}

	// 将 Config Drive ISO 挂载为 SATA CD-ROM，供 CloudbaseInit 首次启动时读取
	if !isNoInit && isoPath != "" {
		vmXML = addConfigDriveCDROMToXML(vmXML, isoPath, diskBus, diskTargetDev)
	}

	// 嵌套虚拟化开关（默认启用，host-passthrough 下需 policy='disable' 覆盖）
	if params.NestedVirt == nil || *params.NestedVirt {
		featureName := vm_xml.DetectHostNestedVirtFeatureName()
		enabled := true
		vmXML, err = vm_xml.ApplyNestedVirtToDomainXML(vmXML, &enabled, featureName)
		if err != nil {
			return err
		}
	} else {
		featureName := vm_xml.DetectHostNestedVirtFeatureName()
		disabled := false
		vmXML, err = vm_xml.ApplyNestedVirtToDomainXML(vmXML, &disabled, featureName)
		if err != nil {
			return err
		}
	}

	// 隐藏 KVM 标志
	if params.KVMHidden != nil {
		vmXML, err = vm_xml.ApplyKVMHiddenToDomainXML(vmXML, params.KVMHidden)
		if err != nil {
			return err
		}
	}

	// Hyper-V vendor_id 伪装
	if strings.TrimSpace(params.VendorID) != "" {
		vmXML, err = vm_xml.ApplyVendorIDToHyperVBlock(vmXML, params.VendorID)
		if err != nil {
			return err
		}
	}

	if _, err := libvirt_rpc.DefineDomainXMLRPC(vmXML); err != nil {
		return fmt.Errorf("定义虚拟机失败: %w", err)
	}
	if memoryMeta != nil {
		if err := memory.WriteVMMemoryMetadata(params.Name, memoryMeta); err != nil {
			return err
		}
	}
	cloneMode := params.CloneMode
	if cloneMode == "" {
		cloneMode = "linked"
	}
	if err := D.WriteVMTemplateSource(params.Name, params.Template, cloneMode); err != nil {
		logger.App.Warn("写入VM模板源信息失败", "error", err)
	}
	if err := D.SetVMRemark(params.Name, params.Remark); err != nil {
		logger.App.Warn("设置VM备注失败", "error", err)
	}

	if err := D.SetVMFreeze(params.Name, params.Freeze); err != nil {
		logger.App.Warn("设置VM冻结配置失败", "error", err)
	}

	if params.SwitchID != 0 && D.BindVMToVPCAsAdmin != nil {
		if err := D.BindVMToVPCAsAdmin(params.Name, params.SwitchID, params.SecurityGroupID); err != nil {
			logger.App.Warn("绑定虚拟机到 VPC 交换机失败", "vm", params.Name, "switch_id", params.SwitchID, "error", err)
		}
	}

	// 额外磁盘：在启动前冷添加，避免占用 PCIe 热插槽
	if len(params.ExtraDisks) > 0 {
		if err := D.AddExtraDisksForVM(params.Name, params.ExtraDisks, cloneDir, params.DiskBus, params.IsAdmin, nil); err != nil {
			return fmt.Errorf("挂载额外磁盘失败: %w", err)
		}
	}

	startFn := D.StartVM
	if firstBootColdReboot {
		startFn = D.StartVMPreserveRebootAction
	}
	if err := startFn(params.Name); err != nil {
		return err
	}
	if firstBootColdReboot {
		if err := D.CompleteWindowsFirstBootColdReboot(ctx, params.Name, progressFn); err != nil {
			return err
		}
	}

	// 在后台等待 QEMU Guest Agent 连接后自动弹出并清理 Config Drive CD-ROM
	if !isNoInit && isoPath != "" {
		scheduleWindowsConfigDriveEject(params.Name, diskBus, diskTargetDev)
	}

	return nil
}

// generateRandomPassword 生成指定长度的随机密码（包含大小写字母和数字）
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[n.Int64()]
	}
	return string(password)
}
