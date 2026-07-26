package disk

import (
	"fmt"
	"strconv"
	"strings"

	"kvm_console/service/libvirt_rpc"

	"github.com/digitalocean/go-libvirt"
)

// DiskIOPSTune holds disk IOPS limit configuration.
type DiskIOPSTune struct {
	TotalIopsSec int `json:"total_iops_sec"` // total IOPS limit (0 = unlimited)
	ReadIopsSec  int `json:"read_iops_sec"`  // read IOPS limit (0 = unlimited)
	WriteIopsSec int `json:"write_iops_sec"` // write IOPS limit (0 = unlimited)
}

// SetDiskIOPSTune sets IOPS limits for a VM disk (live and persistent).
// vmName: VM name, dev: disk device name (e.g. vda)
// iops: IOPS limit config, nil to clear limits.
func SetDiskIOPSTune(vmName, dev string, iops *DiskIOPSTune) error {
	if err := EnsureNotMigrating(vmName, "设置磁盘IOPS限制"); err != nil {
		return err
	}

	vmState, _ := libvirt_rpc.GetDomainStateRPC(vmName)
	totalIops := 0
	readIops := 0
	writeIops := 0
	if iops != nil {
		totalIops = iops.TotalIopsSec
		readIops = iops.ReadIopsSec
		writeIops = iops.WriteIopsSec
	}

	// build TypedParam list
	// libvirt does not allow total_iops_sec and read/write_iops_sec to be set simultaneously
	// 注意：libvirt 的 blkdeviotune 参数期望 ullong (uint64) 类型
	var params []libvirt.TypedParam
	if totalIops > 0 {
		if readIops > 0 || writeIops > 0 {
			return fmt.Errorf("总 IOPS 与读/写 IOPS 不能同时设置，请只设置其中一种")
		}
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneTotalIopsSec,
			Value: *libvirt.NewTypedParamValueUllong(uint64(totalIops)),
		})
	} else {
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneReadIopsSec,
			Value: *libvirt.NewTypedParamValueUllong(uint64(readIops)),
		})
		params = append(params, libvirt.TypedParam{
			Field: libvirt.DomainBlockIotuneWriteIopsSec,
			Value: *libvirt.NewTypedParamValueUllong(uint64(writeIops)),
		})
	}

	var tuneFlags uint32 = 2 // VIR_DOMAIN_AFFECT_CONFIG
	if vmState == "running" {
		tuneFlags = 3 // VIR_DOMAIN_AFFECT_LIVE | VIR_DOMAIN_AFFECT_CONFIG
	}
	if err := libvirt_rpc.SetBlkIOParametersRPC(vmName, dev, params, tuneFlags); err != nil {
		return fmt.Errorf("设置磁盘 IOPS 限制失败: %w", err)
	}

	return nil
}

// GetDiskIOPSTune retrieves IOPS settings for a disk from libvirt.
func GetDiskIOPSTune(vmName, dev string) (*DiskIOPSTune, error) {
	params, err := libvirt_rpc.GetBlkIOParametersRPC(vmName, dev, 0)
	if err != nil {
		return nil, fmt.Errorf("获取磁盘 IOPS 信息失败: %w", err)
	}

	iops := &DiskIOPSTune{}
	for _, p := range params {
		switch p.Field {
		case libvirt.DomainBlockIotuneTotalIopsSec:
			iops.TotalIopsSec = typedParamToInt(p.Value)
		case libvirt.DomainBlockIotuneReadIopsSec:
			iops.ReadIopsSec = typedParamToInt(p.Value)
		case libvirt.DomainBlockIotuneWriteIopsSec:
			iops.WriteIopsSec = typedParamToInt(p.Value)
		}
	}

	return iops, nil
}

// typedParamToInt 从 TypedParamValue 中提取整数，兼容 ullong/uint/llong/int 等类型
func typedParamToInt(v libvirt.TypedParamValue) int {
	switch val := v.I.(type) {
	case uint64:
		return int(val)
	case int64:
		return int(val)
	case uint32:
		return int(val)
	case int32:
		return int(val)
	}
	return 0
}

// ParseAllDiskIOPSTune parses IOPS configuration for all disks from VM XML.
func ParseAllDiskIOPSTune(vmName string) map[string]*DiskIOPSTune {
	result := make(map[string]*DiskIOPSTune)

	xmlStr, err := libvirt_rpc.GetDomainXMLRPC(vmName, 0)
	if err != nil {
		return result
	}

	lines := strings.Split(xmlStr, "\n")
	var currentDev string
	inDisk := false
	inIOTune := false
	var currentIOPS *DiskIOPSTune

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "<disk ") {
			inDisk = true
			inIOTune = false
			currentDev = ""
			currentIOPS = nil
		}

		if inDisk {
			if strings.Contains(trimmed, "<target") && strings.Contains(trimmed, "dev='") {
				parts := strings.Split(trimmed, "dev='")
				if len(parts) > 1 {
					currentDev = strings.Split(parts[1], "'")[0]
				}
			}

			if strings.HasPrefix(trimmed, "<iotune>") {
				inIOTune = true
				currentIOPS = &DiskIOPSTune{}
			}
			if inIOTune && currentIOPS != nil {
				if strings.Contains(trimmed, "total_iops_sec") {
					currentIOPS.TotalIopsSec = parseIOPSElement(trimmed, "total_iops_sec")
				}
				if strings.Contains(trimmed, "read_iops_sec") {
					currentIOPS.ReadIopsSec = parseIOPSElement(trimmed, "read_iops_sec")
				}
				if strings.Contains(trimmed, "write_iops_sec") {
					currentIOPS.WriteIopsSec = parseIOPSElement(trimmed, "write_iops_sec")
				}
			}
			if strings.HasPrefix(trimmed, "</iotune>") {
				inIOTune = false
			}

			if strings.Contains(trimmed, "</disk>") {
				if currentDev != "" && currentIOPS != nil {
					if currentIOPS.TotalIopsSec > 0 || currentIOPS.ReadIopsSec > 0 || currentIOPS.WriteIopsSec > 0 {
						result[currentDev] = currentIOPS
					}
				}
				inDisk = false
				inIOTune = false
			}
		}
	}

	return result
}

// parseIOPSElement parses a numeric value from an iotune XML line.
func parseIOPSElement(line, elementName string) int {
	parts := strings.Split(line, elementName)
	if len(parts) > 1 {
		rest := parts[1]
		rest = strings.TrimSpace(rest)
		rest = strings.TrimPrefix(rest, ">")
		if idx := strings.Index(rest, "<"); idx >= 0 {
			rest = rest[:idx]
		}
		if val, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
			return val
		}
	}
	return 0
}
