package template

import (
	"fmt"
	"strings"
)

// ValidateTemplateCategory validates the template category against its type.
func ValidateTemplateCategory(templateType, category string) error {
	normalizedType := normalizeTemplateType(templateType)
	category = strings.TrimSpace(category)
	if normalizedType != "linux" && normalizedType != "windows" && normalizedType != "other" {
		if category != "" {
			return fmt.Errorf("仅 Linux、Windows 和 Other 模板支持设置二级分类")
		}
		return nil
	}
	if category != "" {
		var allowedCategories []string
		switch normalizedType {
		case "windows":
			allowedCategories = windowsTemplateCategories
		case "other":
			allowedCategories = otherTemplateCategories
		default:
			allowedCategories = linuxTemplateCategories
		}
		for _, allowed := range allowedCategories {
			if strings.EqualFold(category, allowed) {
				return nil
			}
		}
		switch normalizedType {
		case "windows":
			return fmt.Errorf("Windows 模板分类仅支持 WindowsServer2025、WindowsServer2022、Windows11、Windows10、WindowsServer2012R2 或 其它")
		case "other":
			return fmt.Errorf("Other 模板分类仅支持 FnOS、OpenWrt 或 其它")
		default:
			return fmt.Errorf("Linux 模板分类仅支持 Ubuntu、Debian、CentOS、UOS 或 Kylin")
		}
	}
	return nil
}
