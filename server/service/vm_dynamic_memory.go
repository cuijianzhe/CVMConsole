package service

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"kvm_console/logger"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"kvm_console/config"
	"kvm_console/model"
	"kvm_console/utils"
)

const (
	vmMemoryMetadataURI = "https://kvm-console.local/domain-memory"
	vmMemoryMetadataKey = "kvm-console-memory"

	memoryCompatLegacyStatic = "legacy_static"
	memoryCompatDynamic      = "dynamic"
	memoryCompatPending      = "pending_apply"

	memoryBackendBalloon   = "balloon"
	memoryBackendVirtioMem = "virtio_mem"

	schedulerGroupDynamicMemory         = "动态内存"
	schedulerKeyDynamicMemoryBalloon    = "dynamic_memory_balloon"
	schedulerKeyDynamicMemoryVirtioMem  = "dynamic_memory_virtio_mem"
	schedulerNameDynamicMemoryBalloon   = "气球调度"
	schedulerNameDynamicMemoryVirtioMem = "Windows 弹性内存调度"
)

// VMMemoryDynamicRequest 是创建/编辑时提交的动态内存配置，单位为 GB。
type VMMemoryDynamicRequest struct {
	DynamicEnabled *bool  `json:"dynamic_enabled,omitempty"`
	MemoryBackend  string `json:"memory_backend,omitempty"`
	MemoryInitial  int    `json:"memory_initial,omitempty"`
	MemoryMin      int    `json:"memory_min,omitempty"`
	MemoryMax      int    `json:"memory_max,omitempty"`
	AutoBalloon    *bool  `json:"memory_auto_balloon,omitempty"`
	MemoryCurrent  int    `json:"memory_current,omitempty"`
}

// VMMemoryDynamicInfo 是接口返回的动态内存状态，单位为 MB。
type VMMemoryDynamicInfo struct {
	DynamicEnabled   bool   `json:"memory_dynamic_enabled"`
	MemoryBackend    string `json:"memory_backend"`
	MemoryInitial    int    `json:"memory_initial"`
	MemoryMin        int    `json:"memory_min"`
	MemoryMax        int    `json:"memory_max"`
	VirtioMemCurrent int    `json:"memory_virtio_mem_current"`
	AutoBalloon      bool   `json:"memory_auto_balloon"`
	PendingApply     bool   `json:"memory_pending_apply"`
	CompatMode       string `json:"memory_compat_mode"`
	BalloonSupported bool   `json:"memory_balloon_supported"`
	BalloonStatus    string `json:"memory_balloon_status"`
	ObservationUntil int64  `json:"memory_observation_until"`
	ManualPauseUntil int64  `json:"memory_manual_pause_until"`
}

type vmMemoryMetadata struct {
	Version          int    `json:"version"`
	DynamicEnabled   bool   `json:"dynamic_enabled"`
	MemoryBackend    string `json:"memory_backend,omitempty"`
	MemoryInitialMB  int    `json:"memory_initial_mb"`
	MemoryMinMB      int    `json:"memory_min_mb"`
	MemoryMaxMB      int    `json:"memory_max_mb"`
	AutoBalloon      bool   `json:"auto_balloon"`
	PendingApply     bool   `json:"pending_apply"`
	ObservationUntil int64  `json:"observation_until"`
	ManualPauseUntil int64  `json:"manual_pause_until"`
	UpdatedAt        int64  `json:"updated_at"`
}

type vmMemoryMetadataXML struct {
	XMLName xml.Name `xml:"memoryConfig"`
	XMLNS   string   `xml:"xmlns,attr,omitempty"`
	Data    string   `xml:",chardata"`
}

type vmMemoryXMLValues struct {
	MemoryMB        int
	CurrentMemoryMB int
}

type vmMemoryStatsValues struct {
	ActualKB    int64
	UnusedKB    int64
	UsableKB    int64
	AvailableKB int64
	RSSKB       int64
}

var memorySchedulerState = struct {
	sync.Mutex
	lastAdjust map[string]time.Time
	lowUsable  map[string]int
	highUnused map[string]int
}{lastAdjust: make(map[string]time.Time), lowUsable: make(map[string]int), highUnused: make(map[string]int)}

var dynamicMemorySchedulerRegisterOnce sync.Once

// BuildVMMemoryMetadataForCreate 生成新建 VM 的动态内存 metadata 与 XML 参数。
func BuildVMMemoryMetadataForCreate(ramGB int, req *VMMemoryDynamicRequest) (*vmMemoryMetadata, int, int, error) {
	initialGB := ramGB
	if req != nil && req.MemoryInitial > 0 {
		initialGB = req.MemoryInitial
	}
	if initialGB <= 0 {
		return nil, 0, 0, fmt.Errorf("启动内存必须大于 0")
	}

	enabled := false
	if req != nil && req.DynamicEnabled != nil {
		enabled = *req.DynamicEnabled
	}
	if !enabled {
		staticMB := initialGB * 1024
		return nil, staticMB, staticMB, nil
	}

	backend := normalizeMemoryBackend(req.MemoryBackend)
	if backend == memoryBackendVirtioMem && (req == nil || req.MemoryInitial <= 0) {
		initialGB = defaultDynamicMemoryMinGB(ramGB)
	}
	maxGB := defaultDynamicMemoryMaxGB(initialGB)
	if backend == memoryBackendVirtioMem {
		maxGB = defaultDynamicMemoryMaxGB(ramGB)
	}
	if req.MemoryMax > 0 {
		maxGB = req.MemoryMax
	}
	minGB := defaultDynamicMemoryMinGB(initialGB)
	if req.MemoryMin > 0 {
		minGB = req.MemoryMin
	}
	autoBalloon := true
	if req.AutoBalloon != nil {
		autoBalloon = *req.AutoBalloon
	}
	if backend == memoryBackendVirtioMem {
		minGB = initialGB
		autoBalloon = false
	}

	meta, err := newVMMemoryMetadataForBackend(initialGB*1024, minGB*1024, maxGB*1024, autoBalloon, false, backend)
	if err != nil {
		return nil, 0, 0, err
	}
	return meta, meta.MemoryInitialMB, meta.MemoryMaxMB, nil
}

func normalizeMemoryBackend(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case memoryBackendVirtioMem:
		return memoryBackendVirtioMem
	default:
		return memoryBackendBalloon
	}
}

func defaultDynamicMemoryMinGB(initialGB int) int {
	minGB := initialGB / 2
	if minGB < 1 {
		minGB = 1
	}
	return minGB
}

func defaultDynamicMemoryMaxGB(initialGB int) int {
	maxGB := (initialGB*13 + 9) / 10
	if maxGB < initialGB {
		maxGB = initialGB
	}
	return maxGB
}

func newVMMemoryMetadata(initialMB, minMB, maxMB int, autoBalloon bool, pending bool) (*vmMemoryMetadata, error) {
	return newVMMemoryMetadataForBackend(initialMB, minMB, maxMB, autoBalloon, pending, memoryBackendBalloon)
}

func newVMMemoryMetadataForBackend(initialMB, minMB, maxMB int, autoBalloon bool, pending bool, backend string) (*vmMemoryMetadata, error) {
	backend = normalizeMemoryBackend(backend)
	if initialMB <= 0 {
		return nil, fmt.Errorf("启动内存必须大于 0")
	}
	if minMB <= 0 {
		minMB = 1024
	}
	if maxMB <= 0 {
		maxMB = initialMB
	}
	if minMB > initialMB {
		return nil, fmt.Errorf("最小内存不能大于启动内存")
	}
	if initialMB > maxMB {
		return nil, fmt.Errorf("启动内存不能大于最大内存")
	}
	if backend == memoryBackendVirtioMem {
		if initialMB == maxMB {
			return nil, fmt.Errorf("Windows 弹性内存最大内存必须大于基础内存")
		}
		minMB = initialMB
		autoBalloon = false
		pending = false
	}
	now := time.Now()
	observationHours := 24
	if config.GlobalConfig != nil && config.GlobalConfig.DynamicMemoryObservationHours > 0 {
		observationHours = config.GlobalConfig.DynamicMemoryObservationHours
	}
	return &vmMemoryMetadata{
		Version:          1,
		DynamicEnabled:   true,
		MemoryBackend:    backend,
		MemoryInitialMB:  initialMB,
		MemoryMinMB:      minMB,
		MemoryMaxMB:      maxMB,
		AutoBalloon:      autoBalloon,
		PendingApply:     pending,
		ObservationUntil: now.Add(time.Duration(observationHours) * time.Hour).Unix(),
		UpdatedAt:        now.Unix(),
	}, nil
}

// GetVMMemoryDynamicInfo 返回 VM 的动态内存配置；旧 VM 会被推断为静态兼容。
func GetVMMemoryDynamicInfo(name, xmlStr, state string) *VMMemoryDynamicInfo {
	values := parseDomainMemoryXML(xmlStr)
	if values.MemoryMB <= 0 {
		values.MemoryMB = 1024
	}
	if values.CurrentMemoryMB <= 0 {
		values.CurrentMemoryMB = values.MemoryMB
	}

	balloonSupported := hasUsableMemballoon(xmlStr)
	info := &VMMemoryDynamicInfo{
		DynamicEnabled:   false,
		MemoryBackend:    memoryBackendBalloon,
		MemoryInitial:    values.CurrentMemoryMB,
		MemoryMin:        defaultMinMemoryMB(values.CurrentMemoryMB),
		MemoryMax:        values.MemoryMB,
		VirtioMemCurrent: parseVirtioMemCurrentMB(xmlStr),
		AutoBalloon:      false,
		PendingApply:     false,
		CompatMode:       memoryCompatLegacyStatic,
		BalloonSupported: balloonSupported,
		BalloonStatus:    resolveBalloonStatus(name, state, balloonSupported, false),
	}

	meta, err := readVMMemoryMetadata(name)
	if err != nil || meta == nil {
		return info
	}
	backend := normalizeMemoryBackend(meta.MemoryBackend)
	info.DynamicEnabled = meta.DynamicEnabled
	info.MemoryBackend = backend
	info.MemoryInitial = fallbackPositive(meta.MemoryInitialMB, values.CurrentMemoryMB)
	info.MemoryMin = fallbackPositive(meta.MemoryMinMB, defaultMinMemoryMB(info.MemoryInitial))
	info.MemoryMax = fallbackPositive(meta.MemoryMaxMB, values.MemoryMB)
	info.AutoBalloon = meta.AutoBalloon
	info.PendingApply = meta.PendingApply
	info.ObservationUntil = meta.ObservationUntil
	info.ManualPauseUntil = meta.ManualPauseUntil
	if meta.DynamicEnabled {
		info.CompatMode = memoryCompatDynamic
	}
	if meta.PendingApply {
		info.CompatMode = memoryCompatPending
	}
	info.BalloonStatus = resolveBalloonStatus(name, state, balloonSupported, meta.PendingApply)
	return info
}

func fallbackPositive(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func defaultMinMemoryMB(initialMB int) int {
	minMB := initialMB / 2
	if minMB < 1024 {
		minMB = 1024
	}
	return minMB
}

func resolveBalloonStatus(name, state string, supported, pending bool) string {
	if pending {
		return "pending_apply"
	}
	if !supported {
		return "missing_balloon"
	}
	if state != "running" {
		return "not_running"
	}
	if cached := GetCachedStats(name); cached != nil {
		return "ok"
	}
	if name == "" {
		return "no_stats"
	}
	return "no_stats"
}

func readVMMemoryMetadata(name string) (*vmMemoryMetadata, error) {
	result := utils.ExecCommand("virsh", "metadata", name, vmMemoryMetadataURI, "--config")
	if result.Error != nil {
		text := strings.ToLower(strings.TrimSpace(result.Stderr + "\n" + result.Stdout))
		if strings.Contains(text, "metadata not found") || strings.Contains(text, "no metadata") {
			return nil, nil
		}
		return nil, fmt.Errorf("读取动态内存配置失败: %s", result.Stderr)
	}
	return parseVMMemoryMetadataOutput(result.Stdout)
}

func parseVMMemoryMetadataOutput(output string) (*vmMemoryMetadata, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		// 部分 libvirt 版本在 metadata 不存在时返回成功但 stdout 为空。
		return nil, nil
	}
	var wrapper vmMemoryMetadataXML
	if err := xml.Unmarshal([]byte(output), &wrapper); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(wrapper.Data))
	if err != nil {
		return nil, err
	}
	var meta vmMemoryMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeVMMemoryMetadata(name string, meta *vmMemoryMetadata) error {
	meta.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	wrapper := vmMemoryMetadataXML{
		XMLNS: vmMemoryMetadataURI,
		Data:  base64.StdEncoding.EncodeToString(raw),
	}
	xmlBytes, err := xml.Marshal(wrapper)
	if err != nil {
		return err
	}
	result := utils.ExecCommand(
		"virsh", "metadata", name, vmMemoryMetadataURI,
		"--config", "--key", vmMemoryMetadataKey, "--set", string(xmlBytes),
	)
	if result.Error != nil {
		return fmt.Errorf("写入动态内存配置失败: %s", result.Stderr)
	}
	return nil
}

// SetVMMemoryDynamicConfig 由管理员启用/修改动态内存配置，运行中 VM 只写待应用 metadata。
func SetVMMemoryDynamicConfig(name string, req *VMMemoryDynamicRequest) (string, error) {
	if req == nil {
		return "", nil
	}

	xmlResult := utils.ExecCommand("virsh", "dumpxml", name, "--inactive")
	if xmlResult.Error != nil {
		return "", fmt.Errorf("获取虚拟机 XML 失败: %s", xmlResult.Stderr)
	}
	state := strings.TrimSpace(utils.ExecCommand("virsh", "domstate", name).Stdout)
	values := parseDomainMemoryXML(xmlResult.Stdout)
	if values.CurrentMemoryMB <= 0 {
		values.CurrentMemoryMB = values.MemoryMB
	}

	if req.DynamicEnabled != nil && !*req.DynamicEnabled {
		targetMB := values.CurrentMemoryMB
		if req.MemoryInitial > 0 {
			targetMB = req.MemoryInitial * 1024
		}
		if targetMB <= 0 {
			targetMB = values.MemoryMB
		}
		if targetMB <= 0 {
			return "", fmt.Errorf("关闭动态内存失败: 未能确定静态内存大小")
		}
		if err := applyStaticVMMemoryToInactiveXML(name, targetMB); err != nil {
			return "", err
		}
		meta := &vmMemoryMetadata{
			Version:         1,
			DynamicEnabled:  false,
			MemoryBackend:   memoryBackendBalloon,
			MemoryInitialMB: targetMB,
			MemoryMinMB:     defaultMinMemoryMB(targetMB),
			MemoryMaxMB:     targetMB,
			AutoBalloon:     false,
			PendingApply:    false,
			UpdatedAt:       time.Now().Unix(),
		}
		if err := writeVMMemoryMetadata(name, meta); err != nil {
			return "", err
		}
		return "动态内存已关闭", nil
	}

	initialGB := mbToRoundedGB(values.CurrentMemoryMB)
	if req.MemoryInitial > 0 {
		initialGB = req.MemoryInitial
	}
	maxGB := defaultDynamicMemoryMaxGB(initialGB)
	if req.MemoryMax > 0 {
		maxGB = req.MemoryMax
	}
	minGB := defaultDynamicMemoryMinGB(initialGB)
	if req.MemoryMin > 0 {
		minGB = req.MemoryMin
	}
	autoBalloon := true
	if req.AutoBalloon != nil {
		autoBalloon = *req.AutoBalloon
	}
	backend := normalizeMemoryBackend(req.MemoryBackend)
	if backend == memoryBackendVirtioMem {
		minGB = initialGB
		autoBalloon = false
	}

	meta, err := newVMMemoryMetadataForBackend(initialGB*1024, minGB*1024, maxGB*1024, autoBalloon, state == "running", backend)
	if err != nil {
		return "", err
	}

	if backend == memoryBackendVirtioMem {
		if state == "running" {
			oldMeta, _ := readVMMemoryMetadata(name)
			if oldMeta != nil &&
				oldMeta.DynamicEnabled &&
				normalizeMemoryBackend(oldMeta.MemoryBackend) == memoryBackendVirtioMem &&
				oldMeta.MemoryInitialMB == meta.MemoryInitialMB &&
				oldMeta.MemoryMaxMB == meta.MemoryMaxMB {
				return "Windows 弹性内存（实验）基础配置未变更", nil
			}
			return "", fmt.Errorf("Windows 弹性内存（实验）需要先关机后启用或修改基础配置")
		}
		if err := applyVMMemoryMetadataToInactiveXML(name, meta); err != nil {
			return "", err
		}
		if err := writeVMMemoryMetadata(name, meta); err != nil {
			return "", err
		}
		return "Windows 弹性内存（实验）配置已应用", nil
	}

	if state == "running" {
		if !hasUsableMemballoon(xmlResult.Stdout) {
			return "", fmt.Errorf("运行中的虚拟机未配置 virtio-balloon，请先关机后再启用动态内存")
		}
		if err := writeVMMemoryMetadata(name, meta); err != nil {
			return "", err
		}
		return "动态内存配置已保存，将在下次关机后启动时应用", nil
	}

	if err := applyVMMemoryMetadataToInactiveXML(name, meta); err != nil {
		return "", err
	}
	meta.PendingApply = false
	if err := writeVMMemoryMetadata(name, meta); err != nil {
		return "", err
	}
	return "动态内存配置已应用", nil
}

func applyStaticVMMemoryToInactiveXML(name string, memoryMB int) error {
	xmlResult := utils.ExecCommand("virsh", "dumpxml", name, "--inactive")
	if xmlResult.Error != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %s", xmlResult.Stderr)
	}
	xmlStr, err := ApplyStaticMemoryConfigToDomainXML(xmlResult.Stdout, memoryMB)
	if err != nil {
		return err
	}
	xmlPath := fmt.Sprintf("/tmp/_memory-static-%s.xml", name)
	if err := os.WriteFile(xmlPath, []byte(xmlStr), 0644); err != nil {
		return fmt.Errorf("写入静态内存 XML 失败: %w", err)
	}
	defineResult := utils.ExecCommand("virsh", "define", xmlPath)
	_ = os.Remove(xmlPath)
	if defineResult.Error != nil {
		return fmt.Errorf("恢复静态内存配置失败: %s", defineResult.Stderr)
	}
	return nil
}

func mbToRoundedGB(mb int) int {
	if mb <= 0 {
		return 0
	}
	return (mb + 1023) / 1024
}

// SetVMMemoryCurrent 手动调整运行中 VM 当前内存，单位 MB。
func SetVMMemoryCurrent(name string, targetMB int, pauseAuto bool) error {
	if targetMB <= 0 {
		return fmt.Errorf("当前内存必须大于 0")
	}
	meta, _ := readVMMemoryMetadata(name)
	if meta != nil && meta.DynamicEnabled {
		if normalizeMemoryBackend(meta.MemoryBackend) == memoryBackendVirtioMem {
			if targetMB < meta.MemoryInitialMB || targetMB > meta.MemoryMaxMB {
				return fmt.Errorf("当前内存必须在 %dMB 到 %dMB 之间", meta.MemoryInitialMB, meta.MemoryMaxMB)
			}
			if pauseAuto {
				meta.ManualPauseUntil = time.Now().Add(10 * time.Minute).Unix()
				_ = writeVMMemoryMetadata(name, meta)
			}
			return setVirtioMemRequestedLive(name, targetMB-meta.MemoryInitialMB)
		}
		if targetMB < meta.MemoryMinMB || targetMB > meta.MemoryMaxMB {
			return fmt.Errorf("当前内存必须在 %dMB 到 %dMB 之间", meta.MemoryMinMB, meta.MemoryMaxMB)
		}
		if pauseAuto {
			meta.ManualPauseUntil = time.Now().Add(10 * time.Minute).Unix()
			_ = writeVMMemoryMetadata(name, meta)
		}
	}
	result := utils.ExecCommand("virsh", "setmem", name, strconv.Itoa(targetMB*1024), "--live")
	if result.Error != nil {
		return fmt.Errorf("调整当前内存失败: %s", result.Stderr)
	}
	return nil
}

// ApplyPendingVMMemoryConfig 在 VM 开机前应用待迁移配置。
func ApplyPendingVMMemoryConfig(name string) error {
	meta, err := readVMMemoryMetadata(name)
	if err != nil || meta == nil || !meta.DynamicEnabled || !meta.PendingApply {
		return err
	}
	if err := applyVMMemoryMetadataToInactiveXML(name, meta); err != nil {
		return err
	}
	meta.PendingApply = false
	return writeVMMemoryMetadata(name, meta)
}

func applyVMMemoryMetadataToInactiveXML(name string, meta *vmMemoryMetadata) error {
	xmlResult := utils.ExecCommand("virsh", "dumpxml", name, "--inactive")
	if xmlResult.Error != nil {
		return fmt.Errorf("获取虚拟机 XML 失败: %s", xmlResult.Stderr)
	}
	var xmlStr string
	var err error
	if normalizeMemoryBackend(meta.MemoryBackend) == memoryBackendVirtioMem {
		xmlStr, err = ApplyVirtioMemConfigToDomainXML(xmlResult.Stdout, meta.MemoryInitialMB, meta.MemoryMaxMB)
	} else {
		xmlStr, err = ApplyDynamicMemoryConfigToDomainXML(xmlResult.Stdout, meta.MemoryInitialMB, meta.MemoryMaxMB, true)
	}
	if err != nil {
		return err
	}
	xmlPath := fmt.Sprintf("/tmp/_memory-%s.xml", name)
	if err := os.WriteFile(xmlPath, []byte(xmlStr), 0644); err != nil {
		return fmt.Errorf("写入动态内存 XML 失败: %w", err)
	}
	defineResult := utils.ExecCommand("virsh", "define", xmlPath)
	_ = os.Remove(xmlPath)
	if defineResult.Error != nil {
		return fmt.Errorf("应用动态内存配置失败: %s", defineResult.Stderr)
	}
	return nil
}

// ApplyDynamicMemoryConfigToDomainXML 将 XML 调整为最大内存 + 启动内存，并补齐 memballoon。
func ApplyDynamicMemoryConfigToDomainXML(xmlStr string, initialMB, maxMB int, enableFPR bool) (string, error) {
	if initialMB <= 0 || maxMB <= 0 {
		return "", fmt.Errorf("内存配置必须大于 0")
	}
	if initialMB > maxMB {
		return "", fmt.Errorf("启动内存不能大于最大内存")
	}
	maxKiB := maxMB * 1024
	initialKiB := initialMB * 1024
	memRe := regexp.MustCompile(`(?s)<memory\b[^>]*>.*?</memory>`)
	currentRe := regexp.MustCompile(`(?s)<currentMemory\b[^>]*>.*?</currentMemory>`)
	memoryTag := fmt.Sprintf("<memory unit='KiB'>%d</memory>", maxKiB)
	currentTag := fmt.Sprintf("<currentMemory unit='KiB'>%d</currentMemory>", initialKiB)

	if memRe.MatchString(xmlStr) {
		xmlStr = memRe.ReplaceAllString(xmlStr, memoryTag)
	} else {
		xmlStr = strings.Replace(xmlStr, "<vcpu", memoryTag+"\n  <vcpu", 1)
	}
	if currentRe.MatchString(xmlStr) {
		xmlStr = currentRe.ReplaceAllString(xmlStr, currentTag)
	} else {
		xmlStr = strings.Replace(xmlStr, memoryTag, memoryTag+"\n  "+currentTag, 1)
	}
	xmlStr = injectMemballoonConfig(xmlStr, enableFPR)
	return xmlStr, nil
}

func ApplyMemoryMetadataToDomainXML(xmlStr string, meta *vmMemoryMetadata, enableFPR bool) (string, error) {
	if meta == nil {
		return xmlStr, nil
	}
	if normalizeMemoryBackend(meta.MemoryBackend) == memoryBackendVirtioMem {
		return ApplyVirtioMemConfigToDomainXML(xmlStr, meta.MemoryInitialMB, meta.MemoryMaxMB)
	}
	return ApplyDynamicMemoryConfigToDomainXML(xmlStr, meta.MemoryInitialMB, meta.MemoryMaxMB, enableFPR)
}

func ApplyVirtioMemConfigToDomainXML(xmlStr string, initialMB, maxMB int) (string, error) {
	if initialMB <= 0 || maxMB <= 0 {
		return "", fmt.Errorf("内存配置必须大于 0")
	}
	if initialMB >= maxMB {
		return "", fmt.Errorf("Windows 弹性内存最大内存必须大于基础内存")
	}
	initialKiB := initialMB * 1024
	maxKiB := maxMB * 1024
	expandKiB := maxKiB - initialKiB

	memRe := regexp.MustCompile(`(?s)<memory\b[^>]*>.*?</memory>`)
	currentRe := regexp.MustCompile(`(?s)<currentMemory\b[^>]*>.*?</currentMemory>`)
	maxMemoryRe := regexp.MustCompile(`(?s)\s*<maxMemory\b[^>]*>.*?</maxMemory>\n?`)
	virtioMemRe := regexp.MustCompile(`(?s)\s*<memory\s+model=['"]virtio-mem['"][^>]*>.*?</memory>\n?`)

	xmlStr = virtioMemRe.ReplaceAllString(xmlStr, "")
	xmlStr = maxMemoryRe.ReplaceAllString(xmlStr, "\n")
	if memRe.MatchString(xmlStr) {
		xmlStr = memRe.ReplaceAllString(xmlStr, fmt.Sprintf("<memory unit='KiB'>%d</memory>", initialKiB))
	} else {
		xmlStr = strings.Replace(xmlStr, "<vcpu", fmt.Sprintf("<memory unit='KiB'>%d</memory>\n  <vcpu", initialKiB), 1)
	}
	if currentRe.MatchString(xmlStr) {
		xmlStr = currentRe.ReplaceAllString(xmlStr, fmt.Sprintf("<currentMemory unit='KiB'>%d</currentMemory>", initialKiB))
	} else {
		xmlStr = strings.Replace(xmlStr, fmt.Sprintf("<memory unit='KiB'>%d</memory>", initialKiB), fmt.Sprintf("<memory unit='KiB'>%d</memory>\n  <currentMemory unit='KiB'>%d</currentMemory>", initialKiB, initialKiB), 1)
	}
	xmlStr = strings.Replace(xmlStr, fmt.Sprintf("<memory unit='KiB'>%d</memory>", initialKiB), fmt.Sprintf("<maxMemory slots='16' unit='KiB'>%d</maxMemory>\n  <memory unit='KiB'>%d</memory>", maxKiB, initialKiB), 1)
	xmlStr = ensureVirtioMemNumaCell(xmlStr, initialKiB)
	xmlStr = injectMemballoonConfig(xmlStr, false)

	memoryDevice := fmt.Sprintf(`    <memory model='virtio-mem'>
      <target>
        <size unit='KiB'>%d</size>
        <node>0</node>
        <block unit='KiB'>2048</block>
        <requested unit='KiB'>0</requested>
      </target>
    </memory>`, expandKiB)
	if strings.Contains(xmlStr, "</devices>") {
		xmlStr = strings.Replace(xmlStr, "</devices>", memoryDevice+"\n  </devices>", 1)
	} else {
		return "", fmt.Errorf("虚拟机 XML 缺少 devices 节点")
	}
	return xmlStr, nil
}

func ensureVirtioMemNumaCell(xmlStr string, memoryKiB int) string {
	vcpus := parseVCPUCount(xmlStr)
	cpus := "0"
	if vcpus > 1 {
		cpus = fmt.Sprintf("0-%d", vcpus-1)
	}
	numaXML := fmt.Sprintf("    <numa>\n      <cell id='0' cpus='%s' memory='%d' unit='KiB'/>\n    </numa>", cpus, memoryKiB)
	cpuSelfRe := regexp.MustCompile(`(?s)<cpu\b([^>]*)/>`)
	if cpuSelfRe.MatchString(xmlStr) {
		return cpuSelfRe.ReplaceAllString(xmlStr, "<cpu$1>\n"+numaXML+"\n  </cpu>")
	}
	cpuBlockRe := regexp.MustCompile(`(?s)<cpu\b[^>]*>.*?</cpu>`)
	return cpuBlockRe.ReplaceAllStringFunc(xmlStr, func(cpuBlock string) string {
		numaRe := regexp.MustCompile(`(?s)\s*<numa>.*?</numa>\n?`)
		cpuBlock = numaRe.ReplaceAllString(cpuBlock, "")
		return strings.Replace(cpuBlock, "</cpu>", numaXML+"\n  </cpu>", 1)
	})
}

func parseVCPUCount(xmlStr string) int {
	re := regexp.MustCompile(`(?s)<vcpu\b[^>]*>(.*?)</vcpu>`)
	m := re.FindStringSubmatch(xmlStr)
	if len(m) < 2 {
		return 1
	}
	value, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil || value <= 0 {
		return 1
	}
	return value
}

// ApplyStaticMemoryConfigToDomainXML 将最大内存和启动内存恢复为同一个静态值。
func ApplyStaticMemoryConfigToDomainXML(xmlStr string, memoryMB int) (string, error) {
	got, err := ApplyDynamicMemoryConfigToDomainXML(xmlStr, memoryMB, memoryMB, false)
	if err != nil {
		return "", err
	}
	maxMemoryRe := regexp.MustCompile(`(?s)\s*<maxMemory\b[^>]*>.*?</maxMemory>\n?`)
	virtioMemRe := regexp.MustCompile(`(?s)\s*<memory\s+model=['"]virtio-mem['"][^>]*>.*?</memory>\n?`)
	got = maxMemoryRe.ReplaceAllString(got, "\n")
	got = virtioMemRe.ReplaceAllString(got, "")
	return got, nil
}

func parseDomainMemoryXML(xmlStr string) vmMemoryXMLValues {
	return vmMemoryXMLValues{
		MemoryMB:        parseMemoryTagMB(xmlStr, "memory"),
		CurrentMemoryMB: parseMemoryTagMB(xmlStr, "currentMemory"),
	}
}

func parseMemoryTagMB(xmlStr, tag string) int {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<%s\b([^>]*)>(.*?)</%s>`, tag, tag))
	m := re.FindStringSubmatch(xmlStr)
	if len(m) < 3 {
		return 0
	}
	unit := "KiB"
	unitRe := regexp.MustCompile(`unit=['"]([^'"]+)['"]`)
	if um := unitRe.FindStringSubmatch(m[1]); len(um) > 1 {
		unit = um[1]
	}
	value, _ := strconv.ParseFloat(strings.TrimSpace(m[2]), 64)
	switch strings.ToLower(unit) {
	case "b", "bytes":
		return int(value / 1024 / 1024)
	case "kb", "k", "kib":
		return int(value / 1024)
	case "mb", "m", "mib":
		return int(value)
	case "gb", "g", "gib":
		return int(value * 1024)
	default:
		return int(value / 1024)
	}
}

func parseVirtioMemCurrentMB(xmlStr string) int {
	deviceRe := regexp.MustCompile(`(?s)<memory\s+model=['"]virtio-mem['"][^>]*>(.*?)</memory>`)
	m := deviceRe.FindStringSubmatch(xmlStr)
	if len(m) < 2 {
		return 0
	}
	currentMB := parseMemoryTagMB(m[1], "current")
	if currentMB > 0 {
		return currentMB
	}
	return parseMemoryTagMB(m[1], "requested")
}

func parseVirtioMemRequestedMB(xmlStr string) int {
	deviceRe := regexp.MustCompile(`(?s)<memory\s+model=['"]virtio-mem['"][^>]*>(.*?)</memory>`)
	m := deviceRe.FindStringSubmatch(xmlStr)
	if len(m) < 2 {
		return 0
	}
	return parseMemoryTagMB(m[1], "requested")
}

func findVirtioMemAlias(xmlStr string) string {
	deviceRe := regexp.MustCompile(`(?s)<memory\s+model=['"]virtio-mem['"][^>]*>.*?</memory>`)
	device := deviceRe.FindString(xmlStr)
	if device == "" {
		return ""
	}
	aliasRe := regexp.MustCompile(`<alias\s+name=['"]([^'"]+)['"]\s*/>`)
	if m := aliasRe.FindStringSubmatch(device); len(m) > 1 {
		return m[1]
	}
	return ""
}

func hasUsableMemballoon(xmlStr string) bool {
	if !strings.Contains(xmlStr, "<memballoon") {
		return false
	}
	return !strings.Contains(xmlStr, "model='none'") && !strings.Contains(xmlStr, `model="none"`)
}

func getVMMemoryStats(name string) (*vmMemoryStatsValues, error) {
	result := utils.ExecCommand("virsh", "dommemstat", name)
	if result.Error != nil {
		return nil, result.Error
	}
	stats := &vmMemoryStatsValues{
		ActualKB:    parseMemStat(result.Stdout, "actual"),
		UnusedKB:    parseMemStat(result.Stdout, "unused"),
		UsableKB:    parseMemStat(result.Stdout, "usable"),
		AvailableKB: parseMemStat(result.Stdout, "available"),
		RSSKB:       parseMemStat(result.Stdout, "rss"),
	}
	return stats, nil
}

func registerDynamicMemorySchedulers() {
	dynamicMemorySchedulerRegisterOnce.Do(func() {
		RegisterScheduler(SchedulerDefinition{
			Key:         schedulerKeyDynamicMemoryBalloon,
			Name:        schedulerNameDynamicMemoryBalloon,
			Group:       schedulerGroupDynamicMemory,
			Description: "基于 virtio-balloon 的动态内存自动伸缩调度。",
			Enabled: func() bool {
				return config.GlobalConfig == nil || config.GlobalConfig.DynamicMemorySchedulerEnabled
			},
		})
		RegisterScheduler(SchedulerDefinition{
			Key:         schedulerKeyDynamicMemoryVirtioMem,
			Name:        schedulerNameDynamicMemoryVirtioMem,
			Group:       schedulerGroupDynamicMemory,
			Description: "基于 Windows virtio-mem 的弹性内存自动伸缩调度。",
			Enabled: func() bool {
				return config.GlobalConfig == nil || config.GlobalConfig.DynamicMemorySchedulerEnabled
			},
		})
	})
}

// StartMemoryBalloonScheduler 启动动态内存调度器。
func StartMemoryBalloonScheduler() {
	registerDynamicMemorySchedulers()
	go func() {
		logger.App.Info("动态内存调度器已启动")
		for {
			interval := 30
			if config.GlobalConfig != nil && config.GlobalConfig.DynamicMemoryIntervalSeconds > 0 {
				interval = config.GlobalConfig.DynamicMemoryIntervalSeconds
			}
			time.Sleep(time.Duration(interval) * time.Second)
			if IsMaintenanceModeEnabled() {
				continue
			}
			if config.GlobalConfig != nil && !config.GlobalConfig.DynamicMemorySchedulerEnabled {
				continue
			}
			runMemoryBalloonScheduleOnce()
		}
	}()
}

func runMemoryBalloonScheduleOnce() {
	result := utils.ExecShell("virsh list --name --state-running 2>/dev/null | grep -v '^$'")
	if result.Error != nil || strings.TrimSpace(result.Stdout) == "" {
		return
	}
	host := getHostMemoryPressure()
	for _, name := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		scheduleVMMemory(name, host)
	}
}

func startMemorySchedulerEvent(schedulerKey, schedulerName, vmName, vmBackend, reason string) *model.SchedulerEvent {
	event, err := StartSchedulerEvent(SchedulerEventStartInput{
		SchedulerKey:   schedulerKey,
		SchedulerName:  schedulerName,
		SchedulerGroup: schedulerGroupDynamicMemory,
		VMName:         vmName,
		VMBackend:      vmBackend,
		TriggerReason:  reason,
	})
	if err != nil {
		logger.App.Warn("动态内存记录调度事件失败", "error", err)
		return nil
	}
	return event
}

func finishMemorySchedulerEventSuccess(event *model.SchedulerEvent, message string) {
	if err := FinishSchedulerEventSuccess(event, message); err != nil {
		logger.App.Warn("动态内存更新调度事件成功状态失败", "error", err)
	}
}

func finishMemorySchedulerEventFailed(event *model.SchedulerEvent, message string) {
	if err := FinishSchedulerEventFailed(event, message); err != nil {
		logger.App.Warn("动态内存更新调度事件失败状态失败", "error", err)
	}
}

type hostMemoryPressure struct {
	TotalKB     int64
	AvailableKB int64
	ReserveKB   int64
	Pressure    bool
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value*100)
}

func buildBalloonExpandReason(usableRatio, threshold float64) string {
	return fmt.Sprintf("可用内存比例 %s 低于增长阈值 %s，触发扩容", formatPercent(usableRatio), formatPercent(threshold))
}

func buildBalloonReclaimReason(unusedRatio, threshold float64) string {
	return fmt.Sprintf("空闲内存比例 %s 高于回收阈值 %s，触发回收", formatPercent(unusedRatio), formatPercent(threshold))
}

func buildVirtioMemExpandReason(usageRatio float64) string {
	return fmt.Sprintf("来宾内存使用率 %s 超过 70.0%%，触发扩容", formatPercent(usageRatio))
}

func buildVirtioMemReclaimReason(usageRatio float64) string {
	return fmt.Sprintf("来宾内存使用率 %s 低于 50.0%%，触发缩容", formatPercent(usageRatio))
}

func buildVirtioMemResultMessage(actualMB, targetMB int, shrink bool) string {
	if shrink {
		return fmt.Sprintf("已请求将弹性内存目标从 %dMB 调整到 %dMB；若当前仍高于目标，表示来宾系统尚未完全释放内存", actualMB, targetMB)
	}
	return fmt.Sprintf("已请求将弹性内存目标从 %dMB 调整到 %dMB", actualMB, targetMB)
}

func getHostMemoryPressure() hostMemoryPressure {
	stats, _ := GetHostStats()
	if stats == nil {
		return hostMemoryPressure{}
	}
	availableKB := stats.MemFree
	meminfo := utils.ExecShell(`awk '/MemAvailable:/ {print $2}' /proc/meminfo`)
	if meminfo.Error == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(meminfo.Stdout), 10, 64); err == nil && v > 0 {
			availableKB = v
		}
	}
	reserveMB := 2048
	reservePercent := 20
	if config.GlobalConfig != nil {
		if config.GlobalConfig.DynamicMemoryHostReserveMB > 0 {
			reserveMB = config.GlobalConfig.DynamicMemoryHostReserveMB
		}
		if config.GlobalConfig.DynamicMemoryHostReservePercent > 0 {
			reservePercent = config.GlobalConfig.DynamicMemoryHostReservePercent
		}
	}
	reserveByPercent := stats.MemTotal * int64(reservePercent) / 100
	reserveByMB := int64(reserveMB) * 1024
	reserveKB := reserveByPercent
	if reserveByMB > reserveKB {
		reserveKB = reserveByMB
	}
	return hostMemoryPressure{
		TotalKB:     stats.MemTotal,
		AvailableKB: availableKB,
		ReserveKB:   reserveKB,
		Pressure:    availableKB < reserveKB,
	}
}

func scheduleVMMemory(name string, host hostMemoryPressure) {
	meta, err := readVMMemoryMetadata(name)
	if err != nil || meta == nil || !meta.DynamicEnabled || meta.PendingApply {
		return
	}
	backend := normalizeMemoryBackend(meta.MemoryBackend)
	if backend == memoryBackendVirtioMem {
		scheduleVMVirtioMem(name, meta, host)
		return
	}
	if backend != memoryBackendBalloon || !meta.AutoBalloon {
		return
	}

	now := time.Now()
	if meta.ManualPauseUntil > now.Unix() {
		return
	}

	memorySchedulerState.Lock()
	lastAdjust := memorySchedulerState.lastAdjust[name]
	memorySchedulerState.Unlock()
	cooldownSeconds := 120
	if config.GlobalConfig != nil && config.GlobalConfig.DynamicMemoryCooldownSeconds > 0 {
		cooldownSeconds = config.GlobalConfig.DynamicMemoryCooldownSeconds
	}
	if !lastAdjust.IsZero() && now.Sub(lastAdjust) < time.Duration(cooldownSeconds)*time.Second {
		return
	}

	stats, err := getVMMemoryStats(name)
	if err != nil || stats.ActualKB <= 0 || stats.AvailableKB <= 0 {
		return
	}
	actualMB := int(stats.ActualKB / 1024)
	usedKB := stats.AvailableKB - stats.UnusedKB
	if usedKB < 0 {
		usedKB = 0
	}
	usedMB := int(usedKB / 1024)
	usableRatio := float64(stats.UsableKB) / float64(stats.ActualKB)
	unusedRatio := float64(stats.UnusedKB) / float64(stats.ActualKB)

	increaseThresholdValue := 15
	reclaimThresholdValue := 35
	if config.GlobalConfig != nil {
		increaseThresholdValue = config.GlobalConfig.DynamicMemoryIncreaseThresholdPercent
		reclaimThresholdValue = config.GlobalConfig.DynamicMemoryReclaimThresholdPercent
	}
	increaseThreshold := percentConfig(increaseThresholdValue, 15)
	reclaimThreshold := percentConfig(reclaimThresholdValue, 35)

	memorySchedulerState.Lock()
	if usableRatio < increaseThreshold {
		memorySchedulerState.lowUsable[name]++
	} else {
		memorySchedulerState.lowUsable[name] = 0
	}
	if unusedRatio > reclaimThreshold {
		memorySchedulerState.highUnused[name]++
	} else {
		memorySchedulerState.highUnused[name] = 0
	}
	lowCount := memorySchedulerState.lowUsable[name]
	highCount := memorySchedulerState.highUnused[name]
	memorySchedulerState.Unlock()

	if lowCount >= 2 && actualMB < meta.MemoryMaxMB {
		targetMB := maxInt(int(float64(usedMB)*1.35), int(float64(actualMB)*1.25))
		targetMB = minInt(maxInt(targetMB, actualMB+256), meta.MemoryMaxMB)
		reason := buildBalloonExpandReason(usableRatio, increaseThreshold)
		event := startMemorySchedulerEvent(schedulerKeyDynamicMemoryBalloon, schedulerNameDynamicMemoryBalloon, name, memoryBackendBalloon, reason)
		needExtraKB := int64(targetMB-actualMB) * 1024
		if host.AvailableKB-needExtraKB < host.ReserveKB {
			message := fmt.Sprintf("宿主机可用内存不足，目标内存 %dMB，当前可用 %dMB，保留阈值 %dMB", targetMB, host.AvailableKB/1024, host.ReserveKB/1024)
			logger.App.Warn("动态内存跳过增长", "message", message, "vm", name, "targetMB", targetMB)
			finishMemorySchedulerEventFailed(event, message)
			return
		}
		if err := setVMMemoryLive(name, targetMB); err == nil {
			finishMemorySchedulerEventSuccess(event, fmt.Sprintf("已将当前内存从 %dMB 调整到 %dMB", actualMB, targetMB))
			markMemoryAdjusted(name)
		} else {
			finishMemorySchedulerEventFailed(event, err.Error())
		}
		return
	}

	reclaimFloorMB := meta.MemoryInitialMB
	if host.Pressure {
		reclaimFloorMB = meta.MemoryMinMB
	}
	if meta.ObservationUntil > now.Unix() {
		reclaimFloorMB = meta.MemoryInitialMB
	}
	if highCount >= 5 && actualMB > reclaimFloorMB {
		targetMB := maxInt(int(float64(usedMB)*1.25), reclaimFloorMB)
		targetMB = minInt(targetMB, actualMB-256)
		if targetMB < actualMB {
			reason := buildBalloonReclaimReason(unusedRatio, reclaimThreshold)
			event := startMemorySchedulerEvent(schedulerKeyDynamicMemoryBalloon, schedulerNameDynamicMemoryBalloon, name, memoryBackendBalloon, reason)
			if err := setVMMemoryLive(name, targetMB); err == nil {
				finishMemorySchedulerEventSuccess(event, fmt.Sprintf("已将当前内存从 %dMB 调整到 %dMB", actualMB, targetMB))
				markMemoryAdjusted(name)
			} else {
				finishMemorySchedulerEventFailed(event, err.Error())
			}
		}
	}
}

func scheduleVMVirtioMem(name string, meta *vmMemoryMetadata, host hostMemoryPressure) {
	now := time.Now()
	if meta.ManualPauseUntil > now.Unix() {
		return
	}

	memorySchedulerState.Lock()
	lastAdjust := memorySchedulerState.lastAdjust[name]
	memorySchedulerState.Unlock()
	cooldownSeconds := 120
	if config.GlobalConfig != nil && config.GlobalConfig.DynamicMemoryCooldownSeconds > 0 {
		cooldownSeconds = config.GlobalConfig.DynamicMemoryCooldownSeconds
	}
	if !lastAdjust.IsZero() && now.Sub(lastAdjust) < time.Duration(cooldownSeconds)*time.Second {
		return
	}

	stats, err := getVMMemoryStats(name)
	if err != nil || stats.ActualKB <= 0 {
		return
	}
	actualMB := int(stats.ActualKB / 1024)
	usedMB := calculateGuestUsedMemoryMB(stats)
	if usedMB <= 0 {
		return
	}

	requestedMB := maxInt(actualMB-meta.MemoryInitialMB, 0)
	if xmlResult := utils.ExecCommand("virsh", "dumpxml", name); xmlResult.Error == nil {
		currentVirtioMemMB := parseVirtioMemCurrentMB(xmlResult.Stdout)
		requestedMB = parseVirtioMemRequestedMB(xmlResult.Stdout)
		actualMB = maxInt(actualMB, meta.MemoryInitialMB+currentVirtioMemMB)
	}
	actualMB = maxInt(actualMB, meta.MemoryInitialMB)
	actualMB = minInt(actualMB, meta.MemoryMaxMB)
	targetMB := calculateVirtioMemScheduleTarget(actualMB, usedMB, meta.MemoryInitialMB, meta.MemoryMaxMB)
	if targetMB == actualMB {
		return
	}

	usageRatio := float64(usedMB) / float64(actualMB)
	reason := buildVirtioMemExpandReason(usageRatio)
	if targetMB < actualMB {
		reason = buildVirtioMemReclaimReason(usageRatio)
	}

	targetRequestedMB := maxInt(targetMB-meta.MemoryInitialMB, 0)
	if targetRequestedMB == requestedMB {
		return
	}

	event := startMemorySchedulerEvent(schedulerKeyDynamicMemoryVirtioMem, schedulerNameDynamicMemoryVirtioMem, name, memoryBackendVirtioMem, reason)

	if targetMB > actualMB {
		needExtraKB := int64(targetMB-actualMB) * 1024
		if host.AvailableKB-needExtraKB < host.ReserveKB {
			message := fmt.Sprintf("宿主机可用内存不足，目标内存 %dMB，当前可用 %dMB，保留阈值 %dMB", targetMB, host.AvailableKB/1024, host.ReserveKB/1024)
			logger.App.Warn("动态内存跳过Windows弹性内存增长", "message", message, "vm", name, "targetMB", targetMB)
			finishMemorySchedulerEventFailed(event, message)
			return
		}
	}
	if err := setVirtioMemRequestedLive(name, targetMB-meta.MemoryInitialMB); err != nil {
		logger.App.Warn("动态内存调整Windows弹性内存失败", "vm", name, "targetMB", targetMB, "error", err)
		finishMemorySchedulerEventFailed(event, err.Error())
		return
	}
	finishMemorySchedulerEventSuccess(event, buildVirtioMemResultMessage(actualMB, targetMB, targetMB < actualMB))
	markMemoryAdjusted(name)
}

func calculateGuestUsedMemoryMB(stats *vmMemoryStatsValues) int {
	if stats == nil || stats.ActualKB <= 0 {
		return 0
	}
	usedKB := stats.ActualKB - stats.UnusedKB
	if stats.AvailableKB > 0 && stats.UsableKB > 0 {
		usedKB = stats.ActualKB - stats.UsableKB
	}
	if usedKB < 0 {
		usedKB = 0
	}
	if usedKB > stats.ActualKB {
		usedKB = stats.ActualKB
	}
	return int(usedKB / 1024)
}

func calculateVirtioMemScheduleTarget(actualMB, usedMB, initialMB, maxMB int) int {
	if actualMB <= 0 || usedMB < 0 || initialMB <= 0 || maxMB <= 0 {
		return actualMB
	}
	actualMB = maxInt(actualMB, initialMB)
	actualMB = minInt(actualMB, maxMB)
	usageRatio := float64(usedMB) / float64(actualMB)
	if usageRatio > 0.70 && actualMB < maxMB {
		return minInt(actualMB+1024, maxMB)
	}
	if usageRatio < 0.50 && actualMB > initialMB {
		targetMB := maxInt(ceilDivInt(usedMB*100, 70), initialMB)
		if targetMB < actualMB {
			return targetMB
		}
	}
	return actualMB
}

func ceilDivInt(a, b int) int {
	if b <= 0 {
		return 0
	}
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func percentConfig(value int, fallback int) float64 {
	if value <= 0 {
		value = fallback
	}
	return float64(value) / 100
}

func setVMMemoryLive(name string, targetMB int) error {
	result := utils.ExecCommand("virsh", "setmem", name, strconv.Itoa(targetMB*1024), "--live")
	if result.Error != nil {
		errText := strings.TrimSpace(result.Stderr)
		if errText == "" {
			errText = result.Error.Error()
		}
		logger.App.Warn("动态内存调整当前内存失败", "vm", name, "targetMB", targetMB, "error", errText)
		return fmt.Errorf("调整当前内存失败: %s", errText)
	}
	logger.App.Info("动态内存已调整当前内存", "vm", name, "targetMB", targetMB)
	return nil
}

func setVirtioMemRequestedLive(name string, requestedMB int) error {
	if requestedMB < 0 {
		requestedMB = 0
	}
	xmlResult := utils.ExecCommand("virsh", "dumpxml", name)
	if xmlResult.Error != nil {
		return fmt.Errorf("读取运行中虚拟机 XML 失败: %s", xmlResult.Stderr)
	}
	alias := findVirtioMemAlias(xmlResult.Stdout)
	if alias == "" {
		return fmt.Errorf("未找到 virtio-mem 设备，请确认 Windows 弹性内存已在关机状态下启用")
	}
	result := utils.ExecCommand("virsh", "update-memory-device", name, "--alias", alias, "--requested-size", strconv.Itoa(requestedMB*1024), "--live")
	if result.Error != nil {
		return fmt.Errorf("调整 Windows 弹性内存失败: %s", result.Stderr)
	}
	logger.App.Info("动态内存已调整virtio-mem requested", "vm", name, "requestedMB", requestedMB)
	return nil
}

func markMemoryAdjusted(name string) {
	memorySchedulerState.Lock()
	memorySchedulerState.lastAdjust[name] = time.Now()
	memorySchedulerState.lowUsable[name] = 0
	memorySchedulerState.highUnused[name] = 0
	memorySchedulerState.Unlock()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
