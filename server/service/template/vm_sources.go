package template

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/utils"
)

// listVMTemplateSources scans VM XML files to find template source references.
func listVMTemplateSources() ([]TemplateRelatedVM, error) {
	xmlPaths, err := filepath.Glob("/etc/libvirt/qemu/*.xml")
	if err != nil {
		return nil, err
	}
	sort.Strings(xmlPaths)

	result := make([]TemplateRelatedVM, 0, len(xmlPaths))
	for _, xmlPath := range xmlPaths {
		content, err := os.ReadFile(xmlPath)
		if err != nil {
			continue
		}
		text := string(content)
		nameMatch := templateSourceNamePattern.FindStringSubmatch(text)
		if len(nameMatch) < 2 {
			continue
		}
		vmName := strings.TrimSuffix(filepath.Base(xmlPath), ".xml")
		nodeID := ""
		nodeMatch := templateSourceNodePattern.FindStringSubmatch(text)
		if len(nodeMatch) >= 2 {
			nodeID = strings.TrimSpace(nodeMatch[1])
		}
		cloneMode := ""
		cloneMatch := templateSourceCloneModePattern.FindStringSubmatch(text)
		if len(cloneMatch) >= 2 {
			cloneMode = strings.TrimSpace(cloneMatch[1])
		}
		result = append(result, TemplateRelatedVM{
			Name:      vmName,
			Template:  strings.TrimSpace(nameMatch[1]),
			NodeID:    nodeID,
			CloneMode: cloneMode,
		})
	}
	return result, nil
}

// WriteVMTemplateSource writes template source metadata to a VM's libvirt XML.
func WriteVMTemplateSource(vmName, templateName, cloneMode string) error {
	wrapper := vmTemplateSource{
		XMLNS:        vmTemplateSourceMetadataURI,
		TemplateName: templateName,
		CloneMode:    cloneMode,
	}

	// 尝试获取模板的额外信息（UID、NodeID），如果失败则跳过，仍然写入模板名
	if tpl, err := GetTemplateInfoByName(templateName); err == nil {
		wrapper.TemplateUID = tpl.TemplateUID
		wrapper.NodeID = tpl.NodeID
	}

	xmlBytes, err := xml.Marshal(wrapper)
	if err != nil {
		return err
	}

	logger.App.Info("写入VM模板源信息", "vm", vmName, "template", templateName, "cloneMode", cloneMode)
	result := utils.ExecCommand(
		"virsh", "metadata", vmName, vmTemplateSourceMetadataURI,
		"--config", "--key", vmTemplateSourceMetadataKey, "--set", string(xmlBytes),
	)
	if result.Error != nil {
		logger.App.Error("写入虚拟机模板来源失败", "vm", vmName, "stderr", result.Stderr)
		return fmt.Errorf("写入虚拟机模板来源失败: %s", result.Stderr)
	}
	logger.App.Info("VM模板源信息写入成功", "vm", vmName, "template", templateName)

	// 直接更新数据库缓存，确保前端能立即看到模板信息
	if model.DB != nil && vmName != "" && templateName != "" {
		if err := model.DB.Model(&model.VMCache{}).Where("name = ?", vmName).Update("template", templateName).Error; err != nil {
			logger.App.Warn("更新VM缓存模板信息失败", "vm", vmName, "template", templateName, "error", err)
		} else {
			logger.App.Info("VM缓存模板信息已更新", "vm", vmName, "template", templateName)
		}
	}

	return nil
}

// ReadVMTemplateSource reads template source metadata from a VM's libvirt XML.
func ReadVMTemplateSource(vmName string) *vmTemplateSource {
	result := utils.ExecCommand("virsh", "metadata", vmName, vmTemplateSourceMetadataURI, "--config")
	if result.Error != nil || strings.TrimSpace(result.Stdout) == "" {
		logger.App.Info("读取VM模板源信息 - 未找到", "vm", vmName, "error", result.Error, "stdout", result.Stdout)
		return nil
	}
	logger.App.Info("读取VM模板源信息 - 成功", "vm", vmName, "stdout", result.Stdout)
	var source vmTemplateSource
	if err := xml.Unmarshal([]byte(result.Stdout), &source); err != nil {
		logger.App.Error("解析VM模板源信息失败", "vm", vmName, "error", err)
		return nil
	}
	return &source
}

// ReadVMTemplateSourceExported is the exported version that returns template name string
func ReadVMTemplateSourceExported(vmName string) string {
	source := ReadVMTemplateSource(vmName)
	if source != nil {
		return source.TemplateName
	}
	return ""
}
