package clone

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/service/snapshot"
	"kvm_console/service/vm_xml"
	"kvm_console/utils"
)

func shutdownVMForReinstall(ctx context.Context, vmName string, progressFn func(int, string)) error {
	if progressFn != nil {
		progressFn(18, "正在强制关闭虚拟机...")
	}
	err := libvirt_rpc.DestroyDomainRPC(vmName)
	if err != nil {
		state, stateErr := libvirt_rpc.GetDomainStateRPC(vmName)
		if stateErr != nil || (strings.ToLower(state) != "shut off" && strings.ToLower(state) != "shutoff") {
			return fmt.Errorf("强制断电失败: %w", err)
		}
	}
	shutOff, err := D.WaitForVMShutOff(ctx, vmName, 30*time.Second)
	if err != nil {
		return err
	}
	if !shutOff {
		return fmt.Errorf("强制关闭虚拟机超时，请稍后重试")
	}
	return nil
}

func createReinstallSystemDisk(templatePath, targetPath string, diskSize int) error {
	sizeArg := ""
	if diskSize > 0 {
		// 使用 -o size 参数指定新磁盘大小，这是正确的 qemu-img create 语法
		sizeArg = fmt.Sprintf("-o size=%dG", diskSize)
	}
	cmd := fmt.Sprintf("qemu-img create -f qcow2 -F qcow2 -b %s %s %s", utils.ShellSingleQuote(templatePath), sizeArg, utils.ShellSingleQuote(targetPath))
	result := utils.ExecShell(cmd)
	if result.Error != nil {
		return fmt.Errorf("创建新系统盘失败: %s", D.FirstNonEmpty(result.Stderr, result.Error.Error()))
	}
	_ = utils.ExecCommand("chown", "libvirt-qemu:kvm", targetPath)
	return nil
}

func bestEffortRestoreReinstallDisk(originalDiskPath, backupDiskPath string) error {
	if strings.TrimSpace(backupDiskPath) == "" {
		return nil
	}
	if strings.TrimSpace(originalDiskPath) != "" {
		_ = os.Remove(originalDiskPath)
	}
	if err := os.Rename(backupDiskPath, originalDiskPath); err != nil {
		return fmt.Errorf("恢复原系统盘失败: %w", err)
	}
	_ = utils.ExecCommand("chown", "libvirt-qemu:kvm", originalDiskPath)
	return nil
}

func buildReinstallCloneParams(params *ReinstallParams, diskBus string, templateMeta *TemplateMeta) *CloneParams {
	if templateMeta == nil {
		templateMeta = &TemplateMeta{}
	}
	cloneParams := &CloneParams{
		Name:                 params.Name,
		Template:             params.Template,
		TemplateType:         params.TemplateType,
		TemplateCategory:     templateMeta.Category,
		DiskSize:             params.DiskSize,
		Hostname:             strings.TrimSpace(params.Hostname),
		User:                 strings.TrimSpace(params.User),
		Password:             params.Password,
		TemplateRootPass:     params.TemplateRootPass,
		TemplateUser:         params.TemplateUser,
		DiskBus:              D.NormalizeVMDiskBus(diskBus),
		FirstBootRebootMode:  D.NormalizeVMFirstBootRebootMode(params.FirstBootRebootMode),
		PreserveFnOSDeviceID: params.PreserveFnOSDeviceID,
		FnOSDeviceID:         params.FnOSDeviceID,
	}
	if cloneParams.TemplateType == "" {
		cloneParams.TemplateType = strings.ToLower(strings.TrimSpace(templateMeta.Type))
	}
	if cloneParams.TemplateType == "" {
		cloneParams.TemplateType = "linux"
	}

	nameLower := strings.ToLower(strings.TrimSpace(params.Template))
	if strings.Contains(nameLower, "win") || strings.Contains(nameLower, "windows") {
		cloneParams.TemplateType = "windows"
	} else if strings.Contains(nameLower, "fnos") || strings.Contains(nameLower, "nas") {
		cloneParams.TemplateType = "fnos"
	} else if strings.Contains(nameLower, "openwrt") || strings.Contains(nameLower, "lede") || strings.Contains(nameLower, "istoreos") {
		cloneParams.TemplateType = "openwrt"
	}

	cloneParams.User = NormalizeCloneUsernameForTemplate(cloneParams.TemplateType, cloneParams.User)
	if cloneParams.Hostname == "" {
		cloneParams.Hostname = GenerateRandomCloneHostname()
	}
	if cloneParams.TemplateRootPass == "" {
		cloneParams.TemplateRootPass = templateMeta.RootPassword
	}
	if cloneParams.TemplateUser == "" {
		cloneParams.TemplateUser = templateMeta.TemplateUser
	}
	if cloneParams.PostBootCommand == "" {
		cloneParams.PostBootCommand = templateMeta.PostBootCommand
	}
	if !cloneParams.PostBootBlocking {
		cloneParams.PostBootBlocking = templateMeta.PostBootBlocking
	}
	if cloneParams.DiskBus == "" {
		cloneParams.DiskBus = "virtio"
	}
	return cloneParams
}

// ReinstallVM 重装系统
func ReinstallVM(ctx context.Context, params *ReinstallParams, progressFn func(int, string)) (err error) {
	if progressFn == nil {
		progressFn = func(int, string) {}
	}
	if err = D.HookEnsureVMNotMigrating(params.Name, "重装系统"); err != nil {
		return err
	}

	templatePath, err := D.EnsureTemplatePath(params.Template)
	if err != nil {
		return err
	}

	meta := D.GetTemplateMeta(params.Template)
	cloneParams := buildReinstallCloneParams(params, "", meta)

	// 解析重装磁盘大小：优先使用用户指定值，否则使用当前磁盘大小，最后使用模板最小磁盘大小
	resolvedDiskSize, err := ResolveReinstallDiskSizeGB(params.Name, params.Template, cloneParams.DiskSize)
	if err != nil {
		return err
	}
	cloneParams.DiskSize = resolvedDiskSize

	isFnOS := cloneParams.TemplateType == "fnos" || (cloneParams.TemplateType == "other" && strings.EqualFold(cloneParams.TemplateCategory, "FnOS"))
	isOpenWrt := cloneParams.TemplateType == "openwrt" || (cloneParams.TemplateType == "other" && strings.EqualFold(cloneParams.TemplateCategory, "OpenWrt"))
	requireCredentials := cloneParams.TemplateType == "linux" || cloneParams.TemplateType == "windows" || isFnOS
	if err := ValidateCloneCredentialsForTemplate(cloneParams.TemplateType, cloneParams.Hostname, cloneParams.User, cloneParams.Password, requireCredentials); err != nil {
		return err
	}
	if strings.TrimSpace(cloneParams.FnOSDeviceID) != "" {
		if err := ValidateFnOSDeviceID(cloneParams.FnOSDeviceID); err != nil {
			return err
		}
		cloneParams.PreserveFnOSDeviceID = true
	}

	originalXML, err := D.GetVMInactiveDomainXML(params.Name)
	if err != nil {
		return err
	}
	currentBootType := vm_xml.ParseVMBootTypeFromDomainXML(originalXML)
	templateBootType, err := detectTemplateBootTypeForReinstall(params.Template, meta)
	if err != nil {
		return err
	}
	if !IsReinstallBootFamilyCompatible(currentBootType, templateBootType) {
		return fmt.Errorf("所选模板的启动方式与当前虚拟机不兼容，仅支持相同启动族之间重装（当前：%s，模板：%s）", normalizeBootFamily(currentBootType), normalizeBootFamily(templateBootType))
	}

	systemDisk, err := inspectVMSystemDisk(params.Name)
	if err != nil {
		return err
	}
	cloneParams.DiskBus = systemDisk.Bus
	progressFn(8, "正在检查重装环境...")

	progressFn(12, "正在清理现有快照...")
	if _, err := snapshot.DeleteAllSnapshots(params.Name, func(progress int, message string) {
		progressFn(12+progress/4, message)
	}); err != nil {
		return fmt.Errorf("重装前清理快照失败: %w", err)
	}

	if err := shutdownVMForReinstall(ctx, params.Name, progressFn); err != nil {
		return err
	}

	backupDiskPath := fmt.Sprintf("%s.backup.%d", systemDisk.Path, time.Now().UnixNano())
	if err := os.Rename(systemDisk.Path, backupDiskPath); err != nil {
		return fmt.Errorf("备份原系统盘失败: %w", err)
	}

	started := false
	xmlModified := false
	rollbackNeeded := true
	defer func() {
		if rollbackNeeded {
			_ = libvirt_rpc.DestroyDomainRPC(params.Name)
			var rollbackMessages []string
			if xmlModified {
				if restoreXMLErr := D.SetVMInactiveDomainXML(params.Name, originalXML); restoreXMLErr != nil {
					rollbackMessages = append(rollbackMessages, restoreXMLErr.Error())
				}
			}
			if started {
				time.Sleep(2 * time.Second)
			}
			if restoreDiskErr := bestEffortRestoreReinstallDisk(systemDisk.Path, backupDiskPath); restoreDiskErr != nil {
				rollbackMessages = append(rollbackMessages, restoreDiskErr.Error())
			}
			if len(rollbackMessages) > 0 {
				rollbackMessage := strings.Join(rollbackMessages, "；")
				if err != nil {
					err = fmt.Errorf("%w；回滚阶段还出现问题：%s", err, rollbackMessage)
				} else {
					err = fmt.Errorf("重装回滚失败：%s", rollbackMessage)
				}
			}
		}
	}()

	progressFn(30, "正在基于模板创建新系统盘...")
	if err := createReinstallSystemDisk(templatePath, systemDisk.Path, cloneParams.DiskSize); err != nil {
		return err
	}

	if isFnOS && meta.CloudInitMode != "none" {
		if err := D.PrepareFnOSSystemDiskExpansion(ctx, systemDisk.Path, progressFn); err != nil {
			return err
		}
		if err := cloneFnOS(cloneParams, systemDisk.Path, progressFn); err != nil {
			return err
		}
	}
	if isOpenWrt && meta.CloudInitMode != "none" {
		progressFn(25, "配置 OpenWrt 系统...")
		if err := cloneOpenWrt(cloneParams, systemDisk.Path, progressFn); err != nil {
			return err
		}
	}
	if cloneParams.TemplateType == "linux" {
		progressFn(25, "正在重置 Linux 首次启动身份...")
		if err := prepareLinuxCloneFirstBootIdentity(cloneParams, systemDisk.Path); err != nil {
			return err
		}
		cloneParams.LinuxIdentityPrepared = true
	}

	// Windows 单独处理：磁盘扩展 + 快速修改 cloudbase-init.conf（不使用 virt-customize，太慢）
	// 用于跟踪 ISO 路径以便后续弹出
	var reinstallWindowsISOPath string
	if cloneParams.TemplateType == "windows" {
		if err := D.PrepareWindowsSystemDiskExpansion(ctx, systemDisk.Path, progressFn); err != nil {
			return err
		}

		// 使用 guestfish 快速修改 cloudbase-init.conf（不使用 virt-customize，避免10分钟等待）
		// 根据用户是否提供密码，设置 inject_user_password：
		// - 有密码：设为 true，Cloudbase-Init 使用 Config Drive 中的密码
		// - 无密码：设为 false，Cloudbase-Init 不修改密码，保留镜像原有密码
		setWindowsCloudbaseInitPasswordInjection(systemDisk.Path, cloneParams.Password != "")

		// 创建 Config Drive ISO，并更新 VM XML 挂载为 CD-ROM
		// 传入用户名参数，支持创建新用户
		isoPath, isoErr := createWindowsConfigDriveISO(
			params.Name, cloneParams.Hostname, cloneParams.Password, cloneParams.User)
		if isoErr != nil {
			logger.App.Warn("重装时创建 Windows Config Drive ISO 失败，CloudbaseInit 将无法自动注入密码",
				"vm", params.Name, "error", isoErr)
		} else {
			reinstallWindowsISOPath = isoPath
			// 将 Config Drive CD-ROM 注入 VM XML（移除旧的再添加新的）
			updatedReinstallXML := removeConfigDriveCDROMFromXML(originalXML)
			var cdDev string
			updatedReinstallXML, cdDev = addConfigDriveCDROMToXML(updatedReinstallXML, reinstallWindowsISOPath, cloneParams.DiskBus, systemDisk.Device)
			if setXMLErr := D.SetVMInactiveDomainXML(params.Name, updatedReinstallXML); setXMLErr != nil {
				logger.App.Warn("更新 VM XML 添加 Config Drive CD-ROM 失败",
					"vm", params.Name, "error", setXMLErr)
			} else {
				xmlModified = true
				// 更新 originalXML，供后续冷重启逻辑和回滚使用
				originalXML = updatedReinstallXML
				// 记录 CD-ROM 设备名，供后续弹出使用
				reinstallWindowsISOPath = isoPath + "|" + cdDev
			}
		}
	}

	firstBootColdReboot := D.ShouldUseWindowsFirstBootColdReboot(cloneParams.FirstBootRebootMode, cloneParams.TemplateType)
	if firstBootColdReboot {
		progressFn(40, "正在准备 Windows 首次冷重启策略...")
		updatedXML := D.ApplyFirstBootRebootModeToDomainXML(originalXML, cloneParams.FirstBootRebootMode)
		if err := D.SetVMInactiveDomainXML(params.Name, updatedXML); err != nil {
			return fmt.Errorf("设置 Windows 首次冷重启策略失败: %w", err)
		}
		xmlModified = true
	}

	progressFn(50, "正在启动虚拟机...")
	startFn := D.StartVM
	if firstBootColdReboot {
		startFn = D.StartVMPreserveRebootAction
	}
	if err := startFn(params.Name); err != nil {
		return err
	}
	started = true

	if firstBootColdReboot {
		if err := D.CompleteWindowsFirstBootColdReboot(ctx, params.Name, progressFn); err != nil {
			return err
		}
		if err := D.SetVMInactiveDomainXML(params.Name, originalXML); err != nil {
			return fmt.Errorf("恢复首次重启策略失败: %w", err)
		}
		xmlModified = false
	}

	// Windows 重装：在后台等待 QEMU Guest Agent 连接后自动弹出并清理 Config Drive CD-ROM
	if cloneParams.TemplateType == "windows" && reinstallWindowsISOPath != "" {
		// 从 reinstallWindowsISOPath 中提取 CD-ROM 设备名（格式: isoPath|cdDev）
		var cdDev string
		if idx := strings.Index(reinstallWindowsISOPath, "|"); idx != -1 {
			cdDev = reinstallWindowsISOPath[idx+1:]
			reinstallWindowsISOPath = reinstallWindowsISOPath[:idx]
		}
		scheduleWindowsConfigDriveEject(params.Name, cloneParams.DiskBus, cdDev)
	}

	if cloneParams.TemplateType == "linux" {
		// Linux 已通过 prepareLinuxCloneFirstBootIdentity 完成全部离线初始化
		// cloud-init 将在 VM 首次启动时自动处理 hostname 确认和磁盘扩容
		progressFn(70, "Linux 离线初始化已完成，首次启动时 cloud-init 将自动扩容磁盘...")
	}

	progressFn(95, "正在更新虚拟机模板与凭据记录...")
	if err := D.WriteVMTemplateSource(params.Name, params.Template, "linked"); err != nil {
		logger.App.Warn("写入VM模板源信息失败", "error", err)
	}
	if err := D.SaveVMCredential(params.Name, cloneParams.User, cloneParams.Password, "reinstall", params.Operator, false); err != nil {
		logger.App.Warn("保存虚拟机重装凭据失败", "vm", params.Name, "error", err)
	}

	if err := os.Remove(backupDiskPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("重装成功，但清理旧系统盘备份失败: %w", err)
	}

	rollbackNeeded = false
	progressFn(100, "重装完成")
	return nil
}
