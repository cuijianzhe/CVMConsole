package vpc

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/ip_resolver"
)

func normalizeAddressField(value string, ipv6 bool) (string, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	seen := map[string]bool{}
	var result []string
	for _, field := range fields {
		addr, err := netip.ParseAddr(strings.TrimSpace(field))
		if err != nil || addr.Is4() == ipv6 {
			if ipv6 {
				return "", fmt.Errorf("IPv6 地址格式无效: %s", field)
			}
			return "", fmt.Errorf("IPv4 地址格式无效: %s", field)
		}
		value := addr.String()
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return strings.Join(result, "\n"), nil
}

// UpdateVMInterfaceAllowedAddresses 更新指定网卡的端口安全可信地址清单。
func UpdateVMInterfaceAllowedAddresses(vmName string, interfaceOrder int, allowedIPv4, allowedIPv6 string) error {
	var binding model.VPCVMBinding
	if err := model.DB.Where("vm_name = ? AND interface_order = ?", strings.TrimSpace(vmName), interfaceOrder).First(&binding).Error; err != nil {
		return fmt.Errorf("未找到指定的网口绑定")
	}
	var sw model.VPCSwitch
	if err := model.DB.First(&sw, binding.SwitchID).Error; err != nil {
		return fmt.Errorf("交换机不存在")
	}
	req := AddVMInterfaceRequest{AllowedIPv4Addresses: allowedIPv4, AllowedIPv6Addresses: allowedIPv6}
	if err := normalizeInterfacePortSecurityFields(&req, HookSwitchUsesDirectBridge(sw) && sw.IPv6SecurityEnabled); err != nil {
		return err
	}
	binding.AllowedIPv4Addresses = req.AllowedIPv4Addresses
	binding.AllowedIPv6Addresses = req.AllowedIPv6Addresses
	if err := model.DB.Save(&binding).Error; err != nil {
		return fmt.Errorf("保存网卡可信地址失败: %w", err)
	}
	// 直通桥接模式：将允许的 IPv4 地址同步为桥接 dnsmasq 静态绑定，
	// 使 VM 通过 DHCP 获取指定的 IP 而非上级路由器分配的地址
	syncDirectBridgeStaticIPFromAllowed(&sw, &binding)
	if HookTriggerPortSecurityReconcile != nil {
		HookTriggerPortSecurityReconcile()
	}
	return nil
}

func normalizeIPv6PrefixField(value string) (string, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	seen := map[string]bool{}
	var result []string
	for _, field := range fields {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(field))
		if err != nil || prefix.Addr().Is4() {
			return "", fmt.Errorf("IPv6 前缀格式无效: %s", field)
		}
		value := prefix.Masked().String()
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return strings.Join(result, "\n"), nil
}

// syncDirectBridgeStaticIPFromAllowed 直通桥接交换机将允许的 IPv4 首地址同步为桥接 dnsmasq 静态绑定。
// 使 VM 的 DHCP 请求从面板 dnsmasq 获取指定 IP，而非上级路由器自动分配。
// 仅在桥接网桥配置了 DHCP 地址池时生效（预设模式由 binding.go 单独处理）。
func syncDirectBridgeStaticIPFromAllowed(sw *model.VPCSwitch, binding *model.VPCVMBinding) {
	if !HookSwitchUsesDirectBridge(*sw) {
		return
	}
	// 预设模式由 binding.go 的 BindVMToVPC 处理，此处不重复
	if sw.BridgeIPMode == "preset" {
		return
	}
	bridgeName := HookBridgeNameForSwitch(*sw)
	if bridgeName == "" {
		return
	}
	// 检查桥接网桥是否配置了 DHCP 地址池
	var bridge model.NetworkBridge
	if model.DB == nil || model.DB.Where("name = ?", bridgeName).First(&bridge).Error != nil {
		return
	}
	if bridge.DHCPCIDR == "" || bridge.DHCPStart == "" || bridge.DHCPEnd == "" {
		return
	}
	// 获取 VM MAC 地址（优先绑定记录，回退实时查询）
	mac := binding.MACAddress
	if mac == "" {
		mac = ip_resolver.GetFirstVMMAC(binding.VMName)
	}
	if mac == "" {
		return
	}
	// 取第一个允许的 IPv4 地址作为静态绑定
	firstIPv4 := ""
	if binding.AllowedIPv4Addresses != "" {
		for _, line := range strings.FieldsFunc(binding.AllowedIPv4Addresses, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' '
		}) {
			line = strings.TrimSpace(line)
			if line != "" {
				firstIPv4 = line
				break
			}
		}
	}
	if firstIPv4 != "" {
		// 有允许的 IPv4 地址：创建/更新 MAC→IP 静态绑定
		if HookUpsertBridgeStaticHost != nil {
			if err := HookUpsertBridgeStaticHost(bridgeName, binding.VMName, mac, firstIPv4); err != nil {
				logger.App.Warn("直通桥同步静态绑定失败", "vm", binding.VMName, "bridge", bridgeName, "error", err)
			}
		}
	} else {
		// 无允许的 IPv4 地址：清除已有静态绑定，避免残留
		if HookRemoveBridgeStaticHost != nil {
			HookRemoveBridgeStaticHost(bridgeName, binding.VMName, mac)
		}
	}
	// 重载 dnsmasq 使配置生效
	if HookReloadBridgeDNSMasq != nil {
		if err := HookReloadBridgeDNSMasq(bridgeName); err != nil {
			logger.App.Warn("重载桥接 DHCP 服务失败", "bridge", bridgeName, "error", err)
		}
	}
}

func normalizeSwitchPortSecurityFields(req *VPCSwitchRequest, direct bool) error {
	if req == nil {
		return nil
	}
	if !direct && req.IPv6SecurityEnabled {
		return fmt.Errorf("IPv6 端口安全仅适用于直通桥交换机")
	}
	prefixes, err := normalizeIPv6PrefixField(req.TrustedIPv6Prefixes)
	if err != nil {
		return err
	}
	req.TrustedIPv6Prefixes = prefixes
	if req.IPv6SecurityEnabled && config.GlobalConfig != nil && config.GlobalConfig.PortSecurityEnabled && prefixes == "" {
		return fmt.Errorf("启用直通桥 IPv6 防护时需要填写可信 IPv6 前缀")
	}
	if !direct {
		req.IPv6SecurityEnabled = false
		req.TrustedIPv6Prefixes = ""
	}
	return nil
}

func normalizeInterfacePortSecurityFields(req *AddVMInterfaceRequest, ipv6Required bool) error {
	if req == nil {
		return nil
	}
	v4, err := normalizeAddressField(req.AllowedIPv4Addresses, false)
	if err != nil {
		return err
	}
	v6, err := normalizeAddressField(req.AllowedIPv6Addresses, true)
	if err != nil {
		return err
	}
	if ipv6Required && config.GlobalConfig != nil && config.GlobalConfig.PortSecurityEnabled && v6 == "" {
		return fmt.Errorf("该直通桥已启用 IPv6 防护，请填写网卡可信 IPv6 地址")
	}
	req.AllowedIPv4Addresses = v4
	req.AllowedIPv6Addresses = v6
	return nil
}
