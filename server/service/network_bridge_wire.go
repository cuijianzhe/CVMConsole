package service

import (
	"kvm_console/model"
	"kvm_console/service/clone"
	netpkg "kvm_console/service/network"
	bridge "kvm_console/service/network/bridge"
	vpc "kvm_console/service/network/vpc"
	"path/filepath"
	"strings"
)

// init wires network/bridge package function variables to service root implementations.
// This breaks the circular dependency: bridge package cannot import service,
// so it exposes function variables that we set here.
func init() {
	bridge.HookEnsureOVSNetworkReady = EnsureOVSNetworkReady
	bridge.HookEnsureAllVPCSwitchRuntime = EnsureAllVPCSwitchRuntime
	bridge.HookWriteFileIfChanged = writeFileIfChanged
	bridge.HookOvsBridgeName = ovsBridgeName

	netpkg.HookListBridgeStaticHosts = listBridgeStaticHostsHook
	netpkg.HookListBridgeDHCPLeases = listBridgeDHCPLeasesHook
	netpkg.HookUpsertBridgeStaticHost = upsertBridgeStaticHostHook
	netpkg.HookRemoveBridgeStaticHost = removeBridgeStaticHostHook
	netpkg.HookRemoveBridgeDHCPLease = removeBridgeDHCPLeaseHook
	netpkg.HookReloadBridgeDNSMasq = reloadBridgeDNSMasqHook
	netpkg.HookGetBridgeIPByMAC = getBridgeIPByMACHook

	vpc.HookStartBridgeDNSMasq = bridge.HookStartBridgeDNSMasq
	vpc.HookRemoveBridgeDHCPLease = bridge.RemoveBridgeDHCPLease
}

func listBridgeStaticHostsHook(bridgeName string) ([]netpkg.OVSStaticHost, error) {
	hosts, err := bridge.ListBridgeStaticHosts(bridgeName)
	if err != nil {
		return nil, err
	}
	result := make([]netpkg.OVSStaticHost, len(hosts))
	for i, h := range hosts {
		result[i] = netpkg.OVSStaticHost{VMName: h.VMName, MAC: h.MAC, IP: h.IP}
	}
	return result, nil
}

func listBridgeDHCPLeasesHook(bridgeName string) ([]netpkg.OVSDHCPLease, error) {
	leases, err := bridge.ListBridgeDHCPLeases(bridgeName)
	if err != nil {
		return nil, err
	}
	result := make([]netpkg.OVSDHCPLease, len(leases))
	for i, l := range leases {
		result[i] = netpkg.OVSDHCPLease{MAC: l.MAC, IP: l.IP, Hostname: l.Hostname}
	}
	return result, nil
}

func upsertBridgeStaticHostHook(bridgeName, vmName, mac, ipAddr string) error {
	return bridge.UpsertBridgeStaticHost(bridgeName, vmName, mac, ipAddr)
}

func removeBridgeStaticHostHook(bridgeName, vmName, mac string) (string, error) {
	return bridge.RemoveBridgeStaticHost(bridgeName, vmName, mac)
}

func removeBridgeDHCPLeaseHook(bridgeName, vmName, mac string) (string, error) {
	return bridge.RemoveBridgeDHCPLease(bridgeName, vmName, mac)
}

func reloadBridgeDNSMasqHook(bridgeName string) error {
	return bridge.HookReloadBridgeDNSMasq(bridgeName)
}

// getBridgeIPByMACHook 遍历所有桥接网桥，通过 MAC 地址查找静态绑定 IP
// 不依赖数据库，直接扫描网桥配置目录下的 dhcp-hosts-* 文件
func getBridgeIPByMACHook(mac string) string {
	if strings.TrimSpace(mac) == "" {
		return ""
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	bridgeConfigDir := "/etc/kvm-console/bridges"
	matches, err := filepath.Glob(filepath.Join(bridgeConfigDir, "dhcp-hosts-*"))
	if err != nil {
		return ""
	}
	for _, path := range matches {
		fileName := filepath.Base(path)
		bridgeName := strings.TrimPrefix(fileName, "dhcp-hosts-")
		if bridgeName == "" {
			continue
		}
		hosts, err := bridge.ListBridgeStaticHosts(bridgeName)
		if err != nil {
			continue
		}
		for _, host := range hosts {
			if strings.EqualFold(host.MAC, mac) && strings.TrimSpace(host.IP) != "" {
				return host.IP
			}
		}
	}
	return ""
}

// ── Type aliases ──

type HostInterfaceInfo = bridge.HostInterfaceInfo
type NetworkBridgeInfo = bridge.NetworkBridgeInfo
type NetworkBridgeRequest = bridge.NetworkBridgeRequest
type InterfaceConfigInfo = bridge.InterfaceConfigInfo
type SetInterfaceConfigRequest = bridge.SetInterfaceConfigRequest

// ── Constant aliases ──

const BridgeModeNAT = bridge.BridgeModeNAT

// ── Exported delegates (used by handler and other service files) ──

// ListHostPhysicalInterfaces delegates to bridge.ListHostPhysicalInterfaces
func ListHostPhysicalInterfaces() ([]HostInterfaceInfo, error) {
	return bridge.ListHostPhysicalInterfaces()
}

// ListNetworkBridges delegates to bridge.ListNetworkBridges
func ListNetworkBridges() ([]NetworkBridgeInfo, error) {
	return bridge.ListNetworkBridges()
}

// CreateNetworkBridge delegates to bridge.CreateNetworkBridge
func CreateNetworkBridge(req NetworkBridgeRequest) (*model.NetworkBridge, error) {
	return bridge.CreateNetworkBridge(req)
}

// DeleteNetworkBridge delegates to bridge.DeleteNetworkBridge
func DeleteNetworkBridge(id uint) error {
	return bridge.DeleteNetworkBridge(id)
}

// DeleteNetworkBridgeByName delegates to bridge.DeleteNetworkBridgeByName
func DeleteNetworkBridgeByName(name string) error {
	return bridge.DeleteNetworkBridgeByName(name)
}

// GetInterfaceConfig delegates to bridge.GetInterfaceConfig
func GetInterfaceConfig(name string) (*InterfaceConfigInfo, error) {
	return bridge.GetInterfaceConfig(name)
}

// SetInterfaceConfig delegates to bridge.SetInterfaceConfig
func SetInterfaceConfig(req SetInterfaceConfigRequest) (*InterfaceConfigInfo, error) {
	return bridge.SetInterfaceConfig(req)
}

// EnsureAllNetworkBridgesRuntime delegates to bridge.EnsureAllNetworkBridgesRuntime
func EnsureAllNetworkBridgesRuntime() error {
	return bridge.EnsureAllNetworkBridgesRuntime()
}

// EnsureOVSBridgeDirect delegates to bridge.EnsureOVSBridgeDirect
func EnsureOVSBridgeDirect(bridgeName, uplink string, migrateHostIP bool, cfg bridge.HostIPConfig) error {
	return bridge.EnsureOVSBridgeDirect(bridgeName, uplink, migrateHostIP, cfg)
}

// BridgeModeForSwitch delegates to bridge.BridgeModeForSwitch
func BridgeModeForSwitch(sw model.VPCSwitch) string {
	return bridge.BridgeModeForSwitch(sw)
}

// BridgeNameForSwitch delegates to bridge.BridgeNameForSwitch
func BridgeNameForSwitch(sw model.VPCSwitch) string {
	return bridge.BridgeNameForSwitch(sw)
}

// SwitchUsesDirectBridge delegates to bridge.SwitchUsesDirectBridge
func SwitchUsesDirectBridge(sw model.VPCSwitch) bool {
	return bridge.SwitchUsesDirectBridge(sw)
}

// ListBridgeStaticHosts delegates to bridge.ListBridgeStaticHosts (exported for clone package)
func ListBridgeStaticHosts(bridgeName string) ([]clone.NetworkBridgeStaticHost, error) {
	hosts, err := bridge.ListBridgeStaticHosts(bridgeName)
	if err != nil {
		return nil, err
	}
	result := make([]clone.NetworkBridgeStaticHost, len(hosts))
	for i, h := range hosts {
		result[i] = clone.NetworkBridgeStaticHost{VMName: h.VMName, MAC: h.MAC, IP: h.IP}
	}
	return result, nil
}

// BuildOVSInterfaceXMLForBridge delegates to bridge.BuildOVSInterfaceXMLForBridge
func BuildOVSInterfaceXMLForBridge(mac, modelName, bridgeName string) string {
	return bridge.BuildOVSInterfaceXMLForBridge(mac, modelName, bridgeName)
}

// BuildOVSVirtInstallNetworkArgForBridge delegates to bridge.BuildOVSVirtInstallNetworkArgForBridge
func BuildOVSVirtInstallNetworkArgForBridge(modelName, bridgeName string) string {
	return bridge.BuildOVSVirtInstallNetworkArgForBridge(modelName, bridgeName)
}

// ── Unexported delegates (used internally by service root package) ──

// normalizeBridgeMode delegates to bridge.NormalizeBridgeMode
// Kept unexported for backward compatibility with vpc_register.go
func normalizeBridgeMode(mode string) string {
	return bridge.NormalizeBridgeMode(mode)
}
