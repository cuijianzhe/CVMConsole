package vm_xml

import (
	"fmt"
	"regexp"
	"strings"
)

var vgpuHostdevBlockRegexp = regexp.MustCompile(`(?s)<hostdev\s+mode=['"]subsystem['"]\s+type=['"]mdev['"][^>]*>.*?</hostdev>`)

func GenerateVGPUHostdevXML(uuid string) string {
	return fmt.Sprintf(`    <hostdev mode='subsystem' type='mdev' managed='no'>
      <source>
        <address uuid='%s'/>
      </source>
    </hostdev>`, uuid)
}

func ApplyVGPUToDomainXML(xmlStr, vgpuUUID string) (string, error) {
	if strings.TrimSpace(vgpuUUID) == "" {
		return xmlStr, nil
	}

	vgpuHostdevBlockRegexp = regexp.MustCompile(`(?s)<hostdev\s+mode=['"]subsystem['"]\s+type=['"]mdev['"][^>]*>.*?</hostdev>`)

	if vgpuHostdevBlockRegexp.MatchString(xmlStr) {
		return vgpuHostdevBlockRegexp.ReplaceAllString(xmlStr, GenerateVGPUHostdevXML(vgpuUUID)), nil
	}

	if strings.Contains(xmlStr, "</devices>") {
		return strings.Replace(xmlStr, "</devices>", GenerateVGPUHostdevXML(vgpuUUID)+"\n  </devices>", 1), nil
	}

	return xmlStr, fmt.Errorf("无法在 domain XML 中找到 devices 块")
}
