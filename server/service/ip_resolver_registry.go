package service

// ip_resolver 回调注册：避免 ip_resolver → service 循环依赖
// ip_resolver 包通过 VPCCallbacks 注入 service 层函数，service 包在此注册。
import (
	"strings"

	"kvm_console/service/ip_resolver"
	netpkg "kvm_console/service/network"
)

func init() {
	ip_resolver.SetResolverCallbacks(ip_resolver.VPCCallbacks{
		GetVPCSwitchForVM:          getVPCSwitchForVM,
		GetVPCLeaseIPForVM:         GetVPCLeaseIPForVM,
		GetOVSLeaseIPByMAC:         GetOVSLeaseIPByMAC,
		GetOVSStaticIPByMAC:        GetOVSStaticIPByMAC,
		GetVPCStaticIPByMACAndCIDR: getVPCStaticIPByMACAndCIDR,
	})
}

// getVPCStaticIPByMACAndCIDR 在所有 VPC 静态绑定中按 MAC+CIDR 查找 IP
// 原 getVPCStaticIPFromAllHostsByMAC（已从 libvirt.go 移至 ip_resolver 的回调）
func getVPCStaticIPByMACAndCIDR(mac, cidr string) string {
	hosts, err := ListAllVPCStaticHosts()
	if err == nil {
		for _, host := range hosts {
			if strings.EqualFold(host.MAC, mac) && strings.TrimSpace(host.IP) != "" {
				if cidr == "" || IPInCIDR(host.IP, cidr) {
					return host.IP
				}
			}
		}
	}
	// 桥接模式：查找所有桥接网桥的静态绑定
	if netpkg.HookGetBridgeIPByMAC != nil {
		if ip := netpkg.HookGetBridgeIPByMAC(mac); ip != "" {
			if cidr == "" || IPInCIDR(ip, cidr) {
				return ip
			}
		}
	}
	return ""
}
