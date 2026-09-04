package network

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/ip_resolver"
	"kvm_console/service/libvirt_rpc"
	"kvm_console/utils"
)

// ListStaticIPs 列出静态 IP 绑定
func ListStaticIPs() (*IPListInfo, error) {
	info := &IPListInfo{}

	staticHosts, err := HookListOVSStaticHosts()
	if err != nil {
		return info, fmt.Errorf("读取 OVS 静态绑定失败: %w", err)
	}
	for _, host := range staticHosts {
		info.StaticBindings = append(info.StaticBindings, StaticIPInfo{
			MAC:    host.MAC,
			VMName: host.VMName,
			IP:     host.IP,
		})
	}
	if vpcStaticHosts, vpcErr := HookListAllVPCStaticHosts(); vpcErr == nil {
		for _, host := range vpcStaticHosts {
			info.StaticBindings = append(info.StaticBindings, StaticIPInfo{
				MAC:    host.MAC,
				VMName: host.VMName,
				IP:     host.IP,
			})
		}
	}

	// 构建 MAC -> VM名称 的映射（通过遍历所有虚拟机的网卡）
	macToVMName := make(map[string]string)
	domains, err := libvirt_rpc.ListAllDomainsRPC()
	if err == nil {
		for _, dom := range domains {
			vmName := dom.Name
			if vmName == "" {
				continue
			}
			domXML, xmlErr := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
			if xmlErr != nil {
				continue
			}
			ifaces := libvirt_rpc.ParseInterfacesFromDomainXML(domXML)
			for _, iface := range ifaces {
				if iface.MAC != "" {
					macToVMName[strings.ToLower(iface.MAC)] = vmName
				}
			}
		}
	}

	leases, err := HookListOVSDHCPLeases()
	if err != nil {
		leases = []OVSDHCPLease{}
	}
	if vpcLeases, vpcErr := HookListVPCDHCPLeases(); vpcErr == nil {
		leases = append(leases, vpcLeases...)
	}
	leaseMap := make(map[string]OVSDHCPLease)
	for _, lease := range leases {
		mac := strings.ToLower(lease.MAC)
		leaseMap[mac] = HookNewerOVSDHCPLease(leaseMap[mac], lease)
	}
	for mac, lease := range leaseMap {
		info.DHCPLeases = append(info.DHCPLeases, DHCPLeaseInfo{
			ExpiryTime: lease.ExpiryTime,
			MAC:        lease.MAC,
			IP:         lease.IP,
			Hostname:   lease.Hostname,
			VMName:     macToVMName[mac],
		})
	}

	return info, nil
}

// findFreeIP 自动查找空闲 IP，从 .2 到 .254 按顺序分配
func findFreeIP() (string, error) {
	subnet := HookOvsSubnetPrefix()

	// 收集所有已占用的 IP（静态绑定 + DHCP 租约）
	usedIPs := make(map[int]bool)
	// .1 是网关，始终标记为已占用
	usedIPs[1] = true

	staticHosts, _ := HookListOVSStaticHosts()
	for _, host := range staticHosts {
		parts := strings.Split(host.IP, ".")
		if len(parts) == 4 {
			if lastOctet, err := strconv.Atoi(parts[3]); err == nil {
				usedIPs[lastOctet] = true
			}
		}
	}

	leases, _ := HookListOVSDHCPLeases()
	for _, lease := range leases {
		parts := strings.Split(lease.IP, ".")
		if len(parts) == 4 {
			if lastOctet, err := strconv.Atoi(parts[3]); err == nil {
				usedIPs[lastOctet] = true
			}
		}
	}

	// 从 .2 开始按顺序查找空闲 IP
	for i := 2; i <= 254; i++ {
		if !usedIPs[i] {
			return fmt.Sprintf("%s.%d", subnet, i), nil
		}
	}

	return "", fmt.Errorf("网段 %s.0/24 内没有可用的空闲 IP（2-254 均已占用）", subnet)
}

func findVPCFreeIP(sw model.VPCSwitch) (string, error) {
	if HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(sw) {
		if sw.BridgeIPMode == "preset" {
			return FindBridgeFreeIP(sw)
		}
		return "", fmt.Errorf("上级路由分配模式下无法自动分配 IP，请手动指定 IP 地址")
	}

	start := net.ParseIP(sw.DHCPStart).To4()
	end := net.ParseIP(sw.DHCPEnd).To4()
	if start == nil || end == nil {
		return "", fmt.Errorf("交换机 DHCP 地址池无效")
	}
	used := map[string]bool{sw.GatewayIP: true}
	staticHosts, _ := HookListVPCStaticHosts(sw.ID)
	for _, host := range staticHosts {
		used[host.IP] = true
	}
	leases, _ := HookListVPCDHCPLeasesForSwitch(sw.ID)
	for _, lease := range leases {
		used[lease.IP] = true
	}
	for ip := append(net.IP(nil), start...); compareIPv4(ip, end) <= 0; incrementIPv4(ip) {
		ipText := ip.String()
		if !used[ipText] {
			return ipText, nil
		}
	}
	return "", fmt.Errorf("交换机 %s 的 DHCP 地址池没有可用 IP", sw.Name)
}

func FindBridgeFreeIP(sw model.VPCSwitch) (string, error) {
	bridgeName := sw.BridgeName
	if bridgeName == "" {
		return "", fmt.Errorf("桥接模式下网桥名称不能为空")
	}
	if model.DB == nil {
		return "", fmt.Errorf("数据库不可用")
	}

	var bridge model.NetworkBridge
	// 优先读取 network_bridges 表中网桥的 DHCP 配置；
	// 新建的自动直通桥（qvsw{vlanID}）可能没有对应记录，
	// 此时回退使用交换机自身保存的 CIDR/DHCP 字段，保证预设模式仍能自动分配地址。
	usableBridge := false
	if err := model.DB.Where("name = ?", bridgeName).First(&bridge).Error; err == nil &&
		bridge.DHCPCIDR != "" && bridge.DHCPStart != "" && bridge.DHCPEnd != "" {
		usableBridge = true
	}
	if !usableBridge {
		if sw.BridgeIPMode != "preset" || sw.CIDR == "" || sw.DHCPStart == "" || sw.DHCPEnd == "" {
			return "", fmt.Errorf("桥接网桥 %s 未配置 DHCP 地址池", bridgeName)
		}
		bridge = model.NetworkBridge{
			Name:        bridgeName,
			DHCPCIDR:    sw.CIDR,
			DHCPGateway: sw.GatewayIP,
			DHCPStart:   sw.DHCPStart,
			DHCPEnd:     sw.DHCPEnd,
		}
	}

	start := net.ParseIP(bridge.DHCPStart).To4()
	end := net.ParseIP(bridge.DHCPEnd).To4()
	if start == nil || end == nil {
		return "", fmt.Errorf("网桥 DHCP 地址池无效")
	}

	used := map[string]bool{bridge.DHCPGateway: true}

	if HookListBridgeStaticHosts != nil {
		staticHosts, _ := HookListBridgeStaticHosts(bridge.Name)
		for _, host := range staticHosts {
			if host.IP != "" {
				used[host.IP] = true
			}
		}
	}

	if HookListBridgeDHCPLeases != nil {
		leases, _ := HookListBridgeDHCPLeases(bridge.Name)
		for _, lease := range leases {
			used[lease.IP] = true
		}
	}

	for ip := append(net.IP(nil), start...); compareIPv4(ip, end) <= 0; incrementIPv4(ip) {
		ipText := ip.String()
		if !used[ipText] {
			return ipText, nil
		}
	}
	return "", fmt.Errorf("桥接网桥 %s 的 DHCP 地址池没有可用 IP", bridge.Name)
}

func compareIPv4(a, b net.IP) int {
	for i := 0; i < 4; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func incrementIPv4(ip net.IP) {
	for i := 3; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}

func normalizeIPForVPC(ipAddr string, sw model.VPCSwitch) (string, error) {
	ipAddr = strings.TrimSpace(ipAddr)
	if matched, _ := regexp.MatchString(`^\d+$`, ipAddr); matched {
		parts := strings.Split(sw.GatewayIP, ".")
		if len(parts) == 4 {
			ipAddr = strings.Join(parts[:3], ".") + "." + ipAddr
		}
	}
	ip := net.ParseIP(ipAddr)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("IP 地址格式无效")
	}
	if sw.CIDR != "" {
		if !ipInCIDR(ipAddr, sw.CIDR) {
			return "", fmt.Errorf("IP 地址 %s 不在交换机子网 %s 内", ipAddr, sw.CIDR)
		}
		if ipAddr == sw.GatewayIP {
			return "", fmt.Errorf("IP 地址 %s 是交换机网关，不能绑定", ipAddr)
		}
	}
	parts := strings.Split(ipAddr, ".")
	if len(parts) == 4 && (parts[3] == "0" || parts[3] == "255") {
		return "", fmt.Errorf("IP 地址 %s 不能作为虚拟机地址", ipAddr)
	}
	return ipAddr, nil
}

// UpsertVPCStaticHost 插入或更新 VPC 静态绑定。
// 系统基础网络（VLANID == 0）直接跑在 br-ovs 上，没有独立网关端口与 per-VPC dnsmasq，
// DHCP 由集中式旧版 OVS dnsmasq 提供（/etc/kvm-console/ovs/dhcp-hosts），
// 因此 VLAN0 的静态绑定写入集中式文件；其余 NAT 交换机写入各自的 per-VPC hosts 文件
// 并重载对应 dnsmasq。
func UpsertVPCStaticHost(sw model.VPCSwitch, vmName, mac, ipAddr string) error {
	mac = strings.ToLower(strings.TrimSpace(mac))
	vmName = strings.TrimSpace(vmName)
	ipAddr = strings.TrimSpace(ipAddr)
	if sw.VLANID == 0 {
		if HookUpsertOVSStaticHost == nil {
			return fmt.Errorf("当前环境不支持系统基础网络静态绑定")
		}
		if err := HookUpsertOVSStaticHost(vmName, mac, ipAddr); err != nil {
			return fmt.Errorf("写入系统基础网络静态绑定失败: %w", err)
		}
		// 清理历史版本可能写入 per-VPC 文件的残留条目（VLAN0 下无 dnsmasq 读取该文件，
		// 但会被静态绑定列表扫描展示，残留条目会成为幽灵绑定）
		purgeStaleVPCHostsEntry(sw.ID, vmName, mac)
		return nil
	}
	if err := os.MkdirAll(vpcConfigDir, 0755); err != nil {
		return err
	}
	if _, err := os.Stat(vpcDHCPHostsPath(sw.ID)); os.IsNotExist(err) {
		if err := os.WriteFile(vpcDHCPHostsPath(sw.ID), []byte(""), 0644); err != nil {
			return fmt.Errorf("创建 VPC 静态 DHCP 绑定文件失败: %w", err)
		}
	}
	hosts, err := HookListVPCStaticHosts(sw.ID)
	if err != nil {
		return err
	}
	next, err := HookBuildOVSStaticHostsForUpsert(hosts, OVSStaticHost{VMName: vmName, MAC: mac, IP: ipAddr})
	if err != nil {
		return err
	}
	if err := HookWriteVPCStaticHosts(sw.ID, next); err != nil {
		return fmt.Errorf("写入 VPC 静态 IP 绑定失败: %w", err)
	}
	HookCleanVPCDHCPLease(sw.ID, mac, ipAddr)
	HookCleanOVSDHCPLease(mac, "")
	HookReloadVPCDNSMasq(sw.ID)
	// 清理可能残留的旧版集中式绑定，避免同一 MAC 存在两份 dhcp-host
	if HookRemoveOVSStaticHost != nil {
		_, _ = HookRemoveOVSStaticHost(vmName, mac)
	}
	return nil
}

// RemoveVPCStaticHost 删除 VPC 静态绑定，返回被删除的 IP。
// VLAN0（系统基础网络）的绑定实际存放在集中式旧版 OVS dhcp-hosts 中，需路由到旧版删除。
func RemoveVPCStaticHost(switchID uint, vmName, mac string) (string, error) {
	var sw model.VPCSwitch
	if err := model.DB.First(&sw, switchID).Error; err == nil && sw.VLANID == 0 {
		var removedIP string
		if HookRemoveOVSStaticHost != nil {
			ip, err := HookRemoveOVSStaticHost(vmName, mac)
			if err != nil {
				return "", err
			}
			removedIP = ip
		}
		// 兼容清理 per-VPC 文件中的历史残留
		if ip := purgeStaleVPCHostsEntry(switchID, vmName, mac); ip != "" && removedIP == "" {
			removedIP = ip
		}
		if removedIP == "" {
			return "", fmt.Errorf("该虚拟机没有静态绑定")
		}
		return removedIP, nil
	}
	hosts, err := HookListVPCStaticHosts(switchID)
	if err != nil {
		return "", err
	}
	var removedIP string
	var next []OVSStaticHost
	for _, host := range hosts {
		match := strings.EqualFold(host.MAC, mac) || (vmName != "" && host.VMName == vmName)
		if match {
			removedIP = host.IP
			continue
		}
		next = append(next, host)
	}
	if removedIP == "" {
		return "", fmt.Errorf("该虚拟机没有静态绑定")
	}
	if err := HookWriteVPCStaticHosts(switchID, next); err != nil {
		return "", fmt.Errorf("删除 VPC 静态 IP 绑定失败: %w", err)
	}
	HookReloadVPCDNSMasq(switchID)
	return removedIP, nil
}

// purgeStaleVPCHostsEntry 直接操作 per-VPC 物理 hosts 文件，删除匹配 MAC 或 VM 名称的条目。
// 仅供 VLAN0 路由路径清理历史残留使用（不经过 Hook，避免与 VLAN0 列表路由互相递归）。
func purgeStaleVPCHostsEntry(switchID uint, vmName, mac string) string {
	path := vpcDHCPHostsPath(switchID)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	vmName = strings.TrimSpace(vmName)
	var removedIP string
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(trimmed, ",")
		if len(parts) < 2 {
			kept = append(kept, line)
			continue
		}
		lineMAC := strings.ToLower(strings.TrimSpace(parts[0]))
		lineVM := ""
		lineIP := ""
		for _, part := range parts[1:] {
			part = strings.TrimSpace(part)
			if net.ParseIP(part) != nil {
				lineIP = part
			} else if lineVM == "" {
				lineVM = part
			}
		}
		if (mac != "" && lineMAC == mac) || (vmName != "" && lineVM == vmName) {
			if lineIP != "" {
				removedIP = lineIP
			}
			continue
		}
		kept = append(kept, line)
	}
	if removedIP == "" {
		return ""
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0644); err != nil {
		logger.App.Warn("清理 per-VPC 历史残留静态绑定失败", "switch", switchID, "error", err)
	}
	return removedIP
}

// GetVPCStaticIPByMAC 通过 MAC 查找 VPC 静态绑定的 IP
func GetVPCStaticIPByMAC(switchID uint, mac string) string {
	hosts, err := HookListVPCStaticHosts(switchID)
	if err != nil {
		return ""
	}
	for _, host := range hosts {
		if strings.EqualFold(host.MAC, mac) {
			return host.IP
		}
	}
	return ""
}

// GetVPCStaticHostByVMName 通过 VM 名称查找 VPC 静态绑定
func GetVPCStaticHostByVMName(switchID uint, vmName string) (OVSStaticHost, bool) {
	hosts, err := HookListVPCStaticHosts(switchID)
	if err != nil {
		return OVSStaticHost{}, false
	}
	vmName = strings.TrimSpace(vmName)
	for _, host := range hosts {
		if strings.TrimSpace(host.VMName) == vmName {
			return host, true
		}
	}
	return OVSStaticHost{}, false
}

// EnsureStaticIP 确保虚拟机有静态 IP 绑定，如果没有则自动绑定
// 返回实际的静态 IP 地址
func EnsureStaticIP(vmName string) (string, error) {
	// 获取 MAC 地址
	mac := ip_resolver.GetFirstVMMAC(vmName)
	if mac == "" {
		return "", fmt.Errorf("无法获取虚拟机 %s 的 MAC 地址", vmName)
	}
	if sw, ok := HookGetVPCSwitchForVM(vmName); ok && sw != nil {
		if !sw.IsSystem && !sw.DHCPEnabled {
			return "", fmt.Errorf("二层交换机由外部网络或软路由管理地址，不能配置面板静态 IP")
		}
		if host, ok := GetVPCStaticHostByVMName(sw.ID, vmName); ok {
			if !strings.EqualFold(host.MAC, mac) {
				if err := UpsertVPCStaticHost(*sw, vmName, mac, host.IP); err != nil {
					return "", fmt.Errorf("同步 VPC 静态 IP 绑定到当前 MAC 失败: %w", err)
				}
			}
			return host.IP, nil
		}
		if ip := GetVPCStaticIPByMAC(sw.ID, mac); ip != "" {
			return ip, nil
		}
		if ip := HookGetVPCLeaseIPForVM(vmName); ip != "" {
			if err := UpsertVPCStaticHost(*sw, vmName, mac, ip); err != nil {
				return "", fmt.Errorf("固定当前 VPC DHCP 地址失败: %w", err)
			}
			return ip, nil
		}
		if ip := ip_resolver.GetHostNeighborIPByMAC(mac, sw.CIDR, true); ip != "" {
			if err := UpsertVPCStaticHost(*sw, vmName, mac, ip); err != nil {
				return "", fmt.Errorf("固定当前 VPC 邻居表地址失败: %w", err)
			}
			return ip, nil
		}
		return BindStaticIP(vmName, "")
	}

	// 如果同一 VM 曾绑定过静态 IP，但用户修改了 MAC，则保留原 IP 并迁移到当前 MAC。
	if host, ok := HookGetOVSStaticHostByVMName(vmName); ok {
		if !strings.EqualFold(host.MAC, mac) {
			if err := HookUpsertOVSStaticHost(vmName, mac, host.IP); err != nil {
				return "", fmt.Errorf("同步静态 IP 绑定到当前 MAC 失败: %w", err)
			}
			refreshNIC(vmName, mac, "")
		}
		return host.IP, nil
	}

	// 检查当前 MAC 是否已有静态绑定
	if ip := HookGetOVSStaticIPByMAC(mac); ip != "" {
		return ip, nil
	}

	// 没有静态绑定，自动绑定（IP 留空表示自动分配）
	return BindStaticIP(vmName, "")
}

// ResolvePortForwardTargetIP 解析端口转发目标 IP。
// VPC VM 始终以后端当前静态绑定或最新 DHCP 租约为准，避免前端缓存旧 IP 导致 DNAT 指向失效地址。
func ResolvePortForwardTargetIP(vmName, requestedIP string) (string, error) {
	vmName = strings.TrimSpace(vmName)
	requestedIP = strings.TrimSpace(requestedIP)
	if vmName == "" {
		if requestedIP == "" {
			return "", fmt.Errorf("虚拟机名称或目标 IP 不能为空")
		}
		return requestedIP, nil
	}
	if sw, ok := HookGetVPCSwitchForVM(vmName); ok && sw != nil {
		if !sw.IsSystem && !sw.DHCPEnabled {
			return "", fmt.Errorf("二层交换机不提供内置 NAT，不能创建端口转发")
		}
		mac := ip_resolver.GetFirstVMMAC(vmName)
		if mac == "" {
			return "", fmt.Errorf("无法获取虚拟机 %s 的 MAC 地址", vmName)
		}
		if host, ok := GetVPCStaticHostByVMName(sw.ID, vmName); ok {
			if !strings.EqualFold(host.MAC, mac) {
				if err := UpsertVPCStaticHost(*sw, vmName, mac, host.IP); err != nil {
					return "", fmt.Errorf("同步 VPC 静态 IP 绑定到当前 MAC 失败: %w", err)
				}
			}
			return host.IP, nil
		}
		if ip := HookGetVPCLeaseIPForVM(vmName); ip != "" {
			if err := UpsertVPCStaticHost(*sw, vmName, mac, ip); err != nil {
				return "", fmt.Errorf("固定当前 VPC DHCP 地址失败: %w", err)
			}
			return ip, nil
		}
		if ip := ip_resolver.GetHostNeighborIPByMAC(mac, sw.CIDR, true); ip != "" {
			if err := UpsertVPCStaticHost(*sw, vmName, mac, ip); err != nil {
				return "", fmt.Errorf("固定当前 VPC 邻居表地址失败: %w", err)
			}
			return ip, nil
		}
		if requestedIP != "" {
			normalized, err := normalizeIPForVPC(requestedIP, *sw)
			if err != nil {
				return "", err
			}
			if err := UpsertVPCStaticHost(*sw, vmName, mac, normalized); err != nil {
				return "", fmt.Errorf("绑定 VPC 静态 IP 失败: %w", err)
			}
			return normalized, nil
		}
		return BindStaticIP(vmName, "")
	}
	if requestedIP != "" {
		return requestedIP, nil
	}
	return EnsureStaticIP(vmName)
}

// directBridgeDHCPPool 返回直通桥网桥的 DHCP 地址池信息（优先网桥配置，回退交换机预设字段）。
func directBridgeDHCPPool(sw model.VPCSwitch) (cidr, start, end string, ok bool) {
	if sw.BridgeName == "" {
		return "", "", "", false
	}
	var bridge model.NetworkBridge
	if model.DB != nil && model.DB.Where("name = ?", sw.BridgeName).First(&bridge).Error == nil &&
		bridge.DHCPCIDR != "" && bridge.DHCPStart != "" && bridge.DHCPEnd != "" {
		return bridge.DHCPCIDR, bridge.DHCPStart, bridge.DHCPEnd, true
	}
	if sw.BridgeIPMode == "preset" && sw.CIDR != "" && sw.DHCPStart != "" && sw.DHCPEnd != "" {
		return sw.CIDR, sw.DHCPStart, sw.DHCPEnd, true
	}
	return "", "", "", false
}

// ValidateStaticIPv4ForSwitch 校验指定 IP 能否绑定到该交换机（创建/克隆前同步校验用）。
func ValidateStaticIPv4ForSwitch(sw *model.VPCSwitch, ipAddr string) error {
	ipAddr = strings.TrimSpace(ipAddr)
	if ipAddr == "" {
		return nil
	}
	if sw == nil {
		return fmt.Errorf("交换机不存在")
	}
	ip := net.ParseIP(ipAddr)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("指定的 IPv4 地址 %s 无效", ipAddr)
	}
	if HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(*sw) {
		// 直通桥：仅当网桥配置了 DHCP 地址池（预设模式或上级路由+桥接池）时支持指定 IP
		cidr, start, end, ok := directBridgeDHCPPool(*sw)
		if !ok {
			return fmt.Errorf("该直通桥交换机未配置 DHCP 地址池，无法指定 IP")
		}
		if cidr != "" && !ipInCIDR(ipAddr, cidr) {
			return fmt.Errorf("指定的 IPv4 地址 %s 不在网段 %s 内", ipAddr, cidr)
		}
		s := net.ParseIP(start).To4()
		e := net.ParseIP(end).To4()
		v := ip.To4()
		if s != nil && e != nil && v != nil && (compareIPv4(v, s) < 0 || compareIPv4(v, e) > 0) {
			return fmt.Errorf("指定的 IPv4 地址 %s 不在 DHCP 地址池 %s-%s 内", ipAddr, start, end)
		}
		return nil
	}
	if !sw.IsSystem && !sw.DHCPEnabled {
		return fmt.Errorf("该交换机由外部网络管理地址，无法指定 IP")
	}
	if _, err := normalizeIPForVPC(ipAddr, *sw); err != nil {
		return err
	}
	return nil
}

// BindVMInterfaceStaticIP 为虚拟机指定网口绑定 DHCP 静态 IP。
// 创建/克隆流程在虚拟机启动前调用，开机后虚拟机即可通过 DHCP 获取该地址；
// 运行中网口调用时写入绑定，待续租/重启后生效。
func BindVMInterfaceStaticIP(vmName string, interfaceOrder int, ipAddr string) error {
	vmName = strings.TrimSpace(vmName)
	ipAddr = strings.TrimSpace(ipAddr)
	if vmName == "" || ipAddr == "" {
		return nil
	}
	// 获取网口 MAC（支持关机状态从 XML 读取）
	mac := ""
	if interfaceOrder <= 0 {
		mac = ip_resolver.GetFirstVMMAC(vmName)
	} else if HookGetVMMACByOrder != nil {
		mac = HookGetVMMACByOrder(vmName, interfaceOrder)
	}
	if mac == "" {
		return fmt.Errorf("无法获取虚拟机 %s 网口 %d 的 MAC 地址", vmName, interfaceOrder)
	}
	// 查找该网口绑定的交换机
	var binding model.VPCVMBinding
	if err := model.DB.Where("vm_name = ? AND interface_order = ?", vmName, interfaceOrder).First(&binding).Error; err != nil {
		return fmt.Errorf("未找到网口绑定记录，无法指定 IP")
	}
	var sw model.VPCSwitch
	if err := model.DB.First(&sw, binding.SwitchID).Error; err != nil {
		return fmt.Errorf("交换机不存在，无法指定 IP")
	}
	if err := ValidateStaticIPv4ForSwitch(&sw, ipAddr); err != nil {
		return err
	}
	if HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(sw) {
		// 直通桥：写入桥接 dnsmasq 静态绑定（覆盖自动分配的地址）
		if HookUpsertBridgeStaticHost == nil {
			return fmt.Errorf("桥接静态绑定能力不可用")
		}
		if err := HookUpsertBridgeStaticHost(sw.BridgeName, vmName, mac, ipAddr); err != nil {
			return fmt.Errorf("注册桥接静态绑定失败: %w", err)
		}
		if HookReloadBridgeDNSMasq != nil {
			if err := HookReloadBridgeDNSMasq(sw.BridgeName); err != nil {
				logger.App.Warn("重载桥接 DHCP 服务失败", "bridge", sw.BridgeName, "error", err)
			}
		}
		// 同步写入 vm_network_infos.IPAddress，使列表接口优先从数据库读到正确 IP
		upsertVMNetworkInfoStaticIP(vmName, interfaceOrder, mac, ipAddr, sw)
		return nil
	}
	// NAT/系统交换机：写入 dnsmasq 静态绑定（UpsertVPCStaticHost 内部按 VLAN 路由：
	// VLAN0 系统基础网络写集中式旧版 OVS dhcp-hosts，VLAN>0 写 per-VPC hosts 文件）
	normalized, err := normalizeIPForVPC(ipAddr, sw)
	if err != nil {
		return err
	}
	if err := UpsertVPCStaticHost(sw, vmName, mac, normalized); err != nil {
		return fmt.Errorf("绑定静态 IP 失败: %w", err)
	}
	// 同步写入 vm_network_infos.IPAddress，使列表接口优先从数据库读到正确 IP
	upsertVMNetworkInfoStaticIP(vmName, interfaceOrder, mac, ipAddr, sw)
	return nil
}

// upsertVMNetworkInfoStaticIP 将用户指定的静态 IP 同步写入 vm_network_infos 表
// 通过 MAC 地址匹配已有记录并更新 IP，若不存在则创建新记录
// 这样列表接口可以优先从数据库读到正确 IP，避免每次走实时查询受 ARP 影响
func upsertVMNetworkInfoStaticIP(vmName string, interfaceOrder int, mac, ipAddr string, sw model.VPCSwitch) {
	if model.DB == nil {
		return
	}
	vmName = strings.TrimSpace(vmName)
	mac = strings.ToLower(strings.TrimSpace(mac))
	ipAddr = strings.TrimSpace(ipAddr)
	if vmName == "" || mac == "" || ipAddr == "" {
		return
	}
	var existing model.VMNetworkInfo
	err := model.DB.Where("vm_name = ? AND interface_order = ? AND is_deleted = ?", vmName, interfaceOrder, false).
		First(&existing).Error
	if err == nil {
		// 更新已有记录的 IP 地址
		existing.IPAddress = ipAddr
		existing.MacAddress = mac
		if existing.SwitchName == "" {
			existing.SwitchName = sw.Name
		}
		if existing.BridgeName == "" && HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(sw) {
			existing.BridgeName = sw.BridgeName
		}
		if saveErr := model.DB.Save(&existing).Error; saveErr != nil {
			logger.App.Warn("同步静态 IP 到 vm_network_infos 失败", "vm", vmName, "error", saveErr)
		}
		return
	}
	// 创建新记录
	info := model.VMNetworkInfo{
		VMName:         vmName,
		InterfaceOrder: interfaceOrder,
		IPAddress:      ipAddr,
		MacAddress:     mac,
		SwitchName:     sw.Name,
		IsDeleted:      false,
	}
	if HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(sw) {
		info.BridgeName = sw.BridgeName
		info.NetworkType = "bridge"
	}
	if createErr := model.DB.Create(&info).Error; createErr != nil {
		logger.App.Warn("创建 vm_network_infos 记录失败", "vm", vmName, "error", createErr)
	}
}

// BindStaticIP 绑定静态 IP，ipAddr 为空时自动分配空闲 IP
// 返回实际绑定的 IP 地址
func BindStaticIP(vmName, ipAddr string) (string, error) {
	// 获取 MAC 地址
	mac := ip_resolver.GetFirstVMMAC(vmName)
	if mac == "" {
		return "", fmt.Errorf("无法获取虚拟机 %s 的 MAC 地址", vmName)
	}
	if sw, ok := HookGetVPCSwitchForVM(vmName); ok && sw != nil {
		if !sw.IsSystem && !sw.DHCPEnabled {
			return "", fmt.Errorf("二层交换机由外部网络或软路由管理地址，不能配置面板静态 IP")
		}
		if ipAddr == "" {
			freeIP, err := findVPCFreeIP(*sw)
			if err != nil {
				return "", err
			}
			ipAddr = freeIP
		} else {
			normalized, err := normalizeIPForVPC(ipAddr, *sw)
			if err != nil {
				return "", err
			}
			ipAddr = normalized
		}
		if err := UpsertVPCStaticHost(*sw, vmName, mac, ipAddr); err != nil {
			return "", fmt.Errorf("绑定 VPC 静态 IP 失败: %w", err)
		}
		go refreshNIC(vmName, mac, "")
		return ipAddr, nil
	}

	// IP 为空时自动分配
	if ipAddr == "" {
		freeIP, err := findFreeIP()
		if err != nil {
			return "", err
		}
		ipAddr = freeIP
	} else {
		ipAddr = HookNormalizeIPForOVS(ipAddr)
	}

	// 执行绑定
	if err := HookUpsertOVSStaticHost(vmName, mac, ipAddr); err != nil {
		return "", fmt.Errorf("绑定失败: %w", err)
	}

	// 如果虚拟机正在运行，拔插网卡以强制刷新 DHCP，确保使用新 IP
	refreshNIC(vmName, mac, "")

	return ipAddr, nil
}

// refreshNIC 拔插网卡以强制刷新 DHCP（仅运行中的虚拟机）
func refreshNIC(vmName, mac, network string) {
	state, err := libvirt_rpc.GetDomainStateRPC(vmName)
	if err != nil || state != "running" {
		return
	}

	// 获取网卡模型
	nicModel := libvirt_rpc.GetFirstVMInterfaceModelFromXML(vmName)

	if HookUseOVSNetwork() {
		var ifaceXML string
		if sw, ok := HookGetVPCSwitchForVM(vmName); ok && sw != nil {
			ifaceXML = HookBuildOVSInterfaceXMLWithVLAN(mac, nicModel, sw.VLANID)
			if err := libvirt_rpc.DetachDeviceFlagsRPC(vmName, ifaceXML, 1); err == nil { // VIR_DOMAIN_DEVICE_MODIFY_LIVE
				time.Sleep(1 * time.Second)
				if err := libvirt_rpc.AttachDeviceFlagsRPC(vmName, ifaceXML, 1); err == nil {
					_ = applyVPCSwitchRuntime(vmName, *sw)
				}
			}
			return
		}
		ifaceXML = HookBuildOVSInterfaceXML(mac, nicModel)
		if err := libvirt_rpc.DetachDeviceFlagsRPC(vmName, ifaceXML, 1); err == nil { // VIR_DOMAIN_DEVICE_MODIFY_LIVE
			time.Sleep(1 * time.Second)
			libvirt_rpc.AttachDeviceFlagsRPC(vmName, ifaceXML, 1)
		}
		return
	}

	// 非 OVS 环境：通过 detach/attach XML 方式拔插网卡
	detachXML := fmt.Sprintf("<interface type='network'>\n"+
		"  <mac address='%s'/>\n"+
		"  <source network='%s'/>\n"+
		"  <model type='%s'/>\n"+
		"</interface>", mac, network, nicModel)
	if err := libvirt_rpc.DetachDeviceFlagsRPC(vmName, detachXML, 1); err == nil { // VIR_DOMAIN_DEVICE_MODIFY_LIVE
		time.Sleep(1 * time.Second)
		libvirt_rpc.AttachDeviceFlagsRPC(vmName, detachXML, 1)
	}
}

// UnbindStaticIP 解绑静态 IP
func UnbindStaticIP(vmName string) error {
	// 获取 MAC
	mac := ip_resolver.GetFirstVMMAC(vmName)
	if mac == "" {
		return fmt.Errorf("无法获取 MAC 地址")
	}

	if sw, ok := HookGetVPCSwitchForVM(vmName); ok && sw != nil {
		if HookSwitchUsesDirectBridge != nil && HookSwitchUsesDirectBridge(*sw) {
			bridgeName := sw.BridgeName
			if bridgeName != "" {
				if HookRemoveBridgeStaticHost != nil {
					boundIP, err := HookRemoveBridgeStaticHost(bridgeName, vmName, mac)
					if err != nil {
						return err
					}
					if boundIP != "" {
						RemovePortForwardsForIP(boundIP)
					}
				}
				if HookRemoveBridgeDHCPLease != nil {
					if _, err := HookRemoveBridgeDHCPLease(bridgeName, vmName, mac); err != nil {
						logger.App.Warn("清理桥接网桥 DHCP 租约失败", "bridge", bridgeName, "vm", vmName, "error", err)
					}
				}
				if HookReloadBridgeDNSMasq != nil && sw.BridgeIPMode == "preset" {
					if err := HookReloadBridgeDNSMasq(bridgeName); err != nil {
						logger.App.Warn("重载桥接网桥 DNSMasq 失败", "bridge", bridgeName, "error", err)
					}
				}
				return nil
			}
		}

		// 系统基础网络（VLAN0）的解绑也由 RemoveVPCStaticHost 内部路由到集中式旧版文件
		boundIP, err := RemoveVPCStaticHost(sw.ID, vmName, mac)
		if err != nil {
			return err
		}
		if boundIP != "" {
			RemovePortForwardsForIP(boundIP)
		}
		go refreshNIC(vmName, mac, "")
		return nil
	}

	boundIP, err := HookRemoveOVSStaticHost(vmName, mac)
	if err != nil {
		return err
	}

	if boundIP != "" {
		RemovePortForwardsForIP(boundIP)
	}

	refreshNIC(vmName, mac, "")

	return nil
}

// RemovePortForwardsForIP 删除所有指向指定 IP 的端口转发规则
func RemovePortForwardsForIP(targetIP string) {
	// 获取所有 DNAT 规则及行号
	result := utils.ExecShellQuiet("iptables -t nat -L PREROUTING -n --line-numbers 2>/dev/null | grep DNAT")
	if result.Error != nil || result.Stdout == "" {
		return
	}

	// 收集需要删除的规则行号（倒序删除避免偏移）
	var ruleIDs []int
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, targetIP) {
			continue
		}
		// 检查 to:targetIP: 格式确保精确匹配
		if !strings.Contains(line, "to:"+targetIP+":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			var id int
			fmt.Sscanf(fields[0], "%d", &id)
			if id > 0 {
				ruleIDs = append(ruleIDs, id)
			}
		}
	}

	// 倒序删除（从大到小，避免行号偏移）
	for i := len(ruleIDs) - 1; i >= 0; i-- {
		id := ruleIDs[i]
		// 获取规则信息用于清理 FORWARD 和 UFW
		ruleInfo := utils.ExecShell(fmt.Sprintf("iptables -t nat -L PREROUTING %d -n 2>/dev/null", id))

		// 解析协议
		protoRe := regexp.MustCompile(`\s+(tcp|udp|6|17)\s+`)
		proto := "tcp"
		if m := protoRe.FindStringSubmatch(ruleInfo.Stdout); len(m) > 1 {
			switch m[1] {
			case "6":
				proto = "tcp"
			case "17":
				proto = "udp"
			default:
				proto = m[1]
			}
		}

		// 解析宿主机端口
		dportRe := regexp.MustCompile(`dpts?:(\S+)`)
		hostPort := ""
		if m := dportRe.FindStringSubmatch(ruleInfo.Stdout); len(m) > 1 {
			hostPort = m[1]
		}

		// 解析目标端口
		destRe := regexp.MustCompile(`to:(\S+)`)
		destPort := ""
		if m := destRe.FindStringSubmatch(ruleInfo.Stdout); len(m) > 1 {
			parts := strings.SplitN(m[1], ":", 2)
			if len(parts) > 1 {
				destPort = parts[1]
			}
		}

		// 删除 NAT 规则 (PREROUTING)
		utils.ExecShell(fmt.Sprintf("iptables -t nat -D PREROUTING %d", id))

		// 删除 NAT 规则 (OUTPUT - 本地流量 DNAT)
		if hostPort != "" {
			utils.ExecShell(fmt.Sprintf(
				"iptables -t nat -D OUTPUT -d %s -p %s --dport %s -j DNAT --to-destination %s:%s 2>/dev/null",
				utils.ShellSingleQuote(getHostIP()), utils.ShellSingleQuote(proto), utils.ShellSingleQuote(hostPort), utils.ShellSingleQuote(targetIP), utils.ShellSingleQuote(destPort)))
		}

		// 删除 FORWARD 规则
		if destPort != "" {
			utils.ExecShell(fmt.Sprintf(
				"iptables -D FORWARD -d %s -p %s --dport %s -j ACCEPT 2>/dev/null",
				utils.ShellSingleQuote(targetIP), utils.ShellSingleQuote(proto), utils.ShellSingleQuote(destPort)))
		}

		// 删除 UFW 规则
		if hostPort != "" {
			_ = HookDeleteHostFirewallPortForwardRule(hostPort, proto)
		}
	}

	// 如果有删除规则，自动持久化
	if len(ruleIDs) > 0 {
		go SavePortForwardRules()
	}
}
