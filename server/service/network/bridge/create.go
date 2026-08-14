package bridge

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/model"
	ovspkg "kvm_console/service/ovs"
	"kvm_console/utils"
)

func init() {
	HookStartBridgeDNSMasq = startBridgeDNSMasq
	HookListBridgeStaticHosts = ListBridgeStaticHosts
	HookListBridgeDHCPLeases = ListBridgeDHCPLeases
	HookUpsertBridgeStaticHost = UpsertBridgeStaticHost
	HookRemoveBridgeStaticHost = RemoveBridgeStaticHost
	HookReloadBridgeDNSMasq = reloadBridgeDNSMasq
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
		ipCfg = CaptureInterfaceIP(req.UplinkIF)
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
		HostAddrs6: ipCfg.Addrs6, HostGateway6: ipCfg.Gateway6, HostMetric6: ipCfg.Metric6,
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
	if uplink != "" {
		if result := utils.ExecCommand("ovs-vsctl", "--may-exist", "add-port", bridge, uplink); result.Error != nil {
			return fmt.Errorf("添加物理网卡到桥接网桥失败: %s", result.Stderr)
		}
		utils.ExecCommand("ip", "link", "set", uplink, "up")
	}
	// IP 迁移逻辑（同时处理 IPv4 和 IPv6）
	if migrateHostIP && uplink != "" {
		// 检查网桥是否已有 IP（重启恢复场景：systemd 服务已应用了静态 IP）
		bridgeCfg := CaptureInterfaceIP(bridge)
		bridgeHasIP := strings.TrimSpace(bridgeCfg.Addrs) != "" || strings.TrimSpace(bridgeCfg.Addrs6) != ""
		if !bridgeHasIP {
			// 网桥没有 IP，尝试从物理口迁移或使用存储值
			uplinkCfg := CaptureInterfaceIP(uplink)
			if strings.TrimSpace(uplinkCfg.Addrs) != "" || strings.TrimSpace(uplinkCfg.Addrs6) != "" {
				// 物理口有 IP，执行动态迁移（IPv4 + IPv6）
				migrateInterfaceIPToBridge(uplink, bridge)
			} else if strings.TrimSpace(cfg.Addrs) != "" || strings.TrimSpace(cfg.Addrs6) != "" {
				// 物理口也没 IP，使用存储的静态配置恢复（IPv4 + IPv6）
				applyStaticIPToBridge(bridge, cfg)
			}
		}
		// DNS 总是需要确保配置正确（重启恢复场景下即使网桥已有 IP，DNS 也可能丢失）
		ensureBridgeResolvedDNSWithStatic(uplink, bridge, cfg.DNS)
		// 如果 cfg 为空但网桥已有 IP，更新 cfg 用于写入脚本
		if strings.TrimSpace(cfg.Addrs) == "" && strings.TrimSpace(cfg.Addrs6) == "" {
			cfg = CaptureInterfaceIP(bridge)
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
	if uplink != "" {
		disableNetworkdDHCPForPort(uplink)
	}
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

// ValidateVPCSwitchUplink 校验交换机上行链路的占用关系与三层出口条件。
// 托管 NAT 上行允许共享；二层直通只有在共用同一网桥且 VLAN 唯一时允许共享。
func ValidateVPCSwitchUplink(uplink, uplinkGateway string, managed bool, switchID uint, targetBridge string, bridgeVLANID int) error {
	uplink = strings.TrimSpace(uplink)
	uplinkGateway = strings.TrimSpace(uplinkGateway)
	if !isPhysicalInterface(uplink) {
		return fmt.Errorf("请选择真实物理网卡")
	}
	if model.DB != nil {
		query := model.DB.Model(&model.VPCSwitch{}).Where("uplink_if = ?", uplink)
		if switchID > 0 {
			query = query.Where("id <> ?", switchID)
		}
		if !managed {
			var switches []model.VPCSwitch
			query.Find(&switches)
			for _, sw := range switches {
				// 托管 NAT 只借用三层出口，不占用二层直通网桥。
				if sw.DHCPEnabled {
					continue
				}
				if strings.TrimSpace(sw.BridgeName) != strings.TrimSpace(targetBridge) {
					return fmt.Errorf("物理网卡 %s 已由其它直通网桥使用", uplink)
				}
				if bridgeVLANID == 0 {
					return fmt.Errorf("物理网卡 %s 已有直通交换机，共享该上行时 VLAN ID 必须为 1-4094", uplink)
				}
				if sw.BridgeVLANID == bridgeVLANID {
					return fmt.Errorf("物理网卡 %s 的桥接 VLAN ID %d 已被交换机「%s」使用", uplink, bridgeVLANID, sw.Name)
				}
			}
		}

		var legacyCount int64
		model.DB.Model(&model.NetworkBridge{}).Where("uplink_if = ? AND name <> ?", uplink, strings.TrimSpace(targetBridge)).Count(&legacyCount)
		if legacyCount > 0 && !managed {
			return fmt.Errorf("物理网卡 %s 已由历史宿主机网桥使用", uplink)
		}
	}
	ports := readOVSPortBridgeMap()
	if currentBridge := strings.TrimSpace(ports[uplink]); currentBridge != "" && currentBridge != strings.TrimSpace(targetBridge) {
		if !managed && currentBridge == ovspkg.OvsBridgeName() {
			return fmt.Errorf("物理网卡 %s 是系统基础网络上行，不能切换为二层直通", uplink)
		}
		ownedByCurrentSwitch := false
		if model.DB != nil && switchID > 0 {
			var current model.VPCSwitch
			if err := model.DB.First(&current, switchID).Error; err == nil {
				ownedByCurrentSwitch = strings.EqualFold(strings.TrimSpace(current.UplinkIF), uplink) &&
					strings.EqualFold(strings.TrimSpace(current.BridgeName), currentBridge)
			}
		}
		// 托管 NAT 可以从已有 OVS 直通网桥的三层接口出站，不会重复接管物理端口。
		if !ownedByCurrentSwitch && !managed {
			return fmt.Errorf("物理网卡 %s 已接入 OVS 网桥 %s", uplink, currentBridge)
		}
	}
	if managed {
		effective := EffectiveL3Interface(uplink)
		cfg := CaptureInterfaceIPv4(effective)
		if strings.TrimSpace(cfg.Addrs) == "" {
			return fmt.Errorf("物理网卡 %s 的有效三层接口 %s 缺少可用的 IPv4 地址", uplink, effective)
		}
		gateway := uplinkGateway
		if gateway == "" {
			gateway = strings.TrimSpace(cfg.Gateway)
		}
		if gateway == "" {
			return fmt.Errorf("物理网卡 %s 已检测到 IPv4 地址，但未检测到该出口的默认网关，请填写上行网关", uplink)
		}
		if ip := net.ParseIP(gateway); ip == nil || ip.To4() == nil {
			return fmt.Errorf("物理网卡 %s 的上行网关不是有效的 IPv4 地址", uplink)
		}
	}
	return nil
}

// EffectiveL3Interface 返回内核实际承载地址与默认路由的三层接口。
func EffectiveL3Interface(uplink string) string {
	uplink = strings.TrimSpace(uplink)
	if uplink == "" {
		return ""
	}
	bridge := strings.TrimSpace(readOVSPortBridgeMap()[uplink])
	if bridge != "" && len(CaptureInterfaceIPv4(bridge).Addrs) > 0 {
		return bridge
	}
	return uplink
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

	// 确保 dhcp-hosts 文件与数据库同步（向后兼容：首次启动时从文件迁移）
	if err := EnsureBridgeStaticHostsFile(bridge.Name); err != nil {
		logger.App.Warn("同步 DHCP 静态绑定文件失败", "bridge", bridge.Name, "error", err)
	}

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
dhcp-ignore=tag:!known
pid-file=%s
dhcp-leasefile=%s
log-dhcp
`, bridge.Name, bridge.DHCPStart, bridge.DHCPEnd, netmask, bridge.DHCPGateway, dns, hostsPath, pidPath, leasesPath)

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入 DHCP 配置失败: %w", err)
	}

	// 放行 DHCP/DNS 入站规则，确保虚拟机 DHCP Discover 能到达 dnsmasq（预设直通/共享/旧版网桥路径统一兜底）
	if err := ovspkg.EnsureLocalDNSMasqInputRules(bridge.Name); err != nil {
		logger.App.Warn("配置桥接网桥 DHCP/DNS 入站规则失败", "bridge", bridge.Name, "error", err)
	}

	if err := stopBridgeDNSMasq(bridge.Name); err != nil {
		logger.App.Warn("停止桥接网桥 DHCP 服务失败", "bridge", bridge.Name, "error", err)
	}
	// 等待旧进程释放端口，避免 Address already in use
	time.Sleep(300 * time.Millisecond)
	result := utils.ExecCommand("dnsmasq", "--conf-file="+configPath)
	if result.Error != nil {
		return fmt.Errorf("启动 DHCP 服务失败: %s", result.Stderr)
	}
	return nil
}

func stopBridgeDNSMasq(bridgeName string) error {
	pidPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dnsmasq-%s.pid", bridgeName))
	if data, err := os.ReadFile(pidPath); err == nil {
		pid := strings.TrimSpace(string(data))
		if pid != "" {
			result := utils.ExecCommand("kill", "-9", pid)
			if result.Error == nil {
				_ = os.Remove(pidPath)
				return nil
			}
		}
	}
	utils.ExecCommand("pkill", "-f", fmt.Sprintf("dnsmasq.*%s", bridgeName))
	utils.ExecCommand("pkill", "-f", "dnsmasq.*kvm-console")
	utils.ExecCommand("pkill", "-9", "dnsmasq")
	_ = os.Remove(pidPath)
	return nil
}

func reloadBridgeDNSMasq(bridgeName string) error {
	// 先同步数据库到文件
	if err := EnsureBridgeStaticHostsFile(bridgeName); err != nil {
		logger.App.Warn("重载前同步 DHCP 静态绑定文件失败", "bridge", bridgeName, "error", err)
	}
	pidPath := filepath.Join(bridgeConfigDir, fmt.Sprintf("dnsmasq-%s.pid", bridgeName))
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			// dnsmasq 未运行，尝试自动启动
			logger.App.Info("DHCP 服务未运行，尝试自动启动", "bridge", bridgeName)
			if startErr := autoStartBridgeDNSMasq(bridgeName); startErr != nil {
				return fmt.Errorf("桥接网桥 %s 的 DHCP 服务未启动且自动启动失败: %v", bridgeName, startErr)
			}
			// 启动成功后重新加载（同步静态绑定）
			return EnsureBridgeStaticHostsFile(bridgeName)
		}
		return err
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return fmt.Errorf("无法读取桥接网桥 %s 的 DHCP 服务 PID", bridgeName)
	}
	result := utils.ExecCommand("kill", "-HUP", pid)
	if result.Error != nil {
		return fmt.Errorf("重新加载桥接网桥 %s 的 DHCP 服务失败: %s", bridgeName, result.Stderr)
	}
	return nil
}

// autoStartBridgeDNSMasq 尝试从数据库查找网桥或交换机的 DHCP 配置来启动 dnsmasq。
func autoStartBridgeDNSMasq(bridgeName string) error {
	if HookStartBridgeDNSMasq == nil {
		return fmt.Errorf("DHCP 启动钩子未初始化")
	}
	// 优先查找网桥本身的 DHCP 配置
	var bridge model.NetworkBridge
	if err := model.DB.Where("name = ?", bridgeName).First(&bridge).Error; err == nil && bridge.DHCPCIDR != "" {
		return HookStartBridgeDNSMasq(bridge)
	}
	// 网桥无 DHCP 配置时，查找使用该网桥的预设模式交换机
	var sw model.VPCSwitch
	if err := model.DB.Where("bridge_name = ? AND bridge_ip_mode = ? AND cidr != ''", bridgeName, "preset").First(&sw).Error; err == nil && sw.CIDR != "" && sw.DHCPStart != "" && sw.DHCPEnd != "" {
		spooferBridge := model.NetworkBridge{
			Name:        bridgeName,
			DHCPCIDR:    sw.CIDR,
			DHCPGateway: sw.GatewayIP,
			DHCPStart:   sw.DHCPStart,
			DHCPEnd:     sw.DHCPEnd,
		}
		return HookStartBridgeDNSMasq(spooferBridge)
	}
	return fmt.Errorf("网桥 %s 无 DHCP 配置且无预设模式交换机", bridgeName)
}
