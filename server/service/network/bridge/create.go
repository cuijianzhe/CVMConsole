package bridge

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"kvm_console/config"
	"kvm_console/model"
	"kvm_console/utils"
)

func init() {
	HookStartBridgeDNSMasq = startBridgeDNSMasq
}

func CreateNetworkBridge(req NetworkBridgeRequest) (*model.NetworkBridge, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Mode = NormalizeBridgeMode(req.Mode)
	req.UplinkIF = strings.TrimSpace(req.UplinkIF)
	if req.Mode != BridgeModeDirect {
		return nil, fmt.Errorf("当前仅允许创建桥接直通网桥")
	}
	if err := validateBridgeName(req.Name); err != nil {
		return nil, err
	}
	if HookOvsBridgeName != nil && req.Name == HookOvsBridgeName() {
		return nil, fmt.Errorf("默认 OVS 内网网桥已存在，不能重复创建")
	}
	if req.UplinkIF == "" {
		return nil, fmt.Errorf("请选择物理网卡")
	}
	if err := validateBridgeUplink(req.UplinkIF, req.Name); err != nil {
		return nil, err
	}
	if err := validateBridgeDHCPConfig(req); err != nil {
		return nil, err
	}
	if model.DB != nil {
		var count int64
		model.DB.Model(&model.NetworkBridge{}).Where("name = ?", req.Name).Count(&count)
		if count > 0 {
			return nil, fmt.Errorf("网桥名称已存在")
		}
	}
	var ipCfg HostIPConfig
	if req.MigrateHostIP {
		ipCfg = CaptureInterfaceIPv4(req.UplinkIF)
	}
	if err := EnsureOVSBridgeDirect(req.Name, req.UplinkIF, req.MigrateHostIP, ipCfg); err != nil {
		return nil, err
	}
	row := &model.NetworkBridge{
		Name: req.Name, Mode: BridgeModeDirect, UplinkIF: req.UplinkIF,
		MigrateHostIP: req.MigrateHostIP,
		HostAddrs:     ipCfg.Addrs, HostGateway: ipCfg.Gateway, HostMetric: ipCfg.Metric, HostDNS: ipCfg.DNS,
		DHCPCIDR: req.DHCPCIDR, DHCPStart: req.DHCPStart, DHCPEnd: req.DHCPEnd,
		DHCPGateway: req.DHCPGateway, DHCPDNS: req.DHCPDNS,
	}
	if model.DB != nil {
		if err := model.DB.Create(row).Error; err != nil {
			return nil, fmt.Errorf("保存网桥配置失败: %w", err)
		}
		if HookEnsureOVSNetworkReady != nil {
			if err := HookEnsureOVSNetworkReady(); err != nil {
				return nil, fmt.Errorf("网桥已创建，但恢复默认 OVS 网络失败: %w", err)
			}
		}
		if HookEnsureAllVPCSwitchRuntime != nil {
			if err := HookEnsureAllVPCSwitchRuntime(); err != nil {
				return nil, fmt.Errorf("网桥已创建，但恢复 VPC 交换机网络失败: %w", err)
			}
		}
	}
	if row.DHCPCIDR != "" {
		if err := startBridgeDNSMasq(*row); err != nil {
			return row, fmt.Errorf("网桥已创建，但启动 DHCP 服务失败: %w", err)
		}
	}
	return row, nil
}

func EnsureOVSBridgeDirect(bridge, uplink string, migrateHostIP bool, cfg HostIPConfig) error {
	if result := utils.ExecCommand("bash", "-c", "command -v ovs-vsctl"); result.Error != nil {
		return fmt.Errorf("OVS 未安装，请先安装 openvswitch-switch")
	}
	bridge = strings.TrimSpace(bridge)
	uplink = strings.TrimSpace(uplink)
	if err := os.MkdirAll(bridgeConfigDir, 0755); err != nil {
		return fmt.Errorf("创建网桥配置目录失败: %w", err)
	}
	if result := utils.ExecCommand("ovs-vsctl", "--may-exist", "add-br", bridge); result.Error != nil {
		return fmt.Errorf("创建桥接网桥失败: %s", result.Stderr)
	}
	utils.ExecCommand("ip", "link", "set", bridge, "up")
	if result := utils.ExecCommand("ovs-vsctl", "--may-exist", "add-port", bridge, uplink); result.Error != nil {
		return fmt.Errorf("添加物理网卡到桥接网桥失败: %s", result.Stderr)
	}
	utils.ExecCommand("ip", "link", "set", uplink, "up")
	// IP 迁移逻辑
	if migrateHostIP {
		// 检查网桥是否已有 IP（重启恢复场景：systemd 服务已应用了静态 IP）
		bridgeCfg := CaptureInterfaceIPv4(bridge)
		bridgeHasIP := strings.TrimSpace(bridgeCfg.Addrs) != ""
		if !bridgeHasIP {
			// 网桥没有 IP，尝试从物理口迁移或使用存储值
			uplinkCfg := CaptureInterfaceIPv4(uplink)
			if strings.TrimSpace(uplinkCfg.Addrs) != "" {
				// 物理口有 IP，执行动态迁移
				migrateInterfaceIPv4ToBridge(uplink, bridge)
			} else if strings.TrimSpace(cfg.Addrs) != "" {
				// 物理口也没 IP，使用存储的静态配置恢复
				applyStaticIPv4ToBridge(bridge, cfg)
			}
		}
		// DNS 总是需要确保配置正确（重启恢复场景下即使网桥已有 IP，DNS 也可能丢失）
		ensureBridgeResolvedDNSWithStatic(uplink, bridge, cfg.DNS)
		// 如果 cfg 为空但网桥已有 IP，更新 cfg 用于写入脚本
		if strings.TrimSpace(cfg.Addrs) == "" {
			cfg = CaptureInterfaceIPv4(bridge)
			// 同时保留已有的 DNS 信息
			if cfg.DNS == "" {
				cfg.DNS = captureInterfaceDNSServers(bridge)
			}
		}
		// 兼容旧记录：IP 已存储但 DNS 未存储，从网桥当前状态捕获 DNS
		if strings.TrimSpace(cfg.DNS) == "" {
			cfg.DNS = captureInterfaceDNSServers(bridge)
			// 网桥也没有则回退到 uplink
			if cfg.DNS == "" {
				cfg.DNS = captureInterfaceDNSServers(uplink)
			}
		}
	}
	// IP 已迁移完成后再禁用 networkd DHCP，避免周期性 DHCP Discover 干扰 OVS 数据通道
	disableNetworkdDHCPForPort(uplink)
	if err := writeBridgeRestoreScript(bridge, uplink, migrateHostIP, cfg); err != nil {
		return err
	}
	if err := writeBridgeRestoreUnit(); err != nil {
		return err
	}
	return nil
}

func validateBridgeName(name string) error {
	if name == "" {
		return fmt.Errorf("网桥名称不能为空")
	}
	if len(name) > 15 {
		return fmt.Errorf("网桥名称不能超过 15 个字符")
	}
	if ok, _ := regexp.MatchString(`^[A-Za-z0-9_.-]+$`, name); !ok {
		return fmt.Errorf("网桥名称只能包含字母、数字、点、下划线和短横线")
	}
	return nil
}

func validateBridgeUplink(uplink, targetBridge string) error {
	if !isPhysicalInterface(uplink) {
		return fmt.Errorf("请选择真实物理网卡")
	}
	ports := readOVSPortBridgeMap()
	if bridge := ports[uplink]; bridge != "" && bridge != targetBridge {
		return fmt.Errorf("物理网卡 %s 已接入 OVS 网桥 %s", uplink, bridge)
	}
	if model.DB != nil {
		var count int64
		model.DB.Model(&model.NetworkBridge{}).Where("uplink_if = ?", uplink).Count(&count)
		if count > 0 {
			return fmt.Errorf("物理网卡 %s 已被其它桥接网桥使用", uplink)
		}
	}
	return nil
}

func ovsBridgeExists(name string) bool {
	return utils.ExecCommand("ovs-vsctl", "br-exists", strings.TrimSpace(name)).Error == nil
}

func linkIsUp(name string) bool {
	result := utils.ExecCommand("ip", "-j", "link", "show", "dev", strings.TrimSpace(name))
	return result.Error == nil && strings.Contains(strings.ToUpper(result.Stdout), "UP")
}

func validateBridgeDHCPConfig(req NetworkBridgeRequest) error {
	req.DHCPCIDR = strings.TrimSpace(req.DHCPCIDR)
	req.DHCPStart = strings.TrimSpace(req.DHCPStart)
	req.DHCPEnd = strings.TrimSpace(req.DHCPEnd)
	req.DHCPGateway = strings.TrimSpace(req.DHCPGateway)
	req.DHCPDNS = strings.TrimSpace(req.DHCPDNS)

	if req.DHCPCIDR == "" {
		if req.DHCPStart != "" || req.DHCPEnd != "" || req.DHCPGateway != "" || req.DHCPDNS != "" {
			return fmt.Errorf("配置 DHCP 段时必须填写网段 CIDR")
		}
		return nil
	}

	prefix, err := netip.ParsePrefix(req.DHCPCIDR)
	if err != nil {
		return fmt.Errorf("网段 CIDR 格式无效: %s", req.DHCPCIDR)
	}
	if !prefix.Addr().Is4() {
		return fmt.Errorf("仅支持 IPv4 网段")
	}
	if prefix.Bits() < 16 {
		return fmt.Errorf("子网掩码位不能小于 16")
	}
	if prefix.Bits() > 30 {
		return fmt.Errorf("子网掩码位不能大于 30，至少需要 4 个可用 IP")
	}

	if req.DHCPGateway == "" {
		addr := prefix.Addr().Next()
		if !prefix.Contains(addr) {
			return fmt.Errorf("无法自动计算网关地址")
		}
		req.DHCPGateway = addr.String()
	} else {
		gatewayAddr, err := netip.ParseAddr(req.DHCPGateway)
		if err != nil {
			return fmt.Errorf("网关地址格式无效: %s", req.DHCPGateway)
		}
		if !prefix.Contains(gatewayAddr) {
			return fmt.Errorf("网关地址 %s 不在网段 %s 内", req.DHCPGateway, req.DHCPCIDR)
		}
		if gatewayAddr == prefix.Addr() || gatewayAddr == broadcastAddr(prefix) {
			return fmt.Errorf("网关地址不能是网络地址或广播地址")
		}
	}

	if req.DHCPStart == "" {
		start := netip.MustParseAddr(req.DHCPGateway).Next()
		if prefix.Contains(start) && start != broadcastAddr(prefix) {
			req.DHCPStart = start.String()
		} else {
			return fmt.Errorf("无法自动计算 DHCP 起始地址")
		}
	} else {
		startAddr, err := netip.ParseAddr(req.DHCPStart)
		if err != nil {
			return fmt.Errorf("DHCP 起始地址格式无效: %s", req.DHCPStart)
		}
		if !prefix.Contains(startAddr) {
			return fmt.Errorf("DHCP 起始地址 %s 不在网段 %s 内", req.DHCPStart, req.DHCPCIDR)
		}
		if startAddr == prefix.Addr() || startAddr == broadcastAddr(prefix) {
			return fmt.Errorf("DHCP 起始地址不能是网络地址或广播地址")
		}
	}

	if req.DHCPEnd == "" {
		bcast := broadcastAddr(prefix)
		end := bcast.Prev()
		if prefix.Contains(end) && end != prefix.Addr() {
			req.DHCPEnd = end.String()
		} else {
			return fmt.Errorf("无法自动计算 DHCP 结束地址")
		}
	} else {
		endAddr, err := netip.ParseAddr(req.DHCPEnd)
		if err != nil {
			return fmt.Errorf("DHCP 结束地址格式无效: %s", req.DHCPEnd)
		}
		if !prefix.Contains(endAddr) {
			return fmt.Errorf("DHCP 结束地址 %s 不在网段 %s 内", req.DHCPEnd, req.DHCPCIDR)
		}
		if endAddr == prefix.Addr() || endAddr == broadcastAddr(prefix) {
			return fmt.Errorf("DHCP 结束地址不能是网络地址或广播地址")
		}
	}

	startAddr := netip.MustParseAddr(req.DHCPStart)
	endAddr := netip.MustParseAddr(req.DHCPEnd)
	if compareAddr(startAddr, endAddr) > 0 {
		return fmt.Errorf("DHCP 起始地址不能大于结束地址")
	}

	return nil
}

func broadcastAddr(prefix netip.Prefix) netip.Addr {
	addr := prefix.Addr().As4()
	bits := prefix.Bits()
	mask := net.CIDRMask(bits, 32)
	for i := 0; i < 4; i++ {
		addr[i] |= ^mask[i]
	}
	return netip.AddrFrom4(addr)
}

func compareAddr(a, b netip.Addr) int {
	a4 := a.As4()
	b4 := b.As4()
	for i := 0; i < 4; i++ {
		if a4[i] < b4[i] {
			return -1
		}
		if a4[i] > b4[i] {
			return 1
		}
	}
	return 0
}

func startBridgeDNSMasq(bridge model.NetworkBridge) error {
	if bridge.DHCPCIDR == "" || bridge.DHCPStart == "" || bridge.DHCPEnd == "" {
		return nil
	}
	if err := os.MkdirAll(bridgeConfigDir, 0755); err != nil {
		return fmt.Errorf("创建网桥配置目录失败: %w", err)
	}

	dns := bridge.DHCPDNS
	if dns == "" {
		dns = config.GlobalConfig.VPCDNS
	}
	if dns == "" {
		dns = "223.5.5.5 119.29.29.29"
	}

	configPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dnsmasq-%s.conf", bridge.Name))
	hostsPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dhcp-hosts-%s", bridge.Name))
	leasesPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("leases-%s", bridge.Name))
	pidPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dnsmasq-%s.pid", bridge.Name))

	if _, err := os.Stat(hostsPath); os.IsNotExist(err) {
		if err := os.WriteFile(hostsPath, []byte(""), 0644); err != nil {
			return fmt.Errorf("创建 DHCP 静态绑定文件失败: %w", err)
		}
	}

	prefix, _ := netip.ParsePrefix(bridge.DHCPCIDR)
	mask := net.CIDRMask(prefix.Bits(), 32)
	netmask := fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])

	content := fmt.Sprintf(`interface=%s
bind-interfaces
except-interface=lo
dhcp-authoritative
dhcp-range=%s,%s,%s,12h
dhcp-option=option:router,%s
dhcp-option=option:dns-server,%s
dhcp-hostsfile=%s
pid-file=%s
dhcp-leasefile=%s
log-dhcp
`, bridge.Name, bridge.DHCPStart, bridge.DHCPEnd, netmask, bridge.DHCPGateway, dns, hostsPath, pidPath, leasesPath)

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 DHCP 配置失败: %w", err)
	}

	utils.ExecCommand("pkill", "-f", fmt.Sprintf("dnsmasq.*%s", bridge.Name))
	result := utils.ExecCommand("dnsmasq", "--conf-file="+configPath)
	if result.Error != nil {
		return fmt.Errorf("启动 DHCP 服务失败: %s", result.Stderr)
	}
	return nil
}

func stopBridgeDNSMasq(bridgeName string) {
	utils.ExecCommand("pkill", "-f", fmt.Sprintf("dnsmasq.*%s", bridgeName))
	pidPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dnsmasq-%s.pid", bridgeName))
	_ = os.Remove(pidPath)
}
