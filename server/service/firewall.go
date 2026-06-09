package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kvm_console/config"
	"kvm_console/model"
	"kvm_console/utils"
)

const (
	firewallDir        = "/etc/kvm-console/firewall"
	firewallPolicyFile = "/etc/kvm-console/firewall/policy.json"
	firewallRulesFile  = "/etc/kvm-console/firewall/rules.nft"
	firewallTable      = "kvm_console_fw"
	defaultGeoBaseURL  = "https://www.ipdeny.com/ipblocks/data/aggregated"
)

// FirewallPolicy 防火墙策略，存放在本地 JSON 文件，避免依赖数据库。
type FirewallPolicy struct {
	Enabled                bool                          `json:"enabled"`
	Bridge                 string                        `json:"bridge"`
	VMSubnet               string                        `json:"vm_subnet"`
	OutboundEnabled        bool                          `json:"outbound_enabled"`
	InboundEnabled         bool                          `json:"inbound_enabled"`
	DisableVMIPv6          bool                          `json:"disable_vm_ipv6"`
	BlockAction            string                        `json:"block_action"`
	OutboundAllowedRegions []string                      `json:"outbound_allowed_regions"`
	InboundAllowedRegions  []string                      `json:"inbound_allowed_regions"`
	WhitelistCIDRs         []string                      `json:"whitelist_cidrs"`
	Regions                []FirewallRegion              `json:"regions"`
	VMOverrides            map[string]FirewallVMOverride `json:"vm_overrides"`
	PortForwardExemptions  map[string]bool               `json:"port_forward_exemptions"`
	GeoIPBaseURL           string                        `json:"geoip_base_url"`
	UpdatedAt              string                        `json:"updated_at"`
	AppliedAt              string                        `json:"applied_at"`
}

// FirewallRegion 表示一个区域的 IPv4 CIDR 集合。
type FirewallRegion struct {
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	CIDRs     []string `json:"cidrs"`
	Source    string   `json:"source"`
	UpdatedAt string   `json:"updated_at"`
}

// FirewallVMOverride 表示单台 VM 的覆盖策略。
type FirewallVMOverride struct {
	Mode    string   `json:"mode"`    // inherit/disabled/inbound_only/allow/block
	Regions []string `json:"regions"` // allow/block 模式下使用
}

// FirewallStatus 返回当前策略与系统实际状态。
type FirewallStatus struct {
	Policy         *FirewallPolicy `json:"policy"`
	Active         bool            `json:"active"`
	LastError      string          `json:"last_error"`
	RuleFile       string          `json:"rule_file"`
	PolicyFile     string          `json:"policy_file"`
	TableName      string          `json:"table_name"`
	NftAvailable   bool            `json:"nft_available"`
	VMs            []string        `json:"vms"`
	IPv6Note       string          `json:"ipv6_note"`
	GeoIPCopyright string          `json:"geoip_copyright"`
}

type FirewallImportParams struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	CIDRs  string `json:"cidrs"`
	Source string `json:"source"`
}

type FirewallGeoUpdateParams struct {
	Codes   []string `json:"codes"`
	BaseURL string   `json:"base_url"`
}

type FirewallOperationParams struct {
	Action string `json:"action"`
}

type firewallNetworkScope struct {
	IfName string
	CIDR   string
}

func defaultFirewallPolicy() *FirewallPolicy {
	subnet := "192.168.122"
	if config.GlobalConfig != nil {
		subnet = strings.TrimSpace(config.GlobalConfig.SubnetPrefix)
	}
	if subnet == "" {
		subnet = "192.168.122"
	}
	return &FirewallPolicy{
		Enabled:                false,
		Bridge:                 ovsBridgeName(),
		VMSubnet:               subnet + ".0/24",
		OutboundEnabled:        false,
		InboundEnabled:         false,
		DisableVMIPv6:          true,
		BlockAction:            "reject",
		OutboundAllowedRegions: []string{},
		InboundAllowedRegions:  []string{},
		WhitelistCIDRs:         []string{},
		Regions:                []FirewallRegion{},
		VMOverrides:            map[string]FirewallVMOverride{},
		PortForwardExemptions:  map[string]bool{},
		GeoIPBaseURL:           defaultGeoBaseURL,
	}
}

func ensureFirewallDir() error {
	if err := os.MkdirAll(filepath.Join(firewallDir, "backups"), 0755); err != nil {
		return fmt.Errorf("创建防火墙目录失败: %w", err)
	}
	// 测试目录可写性
	testFile := filepath.Join(firewallDir, ".writetest")
	if err := os.WriteFile(testFile, []byte(""), 0600); err != nil {
		return fmt.Errorf("防火墙目录不可写: %w", err)
	}
	os.Remove(testFile)
	return nil
}

func normalizeFirewallPolicy(policy *FirewallPolicy) *FirewallPolicy {
	if policy == nil {
		policy = defaultFirewallPolicy()
	}
	def := defaultFirewallPolicy()
	if strings.TrimSpace(policy.Bridge) == "" {
		policy.Bridge = def.Bridge
	}
	if useOVSNetwork() && strings.TrimSpace(policy.Bridge) == "virbr0" {
		policy.Bridge = ovsBridgeName()
	}
	if strings.TrimSpace(policy.VMSubnet) == "" {
		policy.VMSubnet = def.VMSubnet
	}
	if strings.TrimSpace(policy.BlockAction) == "" {
		policy.BlockAction = def.BlockAction
	}
	if strings.TrimSpace(policy.GeoIPBaseURL) == "" {
		policy.GeoIPBaseURL = def.GeoIPBaseURL
	}
	if policy.VMOverrides == nil {
		policy.VMOverrides = map[string]FirewallVMOverride{}
	}
	if policy.PortForwardExemptions == nil {
		policy.PortForwardExemptions = map[string]bool{}
	}
	if policy.Regions == nil {
		policy.Regions = []FirewallRegion{}
	}
	policy.BlockAction = normalizeBlockAction(policy.BlockAction)
	policy.OutboundAllowedRegions = normalizeStringList(policy.OutboundAllowedRegions)
	policy.InboundAllowedRegions = normalizeStringList(policy.InboundAllowedRegions)
	policy.WhitelistCIDRs = normalizeCIDRList(policy.WhitelistCIDRs)
	for i := range policy.Regions {
		policy.Regions[i].Code = normalizeRegionCode(policy.Regions[i].Code)
		policy.Regions[i].CIDRs = normalizeCIDRList(policy.Regions[i].CIDRs)
	}
	return policy
}

// GetFirewallPolicy 读取策略；文件不存在时返回默认策略。
func GetFirewallPolicy() (*FirewallPolicy, error) {
	data, err := os.ReadFile(firewallPolicyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultFirewallPolicy(), nil
		}
		return nil, fmt.Errorf("读取防火墙策略失败: %w", err)
	}
	var policy FirewallPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("解析防火墙策略失败: %w", err)
	}
	return normalizeFirewallPolicy(&policy), nil
}

// SaveFirewallPolicy 保存策略但不应用规则。
func SaveFirewallPolicy(policy *FirewallPolicy) error {
	if err := validateRawFirewallPolicy(policy); err != nil {
		return err
	}
	policy = normalizeFirewallPolicy(policy)
	if err := ValidateFirewallPolicy(policy); err != nil {
		return err
	}
	if err := ensureFirewallDir(); err != nil {
		return err
	}
	oldPolicy, _ := os.ReadFile(firewallPolicyFile)
	if len(oldPolicy) > 0 {
		backupPath := filepath.Join(firewallDir, "backups", fmt.Sprintf("policy.%s.json", time.Now().Format("20060102_150405")))
		_ = os.WriteFile(backupPath, oldPolicy, 0644)
		pruneFirewallBackups("policy.*.json", 10)
	}
	policy.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化防火墙策略失败: %w", err)
	}
	if err := os.WriteFile(firewallPolicyFile, data, 0644); err != nil {
		return fmt.Errorf("保存防火墙策略失败: %w", err)
	}
	return nil
}

func ValidateFirewallPolicy(policy *FirewallPolicy) error {
	if _, err := netip.ParsePrefix(policy.VMSubnet); err != nil {
		return fmt.Errorf("虚拟机网段无效: %s", policy.VMSubnet)
	}
	if policy.BlockAction != "reject" && policy.BlockAction != "drop" {
		return fmt.Errorf("拦截动作只支持 reject 或 drop")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_.:-]+$`).MatchString(policy.Bridge) {
		return fmt.Errorf("网桥名称格式无效")
	}
	for _, cidr := range policy.WhitelistCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("白名单 CIDR 无效: %s", cidr)
		}
	}
	for _, region := range policy.Regions {
		if region.Code == "" {
			return fmt.Errorf("区域代码不能为空")
		}
		for _, cidr := range region.CIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				return fmt.Errorf("区域 %s 中存在无效 CIDR: %s", region.Code, cidr)
			}
		}
	}
	for vmName, override := range policy.VMOverrides {
		if strings.TrimSpace(vmName) == "" {
			return fmt.Errorf("VM 覆盖策略中存在空名称")
		}
		mode := normalizeOverrideMode(override.Mode)
		if mode == "" {
			return fmt.Errorf("VM %s 的覆盖模式无效", vmName)
		}
	}
	return nil
}

func validateRawFirewallPolicy(policy *FirewallPolicy) error {
	if policy == nil {
		return nil
	}
	for _, cidr := range policy.WhitelistCIDRs {
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		if err := validateIPv4CIDROrAddr(cidr); err != nil {
			return fmt.Errorf("白名单 CIDR 无效: %s", cidr)
		}
	}
	for _, region := range policy.Regions {
		for _, cidr := range region.CIDRs {
			if strings.TrimSpace(cidr) == "" {
				continue
			}
			if err := validateIPv4CIDROrAddr(cidr); err != nil {
				return fmt.Errorf("区域 %s 中存在无效 CIDR: %s", region.Code, cidr)
			}
		}
	}
	return nil
}

func validateIPv4CIDROrAddr(value string) error {
	value = strings.TrimSpace(value)
	if addr, err := netip.ParseAddr(value); err == nil && addr.Is4() {
		return nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return fmt.Errorf("无效的 IPv4 CIDR")
	}
	return nil
}

// GetFirewallStatus 获取策略状态和 nft 实际状态。
func GetFirewallStatus() (*FirewallStatus, error) {
	policy, err := GetFirewallPolicy()
	if err != nil {
		return nil, err
	}
	activeResult := utils.ExecCommand("nft", "list", "table", "inet", firewallTable)
	nftResult := utils.ExecCommand("nft", "--version")
	return &FirewallStatus{
		Policy:         policy,
		Active:         activeResult.Error == nil,
		LastError:      strings.TrimSpace(activeResult.Stderr),
		RuleFile:       firewallRulesFile,
		PolicyFile:     firewallPolicyFile,
		TableName:      firewallTable,
		NftAvailable:   nftResult.Error == nil,
		VMs:            listAllVMNames(),
		IPv6Note:       "第一版仅管控 KVM IPv4 转发流量；VM IPv6 转发默认被独立表拒绝，宿主机 IPv6 不在本功能范围内。",
		GeoIPCopyright: "内置下载源默认使用 IPdeny 聚合 CIDR 数据，请遵守其版权和使用限制。",
	}, nil
}

// PreviewFirewallRules 只生成规则文本，不写入系统规则。
func PreviewFirewallRules(policy *FirewallPolicy) (string, error) {
	if err := validateRawFirewallPolicy(policy); err != nil {
		return "", err
	}
	policy = normalizeFirewallPolicy(policy)
	if err := ValidateFirewallPolicy(policy); err != nil {
		return "", err
	}
	return BuildFirewallRules(policy)
}

// ApplyFirewallPolicy 保存、校验并应用 nft 规则。
func ApplyFirewallPolicy(policy *FirewallPolicy, progress func(int, string)) error {
	if progress != nil {
		progress(10, "正在生成防火墙规则...")
	}
	if err := validateRawFirewallPolicy(policy); err != nil {
		return err
	}
	policy = normalizeFirewallPolicy(policy)
	policy.Enabled = true
	rules, err := BuildFirewallRules(policy)
	if err != nil {
		return err
	}
	if err := ensureFirewallDir(); err != nil {
		return err
	}
	if progress != nil {
		progress(35, "正在执行 nft dry-run 校验...")
	}
	if err := writeAndCheckFirewallRules(rules); err != nil {
		return err
	}
	if progress != nil {
		progress(60, "正在备份旧规则并应用新规则...")
	}
	backupCurrentFirewallFiles()
	result := utils.ExecShell(fmt.Sprintf("nft delete table inet %s 2>/dev/null || true; nft -f '%s'", firewallTable, firewallRulesFile))
	if result.Error != nil {
		return fmt.Errorf("应用 nft 防火墙规则失败: %s", result.Stderr)
	}
	policy.AppliedAt = time.Now().Format(time.RFC3339)
	if err := SaveFirewallPolicy(policy); err != nil {
		return err
	}
	if progress != nil {
		progress(100, "防火墙规则已应用")
	}
	return nil
}

// DisableFirewall 删除独立 nft 表并把策略标记为未启用。
func DisableFirewall(progress func(int, string)) error {
	if progress != nil {
		progress(20, "正在删除 KVM 防火墙独立规则表...")
	}
	result := utils.ExecShell(fmt.Sprintf("nft delete table inet %s 2>/dev/null || true", firewallTable))
	if result.Error != nil {
		return fmt.Errorf("禁用防火墙失败: %s", result.Stderr)
	}
	policy, err := GetFirewallPolicy()
	if err != nil {
		return err
	}
	policy.Enabled = false
	if err := SaveFirewallPolicy(policy); err != nil {
		return err
	}
	if progress != nil {
		progress(100, "防火墙已禁用")
	}
	return nil
}

// RollbackFirewall 当前第一版回滚为删除独立表，保证快速恢复网络。
func RollbackFirewall(progress func(int, string)) error {
	return DisableFirewall(progress)
}

func writeAndCheckFirewallRules(rules string) error {
	if err := os.WriteFile(firewallRulesFile, []byte(rules), 0644); err != nil {
		return fmt.Errorf("写入 nft 规则文件失败: %w", err)
	}
	result := utils.ExecCommand("nft", "-c", "-f", firewallRulesFile)
	if result.Error != nil {
		return fmt.Errorf("nft dry-run 校验失败: %s", result.Stderr)
	}
	return nil
}

func backupCurrentFirewallFiles() {
	_ = ensureFirewallDir()
	ts := time.Now().Format("20060102_150405")
	if data, err := os.ReadFile(firewallRulesFile); err == nil && len(data) > 0 {
		_ = os.WriteFile(filepath.Join(firewallDir, "backups", "rules."+ts+".nft"), data, 0644)
	}
	pruneFirewallBackups("rules.*.nft", 10)
}

func pruneFirewallBackups(pattern string, keep int) {
	matches, _ := filepath.Glob(filepath.Join(firewallDir, "backups", pattern))
	sort.Slice(matches, func(i, j int) bool {
		ii, _ := os.Stat(matches[i])
		jj, _ := os.Stat(matches[j])
		if ii == nil || jj == nil {
			return matches[i] > matches[j]
		}
		return ii.ModTime().After(jj.ModTime())
	})
	for i := keep; i < len(matches); i++ {
		_ = os.Remove(matches[i])
	}
}

// BuildFirewallRules 将策略转换为 nftables 规则。
func BuildFirewallRules(policy *FirewallPolicy) (string, error) {
	policy = normalizeFirewallPolicy(policy)
	if err := ValidateFirewallPolicy(policy); err != nil {
		return "", err
	}

	regionMap := make(map[string]FirewallRegion)
	for _, region := range policy.Regions {
		regionMap[normalizeRegionCode(region.Code)] = region
	}

	scopes := firewallNetworkScopes(policy)
	managedCIDRs := firewallScopeCIDRs(scopes)

	var b strings.Builder
	b.WriteString("table inet ")
	b.WriteString(firewallTable)
	b.WriteString(" {\n")

	writeSet := func(name string, cidrs []string) {
		cidrs = normalizeCIDRList(cidrs)
		b.WriteString("  set ")
		b.WriteString(name)
		b.WriteString(" {\n    type ipv4_addr\n    flags interval\n")
		if len(cidrs) > 0 {
			b.WriteString("    elements = { ")
			b.WriteString(strings.Join(cidrs, ", "))
			b.WriteString(" }\n")
		}
		b.WriteString("  }\n\n")
	}

	writeSet("vm_subnets4", managedCIDRs)
	writeSet("whitelist4", append(defaultWhitelistCIDRsForScopes(scopes), policy.WhitelistCIDRs...))
	writeSet("out_allowed4", collectRegionCIDRs(policy.OutboundAllowedRegions, regionMap))
	writeSet("in_allowed4", collectRegionCIDRs(policy.InboundAllowedRegions, regionMap))

	vmNames := sortedMapKeys(policy.VMOverrides)
	for _, vmName := range vmNames {
		override := policy.VMOverrides[vmName]
		mode := normalizeOverrideMode(override.Mode)
		if mode == "allow" || mode == "block" {
			writeSet(vmSetName(vmName), collectRegionCIDRs(override.Regions, regionMap))
		}
	}

	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority -50; policy accept;\n")
	b.WriteString("    ct state established,related accept\n")
	for _, scope := range scopes {
		b.WriteString(fmt.Sprintf("    iifname %q ip saddr %s ip daddr @whitelist4 accept\n", scope.IfName, scope.CIDR))
		b.WriteString(fmt.Sprintf("    oifname %q ip daddr %s ip saddr @whitelist4 accept\n", scope.IfName, scope.CIDR))
		b.WriteString(fmt.Sprintf("    iifname %q oifname %q accept\n", scope.IfName, scope.IfName))
	}

	if policy.DisableVMIPv6 {
		for _, scope := range scopes {
			b.WriteString(fmt.Sprintf("    iifname %q meta nfproto ipv6 reject\n", scope.IfName))
			b.WriteString(fmt.Sprintf("    oifname %q meta nfproto ipv6 reject\n", scope.IfName))
		}
	}

	for _, rule := range currentPortForwardRulesForPolicy(policy) {
		if !policy.PortForwardExemptions[rule.StableKey()] {
			continue
		}
		proto := strings.ToLower(rule.Protocol)
		if proto == "tcp" || proto == "udp" {
			scope, ok := firewallScopeForIP(scopes, rule.DestIP)
			if !ok {
				continue
			}
			b.WriteString(fmt.Sprintf("    oifname %q ip daddr %s %s dport %s accept\n", scope.IfName, rule.DestIP, proto, rule.DestPort))
		}
	}

	for _, vmName := range vmNames {
		override := policy.VMOverrides[vmName]
		vmIP := getFirewallVMIP(vmName)
		if vmIP == "" {
			continue
		}
		scope, ok := firewallScopeForIP(scopes, vmIP)
		if !ok {
			continue
		}
		switch normalizeOverrideMode(override.Mode) {
		case "inbound_only":
			b.WriteString(fmt.Sprintf("    iifname %q ip saddr %s %s\n", scope.IfName, vmIP, policy.BlockAction))
		case "disabled":
			b.WriteString(fmt.Sprintf("    ip saddr %s accept\n", vmIP))
			b.WriteString(fmt.Sprintf("    ip daddr %s accept\n", vmIP))
		case "allow":
			setName := vmSetName(vmName)
			b.WriteString(fmt.Sprintf("    iifname %q ip saddr %s ip daddr != @%s %s\n", scope.IfName, vmIP, setName, policy.BlockAction))
			b.WriteString(fmt.Sprintf("    oifname %q ip daddr %s ip saddr != @%s %s\n", scope.IfName, vmIP, setName, policy.BlockAction))
		case "block":
			setName := vmSetName(vmName)
			b.WriteString(fmt.Sprintf("    iifname %q ip saddr %s ip daddr @%s %s\n", scope.IfName, vmIP, setName, policy.BlockAction))
			b.WriteString(fmt.Sprintf("    oifname %q ip daddr %s ip saddr @%s %s\n", scope.IfName, vmIP, setName, policy.BlockAction))
		}
	}

	if policy.OutboundEnabled && len(collectRegionCIDRs(policy.OutboundAllowedRegions, regionMap)) > 0 {
		for _, scope := range scopes {
			b.WriteString(fmt.Sprintf("    iifname %q ip saddr %s ip daddr != @out_allowed4 %s\n", scope.IfName, scope.CIDR, policy.BlockAction))
		}
	}
	if policy.InboundEnabled && len(collectRegionCIDRs(policy.InboundAllowedRegions, regionMap)) > 0 {
		for _, scope := range scopes {
			b.WriteString(fmt.Sprintf("    oifname %q ip daddr %s ip saddr != @in_allowed4 %s\n", scope.IfName, scope.CIDR, policy.BlockAction))
		}
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String(), nil
}

func firewallNetworkScopes(policy *FirewallPolicy) []firewallNetworkScope {
	scopes := []firewallNetworkScope{{
		IfName: strings.TrimSpace(policy.Bridge),
		CIDR:   strings.TrimSpace(policy.VMSubnet),
	}}
	seen := map[string]bool{scopes[0].IfName + "|" + scopes[0].CIDR: true}
	if model.DB != nil {
		var switches []model.VPCSwitch
		model.DB.Order("id ASC").Find(&switches)
		for _, sw := range switches {
			if strings.TrimSpace(sw.CIDR) == "" {
				continue
			}
			if _, err := netip.ParsePrefix(sw.CIDR); err != nil {
				continue
			}
			scope := firewallNetworkScope{IfName: vpcGatewayPortName(sw.ID), CIDR: sw.CIDR}
			key := scope.IfName + "|" + scope.CIDR
			if seen[key] {
				continue
			}
			seen[key] = true
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func firewallScopeCIDRs(scopes []firewallNetworkScope) []string {
	cidrs := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if strings.TrimSpace(scope.CIDR) != "" {
			cidrs = append(cidrs, scope.CIDR)
		}
	}
	return normalizeCIDRList(cidrs)
}

func firewallScopeForIP(scopes []firewallNetworkScope, ipText string) (firewallNetworkScope, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(ipText))
	if err != nil || !ip.Is4() {
		return firewallNetworkScope{}, false
	}
	for _, scope := range scopes {
		prefix, err := netip.ParsePrefix(scope.CIDR)
		if err == nil && prefix.Contains(ip) {
			return scope, true
		}
	}
	return firewallNetworkScope{}, false
}

func defaultWhitelistCIDRs(policy *FirewallPolicy) []string {
	return defaultWhitelistCIDRsForScopes(firewallNetworkScopes(policy))
}

func defaultWhitelistCIDRsForScopes(scopes []firewallNetworkScope) []string {
	cidrs := append([]string{}, firewallScopeCIDRs(scopes)...)
	cidrs = append(cidrs,
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"224.0.0.0/4",
	)
	return cidrs
}

func collectRegionCIDRs(codes []string, regions map[string]FirewallRegion) []string {
	var cidrs []string
	for _, code := range codes {
		if region, ok := regions[normalizeRegionCode(code)]; ok {
			cidrs = append(cidrs, region.CIDRs...)
		}
	}
	return normalizeCIDRList(cidrs)
}

func currentPortForwardRulesForPolicy(policy *FirewallPolicy) []PortForwardRule {
	rules, err := listLivePortForwardsFromIPTables()
	if err != nil {
		return []PortForwardRule{}
	}
	return rules
}

func getFirewallVMIP(vmName string) string {
	ip := strings.TrimSpace(getVMIP(vmName, true))
	if ip == "" || ip == "unknown" {
		return ""
	}
	ip = strings.Fields(ip)[0]
	ip = strings.TrimSuffix(ip, "(静态)")
	if addr, err := netip.ParseAddr(ip); err == nil && addr.Is4() {
		return ip
	}
	return ""
}

func listAllVMNames() []string {
	result := utils.ExecCommand("virsh", "list", "--all", "--name")
	if result.Error != nil {
		return []string{}
	}
	var names []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedMapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func vmSetName(vmName string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	name := strings.ToLower(re.ReplaceAllString(vmName, "_"))
	if name == "" {
		name = "vm"
	}
	return "vm_" + name + "_regions4"
}

func normalizeRegionCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	code = regexp.MustCompile(`[^a-z0-9_-]`).ReplaceAllString(code, "_")
	return code
}

func normalizeOverrideMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "inherit":
		return "inherit"
	case "disabled", "inbound_only", "allow", "block":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return ""
	}
}

func normalizeBlockAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "drop" {
		return "drop"
	}
	return "reject"
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		value = normalizeRegionCode(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeCIDRList(values []string) []string {
	seen := make(map[string]bool)
	var prefixes []netip.Prefix
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if addr, err := netip.ParseAddr(value); err == nil && addr.Is4() {
			value = addr.String() + "/32"
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		prefix = prefix.Masked()
		value = prefix.String()
		if seen[value] {
			continue
		}
		seen[value] = true
		prefixes = append(prefixes, prefix)
	}

	sort.Slice(prefixes, func(i, j int) bool {
		startI, _, bitsI := ipv4PrefixRange(prefixes[i])
		startJ, _, bitsJ := ipv4PrefixRange(prefixes[j])
		if startI != startJ {
			return startI < startJ
		}
		return bitsI < bitsJ
	})

	var compacted []netip.Prefix
	for _, prefix := range prefixes {
		if prefixContainedInAny(prefix, compacted) {
			continue
		}
		compacted = append(compacted, prefix)
	}

	var result []string
	for _, prefix := range compacted {
		result = append(result, prefix.String())
	}
	sort.Strings(result)
	return result
}

func prefixContainedInAny(prefix netip.Prefix, existing []netip.Prefix) bool {
	start, end, _ := ipv4PrefixRange(prefix)
	for _, item := range existing {
		itemStart, itemEnd, _ := ipv4PrefixRange(item)
		if itemStart <= start && itemEnd >= end {
			return true
		}
	}
	return false
}

func ipv4PrefixRange(prefix netip.Prefix) (uint32, uint32, int) {
	addr := prefix.Masked().Addr().As4()
	start := uint32(addr[0])<<24 | uint32(addr[1])<<16 | uint32(addr[2])<<8 | uint32(addr[3])
	bits := prefix.Bits()
	var mask uint32
	if bits == 0 {
		mask = 0
	} else {
		mask = ^uint32(0) << uint(32-bits)
	}
	end := start | ^mask
	return start, end, bits
}

// ImportFirewallRegionCIDRs 导入本地区域 CIDR。
func ImportFirewallRegionCIDRs(params FirewallImportParams) (*FirewallPolicy, error) {
	policy, err := GetFirewallPolicy()
	if err != nil {
		return nil, err
	}
	code := normalizeRegionCode(params.Code)
	if code == "" {
		return nil, fmt.Errorf("区域代码不能为空")
	}
	cidrs := parseCIDRText(params.CIDRs)
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("未解析到有效 IPv4 CIDR")
	}
	region := FirewallRegion{
		Code:      code,
		Name:      strings.TrimSpace(params.Name),
		CIDRs:     cidrs,
		Source:    strings.TrimSpace(params.Source),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if region.Name == "" {
		region.Name = strings.ToUpper(code)
	}
	if region.Source == "" {
		region.Source = "local-import"
	}
	upsertFirewallRegion(policy, region)
	if err := SaveFirewallPolicy(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func upsertFirewallRegion(policy *FirewallPolicy, region FirewallRegion) {
	for i := range policy.Regions {
		if normalizeRegionCode(policy.Regions[i].Code) == region.Code {
			policy.Regions[i] = region
			return
		}
	}
	policy.Regions = append(policy.Regions, region)
	sort.Slice(policy.Regions, func(i, j int) bool {
		return policy.Regions[i].Code < policy.Regions[j].Code
	})
}

func parseCIDRText(text string) []string {
	var values []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(strings.ReplaceAll(line, ",", " "))
		for _, part := range strings.Fields(line) {
			values = append(values, part)
		}
	}
	return normalizeCIDRList(values)
}

// UpdateFirewallGeoIP 下载指定区域的 IPdeny 聚合 CIDR。
func UpdateFirewallGeoIP(ctx context.Context, params FirewallGeoUpdateParams, progress func(int, string)) error {
	policy, err := GetFirewallPolicy()
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(params.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(policy.GeoIPBaseURL, "/")
	}
	if baseURL == "" {
		baseURL = defaultGeoBaseURL
	}
	codes := normalizeStringList(params.Codes)
	if len(codes) == 0 {
		return fmt.Errorf("请至少选择一个国家或地区代码")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for i, code := range codes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if progress != nil {
			progress(10+i*80/len(codes), fmt.Sprintf("正在下载区域 %s 的 CIDR 数据...", strings.ToUpper(code)))
		}
		url := fmt.Sprintf("%s/%s-aggregated.zone", baseURL, code)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("下载 %s 失败: %w", code, err)
		}
		data, err := readAllAndClose(resp)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("下载 %s 失败: HTTP %d", code, resp.StatusCode)
		}
		cidrs := parseCIDRText(string(data))
		if len(cidrs) == 0 {
			return fmt.Errorf("区域 %s 下载结果中没有有效 IPv4 CIDR", code)
		}
		upsertFirewallRegion(policy, FirewallRegion{
			Code:      code,
			Name:      strings.ToUpper(code),
			CIDRs:     cidrs,
			Source:    url,
			UpdatedAt: time.Now().Format(time.RFC3339),
		})
	}
	policy.GeoIPBaseURL = baseURL
	if err := SaveFirewallPolicy(policy); err != nil {
		return err
	}
	if progress != nil {
		progress(100, "GeoIP 区域数据已更新")
	}
	return nil
}

func readAllAndClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var b strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取下载内容失败: %w", err)
	}
	return []byte(b.String()), nil
}

// SetPortForwardFirewallExemption 设置端口转发是否豁免入站区域限制。
func SetPortForwardFirewallExemption(key string, exempt bool) (*FirewallPolicy, error) {
	policy, err := GetFirewallPolicy()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("端口转发规则标识不能为空")
	}
	policy.PortForwardExemptions[key] = exempt
	if !exempt {
		delete(policy.PortForwardExemptions, key)
	}
	if err := SaveFirewallPolicy(policy); err != nil {
		return nil, err
	}
	return policy, nil
}

// ClearPortForwardFirewallExemption 清理已删除端口转发的区域限制豁免记录。
func ClearPortForwardFirewallExemption(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	policy, err := GetFirewallPolicy()
	if err != nil {
		return err
	}
	if policy.PortForwardExemptions == nil || !policy.PortForwardExemptions[key] {
		return nil
	}
	delete(policy.PortForwardExemptions, key)
	return SaveFirewallPolicy(policy)
}
