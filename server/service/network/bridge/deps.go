package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	HookReloadBridgeDNSMasq    func(bridgeName string) error
)

type BridgeStaticHost struct {
	VMName string
	MAC    string
	IP     string
}

type BridgeDHCPLease struct {
	ExpiryTime string
	ExpiryUnix int64
	MAC        string
	IP         string
	Hostname   string
	ClientID   string
}

func bridgeDHCPHostsPath(bridgeName string) string {
	return filepath.Join(bridgeConfigDir, fmt.Sprintf("dhcp-hosts-%s", bridgeName))
}

func bridgeDHCPLeasesPath(bridgeName string) string {
	return filepath.Join(bridgeConfigDir, fmt.Sprintf("leases-%s", bridgeName))
}

func ListBridgeStaticHosts(bridgeName string) ([]BridgeStaticHost, error) {
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
				IP:  strings.TrimSpace(fields[1]),
			}
			if len(fields) >= 3 {
				host.VMName = strings.TrimSpace(fields[2])
			}
			hosts = append(hosts, host)
		}
	}
	return hosts, nil
}

func writeBridgeStaticHosts(bridgeName string, hosts []BridgeStaticHost) error {
	path := bridgeDHCPHostsPath(bridgeName)
	var lines []string
	for _, host := range hosts {
		line := fmt.Sprintf("%s,%s,%s", host.MAC, host.IP, host.VMName)
		lines = append(lines, line)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func UpsertBridgeStaticHost(bridgeName, vmName, mac, ipAddr string) error {
	hosts, err := ListBridgeStaticHosts(bridgeName)
	if err != nil {
		return err
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
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
	return writeBridgeStaticHosts(bridgeName, hosts)
}

func RemoveBridgeStaticHost(bridgeName, vmName, mac string) (string, error) {
	hosts, err := ListBridgeStaticHosts(bridgeName)
	if err != nil {
		return "", err
	}
	mac = strings.ToLower(strings.TrimSpace(mac))
	var removedIP string
	var next []BridgeStaticHost
	for _, host := range hosts {
		if host.MAC == mac || host.VMName == vmName {
			removedIP = host.IP
			continue
		}
		next = append(next, host)
	}
	if err := writeBridgeStaticHosts(bridgeName, next); err != nil {
		return "", err
	}
	return removedIP, nil
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
