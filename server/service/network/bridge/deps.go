package bridge

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"kvm_console/logger"
	"kvm_console/model"
)

var (
	HookEnsureOVSNetworkReady     func() error
	HookEnsureAllVPCSwitchRuntime func() error
	HookWriteFileIfChanged        func(path string, content []byte, perm os.FileMode) (bool, error)
	HookOvsBridgeName             func() string

	HookListBridgeStaticHosts  func(bridgeName string) ([]BridgeStaticHost, error)
	HookListBridgeDHCPLeases   func(bridgeName string) ([]BridgeDHCPLease, error)
	HookUpsertBridgeStaticHost func(bridgeName, vmName, mac, ipAddr string) error
	HookRemoveBridgeStaticHost func(bridgeName, vmName, mac string) (string, error)
	HookRemoveBridgeDHCPLease  func(bridgeName, vmName, mac string) (string, error)
	HookReloadBridgeDNSMasq    func(bridgeName string) error
)

// BridgeStaticHost 内存结构，用于文件操作（向后兼容）
type BridgeStaticHost struct {
	VMName string
	MAC    string
	IP     string
}

// BridgeDHCPLease DHCP 租约结构
type BridgeDHCPLease struct {
	ExpiryTime string
	ExpiryUnix int64
	MAC        string
	IP         string
	Hostname   string
	ClientID   string
}

// dhcpHostsMutex 保护 dhcp-hosts 文件的并发读写
var dhcpHostsMutex sync.Mutex

// bridgeDHCPHostsPath 获取指定网桥的 dhcp-hosts 文件路径
func bridgeDHCPHostsPath(bridgeName string) string {
	return filepath.Join(bridgeConfigDir, fmt.Sprintf("dhcp-hosts-%s", bridgeName))
}

// bridgeDHCPLeasesPath 获取指定网桥的 leases 文件路径
func bridgeDHCPLeasesPath(bridgeName string) string {
	return filepath.Join(bridgeConfigDir, fmt.Sprintf("leases-%s", bridgeName))
}

// ListBridgeStaticHosts 从数据库读取 DHCP 静态绑定列表
// 如果数据库未初始化，回退到文件模式（向后兼容）
func ListBridgeStaticHosts(bridgeName string) ([]BridgeStaticHost, error) {
	if model.DB == nil {
		return listBridgeStaticHostsFromFile(bridgeName)
	}
	var dbHosts []model.BridgeStaticHostDB
	if err := model.DB.Where("bridge_name = ?", bridgeName).Order("id ASC").Find(&dbHosts).Error; err != nil {
		return nil, fmt.Errorf("查询 DHCP 静态绑定失败: %w", err)
	}
	var hosts []BridgeStaticHost
	for _, h := range dbHosts {
		hosts = append(hosts, BridgeStaticHost{
			VMName: h.VMName,
			MAC:    h.MAC,
			IP:     h.IP,
		})
	}
	return hosts, nil
}

// listBridgeStaticHostsFromFile 从文件读取（降级模式，仅用于向后兼容）
func listBridgeStaticHostsFromFile(bridgeName string) ([]BridgeStaticHost, error) {
	path := bridgeDHCPHostsPath(bridgeName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []BridgeStaticHost{}, nil
		}
		return nil, err
	}
	var hosts []BridgeStaticHost
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) >= 2 {
			host := BridgeStaticHost{
				MAC: strings.ToLower(strings.TrimSpace(fields[0])),
			}
			if len(fields) >= 3 {
				host.IP = strings.TrimSpace(fields[1])
				host.VMName = strings.TrimSpace(fields[2])
			} else {
				host.VMName = strings.TrimSpace(fields[1])
			}
			hosts = append(hosts, host)
		}
	}
	return hosts, nil
}

// writeBridgeStaticHostsFile 将内存中的绑定列表写入 dhcp-hosts 文件
func writeBridgeStaticHostsFile(bridgeName string, hosts []BridgeStaticHost) error {
	path := bridgeDHCPHostsPath(bridgeName)
	var lines []string
	for _, host := range hosts {
		if host.IP == "" {
			line := fmt.Sprintf("%s,%s", host.MAC, host.VMName)
			lines = append(lines, line)
		} else {
			line := fmt.Sprintf("%s,%s,%s", host.MAC, host.IP, host.VMName)
			lines = append(lines, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// validateStaticHostUniqueness 校验静态绑定的唯一性
// 检查 IP 和 MAC 是否在同一网桥内重复
func validateStaticHostUniqueness(bridgeName, mac, ipAddr, excludeVM string) error {
	if model.DB == nil {
		return nil
	}
	// 检查 IP 是否被其他 VM 占用
	if ipAddr != "" {
		var count int64
		query := model.DB.Model(&model.BridgeStaticHostDB{}).
			Where("bridge_name = ? AND ip = ?", bridgeName, ipAddr)
		if excludeVM != "" {
			query = query.Where("vm_name <> ?", excludeVM)
		}
		if err := query.Count(&count).Error; err != nil {
			return fmt.Errorf("校验 IP 唯一性失败: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("IP %s 在网桥 %s 中已被占用", ipAddr, bridgeName)
		}
	}
	// 检查 MAC 是否被其他 VM 占用
	if mac != "" {
		var count int64
		query := model.DB.Model(&model.BridgeStaticHostDB{}).
			Where("bridge_name = ? AND mac = ?", bridgeName, mac)
		if excludeVM != "" {
			query = query.Where("vm_name <> ?", excludeVM)
		}
		if err := query.Count(&count).Error; err != nil {
			return fmt.Errorf("校验 MAC 唯一性失败: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("MAC %s 在网桥 %s 中已被占用", mac, bridgeName)
		}
	}
	return nil
}

// UpsertBridgeStaticHost 新增或更新 DHCP 静态绑定
// 流程：加锁 → 校验唯一性 → 写入数据库 → 同步文件 → reload dnsmasq
func UpsertBridgeStaticHost(bridgeName, vmName, mac, ipAddr string) error {
	dhcpHostsMutex.Lock()
	defer dhcpHostsMutex.Unlock()

	bridgeName = strings.TrimSpace(bridgeName)
	vmName = strings.TrimSpace(vmName)
	mac = strings.ToLower(strings.TrimSpace(mac))
	ipAddr = strings.TrimSpace(ipAddr)

	// 校验唯一性
	if err := validateStaticHostUniqueness(bridgeName, mac, ipAddr, vmName); err != nil {
		logger.App.Warn("DHCP 静态绑定唯一性校验失败", "bridge", bridgeName, "vm", vmName, "error", err)
		return err
	}

	if model.DB == nil {
		// 降级模式：直接操作文件
		return upsertBridgeStaticHostInFile(bridgeName, vmName, mac, ipAddr)
	}

	// 查找现有记录（按 VMName 或 MAC）
	var existing model.BridgeStaticHostDB
	result := model.DB.Where("bridge_name = ? AND (vm_name = ? OR mac = ?)", bridgeName, vmName, mac).First(&existing)

	if result.Error == nil {
		// 更新现有记录
		existing.IP = ipAddr
		existing.MAC = mac
		existing.VMName = vmName
		if err := model.DB.Save(&existing).Error; err != nil {
			return fmt.Errorf("更新 DHCP 静态绑定失败: %w", err)
		}
		logger.App.Info("更新 DHCP 静态绑定", "bridge", bridgeName, "vm", vmName, "mac", mac, "ip", ipAddr)
	} else {
		// 新增记录
		newHost := model.BridgeStaticHostDB{
			BridgeName: bridgeName,
			VMName:     vmName,
			MAC:        mac,
			IP:         ipAddr,
		}
		if err := model.DB.Create(&newHost).Error; err != nil {
			return fmt.Errorf("新增 DHCP 静态绑定失败: %w", err)
		}
		logger.App.Info("新增 DHCP 静态绑定", "bridge", bridgeName, "vm", vmName, "mac", mac, "ip", ipAddr)
	}

	// 同步到文件
	if err := syncBridgeStaticHostsToFile(bridgeName); err != nil {
		logger.App.Warn("同步 DHCP 静态绑定到文件失败", "bridge", bridgeName, "error", err)
	}

	// Reload dnsmasq
	if HookReloadBridgeDNSMasq != nil {
		if err := HookReloadBridgeDNSMasq(bridgeName); err != nil {
			logger.App.Warn("Reload dnsmasq 失败", "bridge", bridgeName, "error", err)
		}
	}

	return nil
}

// upsertBridgeStaticHostInFile 降级模式：直接操作文件
func upsertBridgeStaticHostInFile(bridgeName, vmName, mac, ipAddr string) error {
	hosts, err := listBridgeStaticHostsFromFile(bridgeName)
	if err != nil {
		return err
	}
	found := false
	for i, host := range hosts {
		if host.MAC == mac || host.VMName == vmName {
			hosts[i] = BridgeStaticHost{
				VMName: vmName,
				MAC:    mac,
				IP:     ipAddr,
			}
			found = true
			break
		}
	}
	if !found {
		hosts = append(hosts, BridgeStaticHost{
			VMName: vmName,
			MAC:    mac,
			IP:     ipAddr,
		})
	}
	return writeBridgeStaticHostsFile(bridgeName, hosts)
}

// RemoveBridgeStaticHost 删除 DHCP 静态绑定
// 流程：加锁 → 从数据库删除 → 同步文件 → reload dnsmasq
func RemoveBridgeStaticHost(bridgeName, vmName, mac string) (string, error) {
	dhcpHostsMutex.Lock()
	defer dhcpHostsMutex.Unlock()

	bridgeName = strings.TrimSpace(bridgeName)
	vmName = strings.TrimSpace(vmName)
	mac = strings.ToLower(strings.TrimSpace(mac))

	if model.DB == nil {
		// 降级模式：直接操作文件
		return removeBridgeStaticHostFromFile(bridgeName, vmName, mac)
	}

	var removedIP string
	// 从数据库删除
	result := model.DB.Where("bridge_name = ? AND (vm_name = ? OR mac = ?)", bridgeName, vmName, mac).Delete(&model.BridgeStaticHostDB{})
	if result.Error != nil {
		return "", fmt.Errorf("删除 DHCP 静态绑定失败: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		// 获取被删除的 IP（用于日志）
		var host model.BridgeStaticHostDB
		model.DB.Unscoped().Where("bridge_name = ? AND (vm_name = ? OR mac = ?)", bridgeName, vmName, mac).First(&host)
		removedIP = host.IP
		logger.App.Info("删除 DHCP 静态绑定", "bridge", bridgeName, "vm", vmName, "mac", mac, "ip", removedIP)
	}

	// 同步到文件
	if err := syncBridgeStaticHostsToFile(bridgeName); err != nil {
		logger.App.Warn("同步 DHCP 静态绑定到文件失败", "bridge", bridgeName, "error", err)
	}

	// Reload dnsmasq
	if HookReloadBridgeDNSMasq != nil {
		if err := HookReloadBridgeDNSMasq(bridgeName); err != nil {
			logger.App.Warn("Reload dnsmasq 失败", "bridge", bridgeName, "error", err)
		}
	}

	return removedIP, nil
}

// removeBridgeStaticHostFromFile 降级模式：直接操作文件
func removeBridgeStaticHostFromFile(bridgeName, vmName, mac string) (string, error) {
	hosts, err := listBridgeStaticHostsFromFile(bridgeName)
	if err != nil {
		return "", err
	}
	var removedIP string
	var next []BridgeStaticHost
	for _, host := range hosts {
		if host.MAC == mac || host.VMName == vmName {
			removedIP = host.IP
			continue
		}
		next = append(next, host)
	}
	if err := writeBridgeStaticHostsFile(bridgeName, next); err != nil {
		return "", err
	}
	return removedIP, nil
}

// syncBridgeStaticHostsToFile 从数据库同步静态绑定到 dhcp-hosts 文件
func syncBridgeStaticHostsToFile(bridgeName string) error {
	hosts, err := ListBridgeStaticHosts(bridgeName)
	if err != nil {
		return err
	}
	return writeBridgeStaticHostsFile(bridgeName, hosts)
}

// MigrateStaticHostsFromFile 将已有的 dhcp-hosts 文件迁移到数据库
// 在网桥创建或恢复时调用一次即可
func MigrateStaticHostsFromFile(bridgeName string) error {
	if model.DB == nil {
		return nil
	}
	bridgeName = strings.TrimSpace(bridgeName)
	path := bridgeDHCPHostsPath(bridgeName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // 文件不存在，无需迁移
	}
	// 检查数据库中是否已有记录
	var count int64
	model.DB.Model(&model.BridgeStaticHostDB{}).Where("bridge_name = ?", bridgeName).Count(&count)
	if count > 0 {
		return nil // 已有记录，跳过迁移
	}
	// 从文件读取
	hosts, err := listBridgeStaticHostsFromFile(bridgeName)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return nil
	}
	// 批量导入数据库
	for _, host := range hosts {
		dbHost := model.BridgeStaticHostDB{
			BridgeName: bridgeName,
			VMName:     host.VMName,
			MAC:        host.MAC,
			IP:         host.IP,
		}
		if err := model.DB.Create(&dbHost).Error; err != nil {
			logger.App.Warn("迁移 DHCP 静态绑定失败", "bridge", bridgeName, "host", host, "error", err)
			continue
		}
	}
	logger.App.Info("从文件迁移 DHCP 静态绑定到数据库", "bridge", bridgeName, "count", len(hosts))
	return nil
}

// SyncAllBridgeStaticHosts 同步所有网桥的静态绑定到文件（用于启动时恢复）
func SyncAllBridgeStaticHosts() error {
	if model.DB == nil {
		return nil
	}
	var bridges []model.NetworkBridge
	if err := model.DB.Find(&bridges).Error; err != nil {
		return err
	}
	for _, bridge := range bridges {
		if bridge.Mode == BridgeModeDirect || bridge.Mode == BridgeModePureL2 {
			// 先从文件迁移到数据库（如果需要）
			if err := MigrateStaticHostsFromFile(bridge.Name); err != nil {
				logger.App.Warn("迁移网桥 DHCP 静态绑定失败", "bridge", bridge.Name, "error", err)
			}
			// 从数据库同步到文件
			if err := syncBridgeStaticHostsToFile(bridge.Name); err != nil {
				logger.App.Warn("同步网桥 DHCP 静态绑定到文件失败", "bridge", bridge.Name, "error", err)
			}
		}
	}
	return nil
}

// ListBridgeStaticHostsForBridge 向后兼容的包装函数
// 保留此函数名供其他模块调用
func ListBridgeStaticHostsForBridge(bridgeName string) ([]BridgeStaticHost, error) {
	return ListBridgeStaticHosts(bridgeName)
}

// writeBridgeStaticHosts 向后兼容的包装函数
func writeBridgeStaticHosts(bridgeName string, hosts []BridgeStaticHost) error {
	return writeBridgeStaticHostsFile(bridgeName, hosts)
}

// EnsureBridgeStaticHostsFile 确保 dhcp-hosts 文件与数据库同步
// 在 dnsmasq 启动前调用
func EnsureBridgeStaticHostsFile(bridgeName string) error {
	// 确保目录存在
	if err := os.MkdirAll(bridgeConfigDir, 0755); err != nil {
		return fmt.Errorf("创建网桥配置目录失败: %w", err)
	}
	// 从文件迁移到数据库（如果需要）
	if err := MigrateStaticHostsFromFile(bridgeName); err != nil {
		logger.App.Warn("迁移 DHCP 静态绑定失败", "bridge", bridgeName, "error", err)
	}
	// 从数据库同步到文件
	return syncBridgeStaticHostsToFile(bridgeName)
}

// GenerateUniqueIP 为 VM 生成唯一 IP（在 DHCP 范围内）
// 返回的 IP 保证不与现有绑定冲突
func GenerateUniqueIP(bridgeName, dhcpStart, dhcpEnd string) (string, error) {
	dhcpHostsMutex.Lock()
	defer dhcpHostsMutex.Unlock()

	startIP := net.ParseIP(strings.TrimSpace(dhcpStart))
	if startIP == nil {
		return "", fmt.Errorf("解析起始 IP 失败: %s", dhcpStart)
	}
	endIP := net.ParseIP(strings.TrimSpace(dhcpEnd))
	if endIP == nil {
		return "", fmt.Errorf("解析结束 IP 失败: %s", dhcpEnd)
	}
	// 获取所有已分配的 IP
	usedIPs := make(map[string]bool)
	if model.DB != nil {
		var hosts []model.BridgeStaticHostDB
		if err := model.DB.Where("bridge_name = ?", bridgeName).Find(&hosts).Error; err != nil {
			return "", err
		}
		for _, h := range hosts {
			usedIPs[h.IP] = true
		}
	} else {
		hosts, err := listBridgeStaticHostsFromFile(bridgeName)
		if err != nil {
			return "", err
		}
		for _, h := range hosts {
			usedIPs[h.IP] = true
		}
	}
	// 从起始 IP 开始遍历，找到第一个未使用的 IP
	current := make([]byte, len(startIP))
	copy(current, startIP)
	for {
		ipStr := net.IP(current).String()
		if !usedIPs[ipStr] {
			return ipStr, nil
		}
		// 递增 IP
		incrementIPBytes(current)
		if net.IP(current).Equal(endIP) {
			break
		}
	}
	return "", fmt.Errorf("IP 范围 %s - %s 已耗尽", dhcpStart, dhcpEnd)
}

// incrementIPBytes 递增 IP 字节数组
func incrementIPBytes(ip []byte) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] > 0 {
			break
		}
	}
}

func ListBridgeDHCPLeases(bridgeName string) ([]BridgeDHCPLease, error) {
	path := bridgeDHCPLeasesPath(bridgeName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []BridgeDHCPLease{}, nil
		}
		return nil, err
	}
	return parseBridgeDHCPLeasesText(string(data)), nil
}

func parseBridgeDHCPLeasesText(text string) []BridgeDHCPLease {
	var leases []BridgeDHCPLease
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		lease := BridgeDHCPLease{
			MAC: strings.ToLower(fields[1]),
			IP:  fields[2],
		}
		if len(fields) >= 4 && fields[3] != "*" {
			lease.Hostname = fields[3]
		}
		leases = append(leases, lease)
	}
	return leases
}

func RemoveBridgeDHCPLease(bridgeName, vmName, mac string) (string, error) {
	path := bridgeDHCPLeasesPath(bridgeName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	var removedIP string
	var remaining []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			remaining = append(remaining, line)
			continue
		}
		lineMAC := strings.ToLower(fields[1])
		lineHostname := ""
		if len(fields) >= 4 && fields[3] != "*" {
			lineHostname = fields[3]
		}
		if lineMAC == mac || lineHostname == vmName {
			removedIP = fields[2]
			continue
		}
		remaining = append(remaining, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(remaining, "\n")+"\n"), 0644); err != nil {
		return "", err
	}
	return removedIP, nil
}
