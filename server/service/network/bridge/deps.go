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
		if len(fields) >= 3 {
			hosts = append(hosts, BridgeStaticHost{
				MAC:  strings.ToLower(strings.TrimSpace(fields[0])),
				IP:   strings.TrimSpace(fields[1]),
				VMName: strings.TrimSpace(fields[2]),
			})
		}
	}
	return hosts, nil
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
			MAC:        strings.ToLower(fields[1]),
			IP:         fields[2],
		}
		if len(fields) >= 4 && fields[3] != "*" {
			lease.Hostname = fields[3]
		}
		leases = append(leases, lease)
	}
	return leases
}
