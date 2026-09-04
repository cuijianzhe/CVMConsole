package ovs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kvm_console/logger"
	"kvm_console/model"
	"kvm_console/service/ip_resolver"
	netpkg "kvm_console/service/network"
	vpcpkg "kvm_console/service/network/vpc"
	"kvm_console/utils"
)

// ListOVSDHCPLeases reads and parses the OVS DHCP leases file.
func ListOVSDHCPLeases() ([]OVSDHCPLease, error) {
	data, err := os.ReadFile(OVSLeasesFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []OVSDHCPLease{}, nil
		}
		return nil, err
	}
	return ParseOVSDHCPLeasesText(string(data)), nil
}

// ListVPCDHCPLeases reads and parses all VPC DHCP leases files.
func ListVPCDHCPLeases() ([]OVSDHCPLease, error) {
	files, err := filepath.Glob(filepath.Join(vpcpkg.VPCConfigDir, "leases-*"))
	if err != nil {
		return nil, err
	}
	var leases []OVSDHCPLease
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		leases = append(leases, ParseOVSDHCPLeasesText(string(data))...)
	}
	return leases, nil
}

// ListVPCDHCPLeasesForSwitch reads and parses the DHCP leases file for a specific VPC switch.
func ListVPCDHCPLeasesForSwitch(switchID uint) ([]OVSDHCPLease, error) {
	data, err := os.ReadFile(filepath.Join(vpcpkg.VPCConfigDir, fmt.Sprintf("leases-%d", switchID)))
	if err != nil {
		if os.IsNotExist(err) {
			return []OVSDHCPLease{}, nil
		}
		return nil, err
	}
	return ParseOVSDHCPLeasesText(string(data)), nil
}

// ParseOVSDHCPLeasesText parses the text content of a DHCP leases file.
func ParseOVSDHCPLeasesText(text string) []OVSDHCPLease {
	var leases []OVSDHCPLease
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		lease := OVSDHCPLease{
			ExpiryTime: formatOVSLeaseExpiry(fields[0]),
			ExpiryUnix: parseOVSLeaseExpiryUnix(fields[0]),
			MAC:        strings.ToLower(fields[1]),
			IP:         fields[2],
		}
		if len(fields) >= 4 && fields[3] != "*" {
			lease.Hostname = fields[3]
		}
		if len(fields) >= 5 && fields[4] != "*" {
			lease.ClientID = fields[4]
		}
		leases = append(leases, lease)
	}
	return leases
}

func parseOVSLeaseExpiryUnix(raw string) int64 {
	sec, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return sec
}

func formatOVSLeaseExpiry(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return raw
	}
	if sec == 0 {
		return "永久"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}

// NewerOVSDHCPLease returns the lease with the later expiry time.
func NewerOVSDHCPLease(current, candidate OVSDHCPLease) OVSDHCPLease {
	if current.IP == "" {
		return candidate
	}
	currentExpiry := current.ExpiryUnix
	candidateExpiry := candidate.ExpiryUnix
	if currentExpiry == 0 {
		currentExpiry = 1<<63 - 1
	}
	if candidateExpiry == 0 {
		candidateExpiry = 1<<63 - 1
	}
	if candidateExpiry >= currentExpiry {
		return candidate
	}
	return current
}

// ── dhcp_release：通知运行中的 dnsmasq 释放内存租约 ──
// dnsmasq 的活跃租约保存在进程内存中，SIGHUP 重载只会重读 dhcp-hosts 而不会重读
// 租约文件；仅手动删除 leases 文件中的条目，dnsmasq 回写文件时还会将其"复活"。
// 必须使用 dnsmasq-utils 包提供的 dhcp_release 通知 dnsmasq 立即释放租约
//（其内部以客户端身份发送 DHCPRELEASE 报文，dnsmasq 处理后会自行回写租约文件）。

var (
	dhcpReleaseBinPath string
	dhcpReleaseChecked bool
)

// lookupDHCPRelease 查找 dhcp_release 可执行文件路径（仅检查一次，缺失时告警）
func lookupDHCPRelease() string {
	if !dhcpReleaseChecked {
		dhcpReleaseChecked = true
		if p, err := exec.LookPath("dhcp_release"); err == nil {
			dhcpReleaseBinPath = p
		} else {
			logger.App.Warn("未找到 dhcp_release 命令，无法通知运行中的 dnsmasq 释放租约，请安装 dnsmasq-utils 包")
		}
	}
	return dhcpReleaseBinPath
}

// releaseRunningDHCPLease 通过 dhcp_release 通知监听在 iface 上的 dnsmasq 释放指定租约。
// 租约不存在或已过期时命令可能返回失败，仅记录日志不阻断主流程。
// clientID 为租约文件第 5 字段，无值（"*"）时省略，避免与服务端记录不匹配导致释放失败。
func releaseRunningDHCPLease(iface, ip, mac, clientID string) {
	bin := lookupDHCPRelease()
	if bin == "" || iface == "" || ip == "" || mac == "" {
		return
	}
	args := []string{iface, ip, mac}
	if clientID != "" {
		args = append(args, clientID)
	}
	// 使用 Quiet 变体：租约已过期/已释放时非零退出属预期情况，仅 DEBUG 记录
	result := utils.ExecCommandQuiet(bin, args...)
	if result.Error != nil {
		logger.App.Warn("dhcp_release 释放租约失败（租约可能已过期或不存在）",
			"iface", iface, "ip", ip, "mac", mac, "stderr", result.Stderr)
		return
	}
	logger.App.Info("已通知 dnsmasq 释放 DHCP 租约", "iface", iface, "ip", ip, "mac", mac)
}

// leaseClientID 从 dnsmasq leases 行字段中提取 client-id（第 5 字段），"*" 表示无
func leaseClientID(fields []string) string {
	if len(fields) >= 5 && fields[4] != "*" {
		return fields[4]
	}
	return ""
}

// CleanOVSDHCPLease removes DHCP lease entries matching the given MAC or IP.
// 集中式 OVS dnsmasq（系统基础网络 VLAN0）监听在 br-ovs 上，删除文件条目前
// 先通过 dhcp_release 通知进程释放内存租约，避免被删除虚拟机的幽灵租约继续占用 IP、
// 导致 dnsmasq 拒绝下发静态地址（"not using configured address ... because it is leased to ..."）。
func CleanOVSDHCPLease(mac, ipAddr string) {
	data, err := os.ReadFile(OVSLeasesFile)
	if err != nil {
		return
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			if (mac != "" && strings.EqualFold(fields[1], mac)) || (ipAddr != "" && fields[2] == ipAddr) {
				// 先通知运行中的 dnsmasq 释放内存租约，再从文件剔除
				releaseRunningDHCPLease(OvsBridgeName(), fields[2], fields[1], leaseClientID(fields))
				continue
			}
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	_ = os.WriteFile(OVSLeasesFile, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// CleanVPCDHCPLease removes DHCP lease entries from a specific VPC switch.
func CleanVPCDHCPLease(switchID uint, mac, ipAddr string) {
	path := filepath.Join(vpcpkg.VPCConfigDir, fmt.Sprintf("leases-%d", switchID))
	// VPC NAT 交换机（VLAN>0）的独立 dnsmasq 监听在网关端口 vpcsw<switchID> 上
	cleanVPCDHCPLeaseFile(path, vpcpkg.VPCGatewayPortName(switchID), mac, ipAddr)
}

// CleanAllVPCDHCPLeases removes DHCP lease entries from all VPC switches matching the given MAC or IP.
func CleanAllVPCDHCPLeases(mac, ipAddr string) {
	entries, err := os.ReadDir(vpcpkg.VPCConfigDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "leases-") {
			continue
		}
		// 从文件名 leases-<switchID> 解析交换机 ID，推导 dnsmasq 监听接口 vpcsw<switchID>
		iface := ""
		if id, parseErr := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "leases-"), 10, 64); parseErr == nil {
			iface = vpcpkg.VPCGatewayPortName(uint(id))
		}
		cleanVPCDHCPLeaseFile(filepath.Join(vpcpkg.VPCConfigDir, entry.Name()), iface, mac, ipAddr)
	}
}

// cleanVPCDHCPLeaseFile 清理指定 VPC 租约文件中匹配 MAC 或 IP 的条目，
// 并通过 dhcp_release 在 iface（vpcsw<switchID> 网关端口）上通知运行中的 dnsmasq 释放内存租约。
func cleanVPCDHCPLeaseFile(path, iface, mac, ipAddr string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			if (mac != "" && strings.EqualFold(fields[1], mac)) || (ipAddr != "" && fields[2] == ipAddr) {
				releaseRunningDHCPLease(iface, fields[2], fields[1], leaseClientID(fields))
				continue
			}
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// GetOVSLeaseIPByMAC finds the latest DHCP lease IP for the given MAC across OVS and VPC.
func GetOVSLeaseIPByMAC(mac string) string {
	leases, err := ListOVSDHCPLeases()
	if err != nil {
		leases = []OVSDHCPLease{}
	}
	if vpcLeases, vpcErr := ListVPCDHCPLeases(); vpcErr == nil {
		leases = append(leases, vpcLeases...)
	}
	var latest OVSDHCPLease
	for _, lease := range leases {
		if strings.EqualFold(lease.MAC, mac) {
			latest = NewerOVSDHCPLease(latest, lease)
		}
	}
	return latest.IP
}

// GetVPCLeaseIPForVMByMAC finds the VPC DHCP lease IP for a VM by MAC address (multi-NIC scenario).
func GetVPCLeaseIPForVMByMAC(vmName, mac string) string {
	vmName = strings.TrimSpace(vmName)
	mac = strings.ToLower(strings.TrimSpace(mac))
	if vmName == "" || mac == "" || model.DB == nil {
		return ""
	}
	var bindings []model.VPCVMBinding
	if err := model.DB.Where("vm_name = ?", vmName).Order("interface_order ASC").Find(&bindings).Error; err != nil || len(bindings) == 0 {
		return ""
	}
	for _, binding := range bindings {
		if ip := netpkg.GetVPCStaticIPByMAC(binding.SwitchID, mac); ip != "" {
			return ip
		}
		leasesPath := filepath.Join(vpcpkg.VPCConfigDir, fmt.Sprintf("leases-%d", binding.SwitchID))
		data, err := os.ReadFile(leasesPath)
		if err != nil {
			continue
		}
		leases := ParseOVSDHCPLeasesText(string(data))
		var latest OVSDHCPLease
		for _, lease := range leases {
			if strings.EqualFold(lease.MAC, mac) {
				latest = NewerOVSDHCPLease(latest, lease)
			}
		}
		if latest.IP != "" {
			return latest.IP
		}
	}
	return ""
}

// GetVPCLeaseIPForVM finds the VPC DHCP lease IP for a VM (first interface).
func GetVPCLeaseIPForVM(vmName string) string {
	vmName = strings.TrimSpace(vmName)
	if vmName == "" || model.DB == nil {
		return ""
	}
	var binding model.VPCVMBinding
	if err := model.DB.Where("vm_name = ?", vmName).First(&binding).Error; err != nil {
		return ""
	}
	mac := ip_resolver.GetFirstVMMAC(vmName)
	if mac == "" {
		return ""
	}
	if ip := netpkg.GetVPCStaticIPByMAC(binding.SwitchID, mac); ip != "" {
		return ip
	}
	data, err := os.ReadFile(filepath.Join(vpcpkg.VPCConfigDir, fmt.Sprintf("leases-%d", binding.SwitchID)))
	if err != nil {
		return ""
	}
	leases := ParseOVSDHCPLeasesText(string(data))
	var latest OVSDHCPLease
	for _, lease := range leases {
		if strings.EqualFold(lease.MAC, mac) {
			latest = NewerOVSDHCPLease(latest, lease)
		}
	}
	return latest.IP
}
