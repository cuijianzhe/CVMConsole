package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"kvm_console/config"
	"kvm_console/logger"
	"kvm_console/model"
)

const (
	portForwardProbeSchedulerGroup = "网络安全"
	portForwardProbeSchedulerKey   = "port_forward_http_probe"
	portForwardProbeSchedulerName  = "端口转发 HTTP 探测"
	portForwardProbeBanReason      = "检测到存在建站或HTTP访问且未报备，当前转发已封禁，请联系管理员"

	PortForwardProbeStatusNotApplicable       = "not_applicable"
	PortForwardProbeStatusPending             = "pending"
	PortForwardProbeStatusClear               = "clear"
	PortForwardProbeStatusHTTPBanned          = "http_banned"
	PortForwardProbeStatusHTTPWhitelisted     = "http_whitelisted"
	PortForwardProbeStatusRestoredByWhitelist = "restored_by_whitelist"
	PortForwardProbeStatusError               = "error"
	PortForwardWhitelistScopeAdmin            = "admin"
	PortForwardWhitelistScopeNone             = ""
)

type PortForwardWhitelistSummary struct {
	VMName               string `json:"vm_name"`
	Username             string `json:"username"`
	UserWhitelisted      bool   `json:"user_whitelisted"`
	VMWhitelisted        bool   `json:"vm_whitelisted"`
	EffectiveWhitelisted bool   `json:"effective_whitelisted"`
	EffectiveScope       string `json:"effective_scope"`
}

type PortForwardWhitelistList struct {
	Users []model.PortForwardWhitelist `json:"users"`
	VMs   []model.PortForwardWhitelist `json:"vms"`
}

type PortForwardHTTPProbeTaskParams struct {
	VMName string `json:"vm_name"`
}

type PortForwardHTTPProbeRunResult struct {
	Scanned      int      `json:"scanned"`
	Banned       int      `json:"banned"`
	Whitelisted  int      `json:"whitelisted"`
	Clear        int      `json:"clear"`
	Skipped      int      `json:"skipped"`
	Errors       int      `json:"errors"`
	MatchedVM    string   `json:"matched_vm"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

type portForwardWhitelistSet struct {
	user map[string]bool
	vm   map[string]bool
}

var portForwardProbeRegisterOnce sync.Once

func registerPortForwardProbeScheduler() {
	portForwardProbeRegisterOnce.Do(func() {
		RegisterScheduler(SchedulerDefinition{
			Key:         portForwardProbeSchedulerKey,
			Name:        portForwardProbeSchedulerName,
			Group:       portForwardProbeSchedulerGroup,
			Description: "每小时探测 TCP 端口转发是否暴露明文 HTTP 建站服务，并按白名单自动封禁。",
			Enabled: func() bool {
				return config.GlobalConfig == nil || config.GlobalConfig.PortForwardHTTPProbeEnabled
			},
		})
	})
}

// StartPortForwardHTTPProbeScheduler 启动端口转发 HTTP 探测调度器。
func StartPortForwardHTTPProbeScheduler() {
	registerPortForwardProbeScheduler()
	go func() {
		for {
			intervalMinutes := 60
			if config.GlobalConfig != nil && config.GlobalConfig.PortForwardHTTPProbeIntervalMinutes > 0 {
				intervalMinutes = config.GlobalConfig.PortForwardHTTPProbeIntervalMinutes
			}
			if !IsMaintenanceModeEnabled() && (config.GlobalConfig == nil || config.GlobalConfig.PortForwardHTTPProbeEnabled) {
				if _, err := RunPortForwardHTTPProbeScan(context.Background(), "", "scheduler", nil); err != nil {
					logger.App.Warn("端口转发HTTP探测调度执行失败", "error", err)
				}
			}
			time.Sleep(time.Duration(intervalMinutes) * time.Minute)
		}
	}()
}

func ListPortForwardWhitelists() (*PortForwardWhitelistList, error) {
	var rows []model.PortForwardWhitelist
	if err := model.DB.Order("scope_type ASC, scope_value ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &PortForwardWhitelistList{
		Users: make([]model.PortForwardWhitelist, 0),
		VMs:   make([]model.PortForwardWhitelist, 0),
	}
	for _, row := range rows {
		switch strings.TrimSpace(row.ScopeType) {
		case model.PortForwardWhitelistScopeUser:
			result.Users = append(result.Users, row)
		case model.PortForwardWhitelistScopeVM:
			result.VMs = append(result.VMs, row)
		}
	}
	return result, nil
}

func AddPortForwardWhitelist(scopeType, scopeValue, createdBy string) (*model.PortForwardWhitelist, []string, error) {
	scopeType = strings.TrimSpace(scopeType)
	scopeValue = strings.TrimSpace(scopeValue)
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		createdBy = "admin"
	}
	if scopeType != model.PortForwardWhitelistScopeUser && scopeType != model.PortForwardWhitelistScopeVM {
		return nil, nil, fmt.Errorf("白名单类型无效")
	}
	if scopeValue == "" {
		return nil, nil, fmt.Errorf("白名单值不能为空")
	}
	if scopeType == model.PortForwardWhitelistScopeUser {
		var user model.User
		if err := model.DB.Where("username = ?", scopeValue).First(&user).Error; err != nil {
			return nil, nil, fmt.Errorf("用户不存在")
		}
	} else {
		if _, err := GetVM(scopeValue); err != nil {
			return nil, nil, fmt.Errorf("虚拟机不存在")
		}
	}

	row := &model.PortForwardWhitelist{
		ScopeType:  scopeType,
		ScopeValue: scopeValue,
		CreatedBy:  createdBy,
	}
	if err := model.DB.Where("scope_type = ? AND scope_value = ?", scopeType, scopeValue).
		Assign(model.PortForwardWhitelist{CreatedBy: createdBy}).
		FirstOrCreate(row).Error; err != nil {
		return nil, nil, err
	}

	warnings, err := restoreBannedPortForwardsByWhitelist(scopeType, scopeValue)
	if err != nil {
		return nil, warnings, err
	}
	warnings = append(warnings, healWhitelistedPortForwardsByWhitelist(scopeType, scopeValue)...)
	return row, warnings, nil
}

func DeletePortForwardWhitelist(scopeType, scopeValue string) error {
	scopeType = strings.TrimSpace(scopeType)
	scopeValue = strings.TrimSpace(scopeValue)
	if scopeType == "" || scopeValue == "" {
		return fmt.Errorf("白名单参数不能为空")
	}
	return model.DB.Where("scope_type = ? AND scope_value = ?", scopeType, scopeValue).Delete(&model.PortForwardWhitelist{}).Error
}

func GetPortForwardWhitelistSummary(vmName, username, role string) (*PortForwardWhitelistSummary, error) {
	vmName = strings.TrimSpace(vmName)
	username = strings.TrimSpace(username)
	role = strings.TrimSpace(role)
	if vmName == "" {
		return nil, fmt.Errorf("虚拟机名称不能为空")
	}
	if username == "" {
		username = strings.TrimSpace(FindVMOwner(vmName))
	}
	summary := &PortForwardWhitelistSummary{
		VMName:   vmName,
		Username: username,
	}
	if role == "admin" || username == "admin" {
		summary.UserWhitelisted = true
		summary.EffectiveWhitelisted = true
		summary.EffectiveScope = PortForwardWhitelistScopeAdmin
		return summary, nil
	}
	sets, err := loadPortForwardWhitelistSet()
	if err != nil {
		return nil, err
	}
	summary.UserWhitelisted = sets.user[username]
	summary.VMWhitelisted = sets.vm[vmName]
	summary.EffectiveWhitelisted = summary.UserWhitelisted || summary.VMWhitelisted
	switch {
	case summary.UserWhitelisted:
		summary.EffectiveScope = model.PortForwardWhitelistScopeUser
	case summary.VMWhitelisted:
		summary.EffectiveScope = model.PortForwardWhitelistScopeVM
	default:
		summary.EffectiveScope = PortForwardWhitelistScopeNone
	}
	return summary, nil
}

func RunPortForwardHTTPProbeScan(ctx context.Context, vmName, trigger string, progress func(int, string)) (*PortForwardHTTPProbeRunResult, error) {
	registerPortForwardProbeScheduler()
	if progress == nil {
		progress = func(int, string) {}
	}
	liveRules, err := listLivePortForwardsFromIPTables()
	if err != nil {
		return nil, err
	}
	vmName = strings.TrimSpace(vmName)
	filtered := make([]PortForwardRule, 0, len(liveRules))
	for _, rule := range liveRules {
		if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
			continue
		}
		if vmName != "" && strings.TrimSpace(rule.VMName) != vmName {
			continue
		}
		filtered = append(filtered, rule)
	}

	result := &PortForwardHTTPProbeRunResult{
		Scanned:   len(filtered),
		MatchedVM: vmName,
	}
	if len(filtered) == 0 {
		progress(100, "没有需要探测的 TCP 端口转发")
		return result, nil
	}

	whitelistSet, err := loadPortForwardWhitelistSet()
	if err != nil {
		return nil, err
	}
	timeoutSeconds := 3
	if config.GlobalConfig != nil && config.GlobalConfig.PortForwardHTTPProbeTimeoutSeconds > 0 {
		timeoutSeconds = config.GlobalConfig.PortForwardHTTPProbeTimeoutSeconds
	}
	timeout := time.Duration(timeoutSeconds) * time.Second

	for idx, rule := range filtered {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		progressPercent := int(float64(idx) / float64(len(filtered)) * 100)
		progress(progressPercent, fmt.Sprintf("正在探测 %s (%d/%d)", rule.AccessAddress, idx+1, len(filtered)))
		if err := runPortForwardProbeForRule(&rule, whitelistSet, timeout, trigger); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails, err.Error())
			continue
		}

		state, stateErr := getPortForwardProbeStateByRuleKey(rule.RuleKey)
		if stateErr != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails, stateErr.Error())
			continue
		}
		if state == nil {
			result.Skipped++
			continue
		}
		switch strings.TrimSpace(state.LastResult) {
		case PortForwardProbeStatusHTTPBanned:
			result.Banned++
		case PortForwardProbeStatusHTTPWhitelisted:
			result.Whitelisted++
		case PortForwardProbeStatusClear:
			result.Clear++
		case PortForwardProbeStatusError:
			result.Errors++
			if strings.TrimSpace(state.LastError) != "" {
				result.ErrorDetails = append(result.ErrorDetails, state.LastError)
			}
		default:
			result.Skipped++
		}
	}

	progress(100, fmt.Sprintf("探测完成，扫描 %d 条 TCP 转发", result.Scanned))
	return result, nil
}

func ExecuteManualPortForwardHTTPProbe(ctx context.Context, task *PortForwardHTTPProbeTaskParams, createdBy string, progress func(int, string)) (string, error) {
	vmName := ""
	if task != nil {
		vmName = strings.TrimSpace(task.VMName)
	}
	result, err := RunPortForwardHTTPProbeScan(ctx, vmName, "manual", progress)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"scanned":%d,"banned":%d,"whitelisted":%d,"clear":%d,"skipped":%d,"errors":%d,"matched_vm":%q}`,
		result.Scanned, result.Banned, result.Whitelisted, result.Clear, result.Skipped, result.Errors, result.MatchedVM), nil
}

func DeleteBannedPortForwardByRuleKey(ruleKey string) error {
	ruleKey = strings.TrimSpace(ruleKey)
	if ruleKey == "" {
		return fmt.Errorf("规则标识不能为空")
	}
	state, err := getPortForwardProbeStateByRuleKey(ruleKey)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("封禁记录不存在")
	}
	if state.Live {
		return fmt.Errorf("该端口转发当前仍处于启用状态，请使用常规删除接口")
	}
	return model.DB.Where("rule_key = ?", ruleKey).Delete(&model.PortForwardProbeState{}).Error
}

func SyncPortForwardProbeStateOnAdd(params *PortForwardAddParams, protocol string, ownerUsername string) {
	if params == nil {
		return
	}
	ownerUsername = strings.TrimSpace(ownerUsername)
	vmName := strings.TrimSpace(params.Comment)
	if vmName == "" {
		vmName = strings.TrimSpace(params.VMIP)
	}
	rule := PortForwardRule{
		Protocol:      strings.ToUpper(strings.TrimSpace(protocol)),
		HostPort:      strings.TrimSpace(params.HostPort),
		DestIP:        strings.TrimSpace(params.VMIP),
		DestPort:      strings.TrimSpace(params.VMPort),
		VMName:        vmName,
		OwnerUsername: ownerUsername,
	}
	rule.RuleKey = rule.StableKey()
	now := time.Now()
	state := &model.PortForwardProbeState{
		RuleKey:            rule.RuleKey,
		Protocol:           strings.ToLower(strings.TrimSpace(protocol)),
		HostPort:           rule.HostPort,
		DestIP:             rule.DestIP,
		DestPort:           rule.DestPort,
		VMName:             rule.VMName,
		OwnerUsername:      ownerUsername,
		CreatedBy:          strings.TrimSpace(params.CreatedBy),
		CreatedByAdmin:     params.CreatedByAdmin,
		Live:               true,
		Banned:             false,
		LastResult:         PortForwardProbeStatusPending,
		LastError:          "",
		BanReason:          "",
		WhitelistScope:     "",
		LastCheckedAt:      &now,
		LastHTTPStatusCode: 0,
		BannedAt:           nil,
	}
	_ = upsertPortForwardProbeState(state)
}

func SyncPortForwardProbeStateOnDelete(ruleKey string, deletedByBan bool) {
	ruleKey = strings.TrimSpace(ruleKey)
	if ruleKey == "" {
		return
	}
	state, err := getPortForwardProbeStateByRuleKey(ruleKey)
	if err != nil || state == nil {
		return
	}
	if !deletedByBan {
		_ = model.DB.Where("rule_key = ?", ruleKey).Delete(&model.PortForwardProbeState{}).Error
		return
	}
	state.Live = false
	_ = upsertPortForwardProbeState(state)
}

func MergePortForwardProbeState(rules []PortForwardRule) []PortForwardRule {
	stateMap, err := getPortForwardProbeStateMap()
	if err != nil {
		return rules
	}
	seen := make(map[string]bool, len(rules))
	merged := make([]PortForwardRule, 0, len(rules))
	for _, rule := range rules {
		rule.RuleKey = rule.StableKey()
		rule.Live = true
		applyProbeStateToRule(&rule, stateMap[rule.RuleKey])
		if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") && rule.ProbeStatus == "" {
			rule.ProbeStatus = PortForwardProbeStatusNotApplicable
			rule.ProbeReason = "仅 TCP 转发参与 HTTP 探测"
		}
		seen[rule.RuleKey] = true
		merged = append(merged, rule)
	}
	for _, state := range stateMap {
		if state == nil || !state.Banned || state.Live || seen[state.RuleKey] {
			continue
		}
		rule := buildPortForwardRuleFromProbeState(state)
		if rule.RuleKey == "" {
			continue
		}
		seen[rule.RuleKey] = true
		merged = append(merged, rule)
	}
	return merged
}

func loadPortForwardWhitelistSet() (*portForwardWhitelistSet, error) {
	var rows []model.PortForwardWhitelist
	if err := model.DB.Find(&rows).Error; err != nil {
		return nil, err
	}
	result := &portForwardWhitelistSet{
		user: make(map[string]bool),
		vm:   make(map[string]bool),
	}
	for _, row := range rows {
		switch strings.TrimSpace(row.ScopeType) {
		case model.PortForwardWhitelistScopeUser:
			result.user[strings.TrimSpace(row.ScopeValue)] = true
		case model.PortForwardWhitelistScopeVM:
			result.vm[strings.TrimSpace(row.ScopeValue)] = true
		}
	}
	return result, nil
}

func (s *portForwardWhitelistSet) Match(ownerUsername, vmName string, createdByAdmin bool) string {
	ownerUsername = strings.TrimSpace(ownerUsername)
	vmName = strings.TrimSpace(vmName)
	if ownerUsername == "admin" || createdByAdmin {
		return PortForwardWhitelistScopeAdmin
	}
	if s == nil {
		return PortForwardWhitelistScopeNone
	}
	if s.user[ownerUsername] {
		return model.PortForwardWhitelistScopeUser
	}
	if s.vm[vmName] {
		return model.PortForwardWhitelistScopeVM
	}
	return PortForwardWhitelistScopeNone
}

func runPortForwardProbeForRule(rule *PortForwardRule, whitelistSet *portForwardWhitelistSet, timeout time.Duration, trigger string) error {
	if rule == nil {
		return nil
	}
	rule.RuleKey = rule.StableKey()
	state, err := getPortForwardProbeStateByRuleKey(rule.RuleKey)
	if err != nil {
		return err
	}
	if state == nil {
		state = &model.PortForwardProbeState{
			RuleKey:       rule.RuleKey,
			Protocol:      strings.ToLower(strings.TrimSpace(rule.Protocol)),
			HostPort:      strings.TrimSpace(rule.HostPort),
			DestIP:        strings.TrimSpace(rule.DestIP),
			DestPort:      strings.TrimSpace(rule.DestPort),
			VMName:        strings.TrimSpace(rule.VMName),
			OwnerUsername: strings.TrimSpace(rule.OwnerUsername),
		}
	}

	state.Live = true
	state.Protocol = strings.ToLower(strings.TrimSpace(rule.Protocol))
	state.HostPort = strings.TrimSpace(rule.HostPort)
	state.DestIP = strings.TrimSpace(rule.DestIP)
	state.DestPort = strings.TrimSpace(rule.DestPort)
	state.VMName = strings.TrimSpace(rule.VMName)
	state.OwnerUsername = strings.TrimSpace(rule.OwnerUsername)
	now := time.Now()
	state.LastCheckedAt = &now
	state.LastError = ""
	state.LastHTTPStatusCode = 0

	if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") {
		state.LastResult = PortForwardProbeStatusNotApplicable
		state.Banned = false
		state.BanReason = ""
		state.WhitelistScope = ""
		return upsertPortForwardProbeState(state)
	}

	httpDetected, statusCode, probeErr := detectHTTPService(rule.DestIP, rule.DestPort, timeout)
	if probeErr != nil {
		state.LastResult = PortForwardProbeStatusError
		state.LastError = probeErr.Error()
		state.WhitelistScope = ""
		return upsertPortForwardProbeState(state)
	}
	if !httpDetected {
		state.Banned = false
		state.BanReason = ""
		state.BannedAt = nil
		state.LastResult = PortForwardProbeStatusClear
		state.LastError = ""
		state.WhitelistScope = ""
		return upsertPortForwardProbeState(state)
	}

	state.LastHTTPStatusCode = statusCode
	scope := whitelistSet.Match(rule.OwnerUsername, rule.VMName, state.CreatedByAdmin)
	state.WhitelistScope = scope
	if scope != PortForwardWhitelistScopeNone {
		state.Banned = false
		state.BanReason = ""
		state.BannedAt = nil
		state.LastResult = PortForwardProbeStatusHTTPWhitelisted
		state.LastError = ""
		return upsertPortForwardProbeState(state)
	}

	event := startPortForwardProbeEvent(rule, statusCode)
	state.Banned = true
	state.Live = false
	state.LastResult = PortForwardProbeStatusHTTPBanned
	state.BanReason = portForwardProbeBanReason
	state.LastError = ""
	state.BannedAt = &now
	if err := upsertPortForwardProbeState(state); err != nil {
		finishPortForwardProbeEventFailure(event, fmt.Sprintf("写入封禁状态失败: %v", err))
		return err
	}
	if err := deleteLivePortForwardByStableKey(rule.RuleKey, true); err != nil {
		state.Live = true
		_ = upsertPortForwardProbeState(state)
		finishPortForwardProbeEventFailure(event, fmt.Sprintf("自动封禁失败: %v", err))
		return fmt.Errorf("自动封禁 %s 失败: %w", rule.AccessAddress, err)
	}
	finishPortForwardProbeEventSuccess(event, fmt.Sprintf("已自动封禁 %s，命中 HTTP 状态码 %d", rule.AccessAddress, statusCode))
	_ = trigger
	return nil
}

func detectHTTPService(destIP, destPort string, timeout time.Duration) (bool, int, error) {
	target := net.JoinHostPort(strings.TrimSpace(destIP), strings.TrimSpace(destPort))
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false, 0, nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	host := strings.TrimSpace(destIP)
	if host == "" {
		host = "localhost"
	}
	request := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\nUser-Agent: kvm-console-port-probe\r\n\r\n", host)
	if _, err := conn.Write([]byte(request)); err != nil {
		return false, 0, nil
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, 0, nil
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "HTTP/") {
		return false, 0, nil
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return false, 0, nil
	}
	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return false, 0, nil
	}
	return true, statusCode, nil
}

func restoreBannedPortForwardsByWhitelist(scopeType, scopeValue string) ([]string, error) {
	var states []model.PortForwardProbeState
	query := model.DB.Where("banned = ?", true)
	switch strings.TrimSpace(scopeType) {
	case model.PortForwardWhitelistScopeUser:
		query = query.Where("owner_username = ?", strings.TrimSpace(scopeValue))
	case model.PortForwardWhitelistScopeVM:
		query = query.Where("vm_name = ?", strings.TrimSpace(scopeValue))
	default:
		return nil, nil
	}
	if err := query.Find(&states).Error; err != nil {
		return nil, err
	}
	warnings := make([]string, 0)
	for i := range states {
		if err := restorePortForwardProbeState(&states[i], scopeType); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 恢复失败: %v", states[i].RuleKey, err))
		}
	}
	return warnings, nil
}

func healWhitelistedPortForwardsByWhitelist(scopeType, scopeValue string) []string {
	var states []model.PortForwardProbeState
	query := model.DB.Where("live = ?", true)
	switch strings.TrimSpace(scopeType) {
	case model.PortForwardWhitelistScopeUser:
		query = query.Where("owner_username = ?", strings.TrimSpace(scopeValue))
	case model.PortForwardWhitelistScopeVM:
		query = query.Where("vm_name = ?", strings.TrimSpace(scopeValue))
	default:
		return nil
	}
	if err := query.Find(&states).Error; err != nil {
		return []string{fmt.Sprintf("%s 白名单修复失败: %v", scopeValue, err)}
	}
	warnings := make([]string, 0)
	for i := range states {
		state := states[i]
		liveRule, err := findLivePortForwardByStableKey(state.RuleKey)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 校验失败: %v", state.RuleKey, err))
			continue
		}
		if liveRule == nil {
			continue
		}
		vmName := firstNonEmptyString(state.VMName, liveRule.VMName)
		protocol := firstNonEmptyString(strings.ToLower(strings.TrimSpace(state.Protocol)), strings.ToLower(strings.TrimSpace(liveRule.Protocol)), "tcp")
		portText := firstNonEmptyString(state.DestPort, liveRule.DestPort)
		if err := EnsureSecurityGroupAllowsPortForward(vmName, protocol, portText); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s 修复安全组失败: %v", state.RuleKey, err))
		}
	}
	return warnings
}

func restorePortForwardProbeState(state *model.PortForwardProbeState, whitelistScope string) error {
	if state == nil {
		return nil
	}
	originalState := *state
	if liveRule, _ := findLivePortForwardByStableKey(state.RuleKey); liveRule != nil {
		restoreVMName := firstNonEmptyString(strings.TrimSpace(state.VMName), strings.TrimSpace(liveRule.VMName))
		restoreProtocol := firstNonEmptyString(strings.ToLower(strings.TrimSpace(state.Protocol)), strings.ToLower(strings.TrimSpace(liveRule.Protocol)))
		restorePort := firstNonEmptyString(strings.TrimSpace(state.DestPort), strings.TrimSpace(liveRule.DestPort))
		if err := EnsureSecurityGroupAllowsPortForward(restoreVMName, restoreProtocol, restorePort); err != nil {
			return fmt.Errorf("补充安全组放行失败: %w", err)
		}
		now := time.Now()
		state.Live = true
		state.Banned = false
		state.BanReason = ""
		state.BannedAt = nil
		state.LastResult = PortForwardProbeStatusRestoredByWhitelist
		state.LastError = ""
		state.LastCheckedAt = &now
		state.WhitelistScope = whitelistScope
		return upsertPortForwardProbeState(state)
	}
	restoreVMName := strings.TrimSpace(state.VMName)
	restoreVMIP := resolvePortForwardRestoreIP(state)
	restoreOwnerUsername := resolvePortForwardRestoreOwner(state)
	restoreProtocol := firstNonEmptyString(strings.ToLower(strings.TrimSpace(state.Protocol)), "tcp")
	restoreRuleKey := PortForwardRule{
		Protocol: restoreProtocol,
		HostPort: strings.TrimSpace(state.HostPort),
		DestIP:   restoreVMIP,
		DestPort: strings.TrimSpace(state.DestPort),
	}.StableKey()
	params := &PortForwardAddParams{
		VMIP:           restoreVMIP,
		HostPort:       state.HostPort,
		VMPort:         state.DestPort,
		Protocol:       restoreProtocol,
		Comment:        restoreVMName,
		CreatedBy:      strings.TrimSpace(state.CreatedBy),
		CreatedByAdmin: state.CreatedByAdmin,
	}
	if err := AddPortForward(params); err != nil {
		return err
	}
	if err := EnsureSecurityGroupAllowsPortForward(restoreVMName, restoreProtocol, state.DestPort); err != nil {
		_ = deleteLivePortForwardByStableKey(restoreRuleKey, true)
		originalState.Live = false
		_ = upsertPortForwardProbeState(&originalState)
		return fmt.Errorf("恢复安全组放行失败: %w", err)
	}
	now := time.Now()
	if restoreRuleKey != originalState.RuleKey {
		_ = model.DB.Where("rule_key = ?", originalState.RuleKey).Delete(&model.PortForwardProbeState{}).Error
	}
	state.RuleKey = restoreRuleKey
	state.ID = 0
	state.Protocol = restoreProtocol
	state.DestIP = restoreVMIP
	state.OwnerUsername = restoreOwnerUsername
	state.CreatedBy = strings.TrimSpace(params.CreatedBy)
	state.CreatedByAdmin = params.CreatedByAdmin
	state.Live = true
	state.Banned = false
	state.BanReason = ""
	state.BannedAt = nil
	state.LastResult = PortForwardProbeStatusRestoredByWhitelist
	state.LastError = ""
	state.LastCheckedAt = &now
	state.WhitelistScope = whitelistScope
	return upsertPortForwardProbeState(state)
}

func resolvePortForwardRestoreIP(state *model.PortForwardProbeState) string {
	if state == nil {
		return ""
	}
	vmName := strings.TrimSpace(state.VMName)
	if vmName != "" {
		if ip, err := EnsureStaticIP(vmName); err == nil && strings.TrimSpace(ip) != "" {
			return strings.TrimSpace(ip)
		}
		if ip := strings.TrimSpace(getFirewallVMIP(vmName)); ip != "" {
			return ip
		}
	}
	return strings.TrimSpace(state.DestIP)
}

func resolvePortForwardRestoreOwner(state *model.PortForwardProbeState) string {
	if state == nil {
		return ""
	}
	vmName := strings.TrimSpace(state.VMName)
	if vmName != "" {
		if owner := strings.TrimSpace(FindVMOwner(vmName)); owner != "" {
			return owner
		}
	}
	return strings.TrimSpace(state.OwnerUsername)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func getPortForwardProbeStateByRuleKey(ruleKey string) (*model.PortForwardProbeState, error) {
	var state model.PortForwardProbeState
	if err := model.DB.Where("rule_key = ?", strings.TrimSpace(ruleKey)).First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func getPortForwardProbeStateMap() (map[string]*model.PortForwardProbeState, error) {
	var states []model.PortForwardProbeState
	if err := model.DB.Find(&states).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*model.PortForwardProbeState, len(states))
	for i := range states {
		state := states[i]
		result[strings.TrimSpace(state.RuleKey)] = &state
	}
	return result, nil
}

func upsertPortForwardProbeState(state *model.PortForwardProbeState) error {
	if state == nil || strings.TrimSpace(state.RuleKey) == "" {
		return nil
	}
	var existing model.PortForwardProbeState
	if err := model.DB.Where("rule_key = ?", state.RuleKey).First(&existing).Error; err == nil {
		state.ID = existing.ID
		if strings.TrimSpace(state.CreatedBy) == "" {
			state.CreatedBy = strings.TrimSpace(existing.CreatedBy)
		}
		if !state.CreatedByAdmin {
			state.CreatedByAdmin = existing.CreatedByAdmin
		}
		return model.DB.Model(&existing).Updates(map[string]interface{}{
			"protocol":              state.Protocol,
			"host_port":             state.HostPort,
			"dest_ip":               state.DestIP,
			"dest_port":             state.DestPort,
			"vm_name":               state.VMName,
			"owner_username":        state.OwnerUsername,
			"created_by":            state.CreatedBy,
			"created_by_admin":      state.CreatedByAdmin,
			"live":                  state.Live,
			"banned":                state.Banned,
			"whitelist_scope":       state.WhitelistScope,
			"last_checked_at":       state.LastCheckedAt,
			"last_http_status_code": state.LastHTTPStatusCode,
			"last_result":           state.LastResult,
			"last_error":            state.LastError,
			"ban_reason":            state.BanReason,
			"banned_at":             state.BannedAt,
		}).Error
	}
	return model.DB.Create(state).Error
}

func applyProbeStateToRule(rule *PortForwardRule, state *model.PortForwardProbeState) {
	if rule == nil {
		return
	}
	rule.Live = true
	rule.RuleKey = rule.StableKey()
	if state == nil {
		if strings.EqualFold(strings.TrimSpace(rule.Protocol), "tcp") {
			rule.ProbeStatus = PortForwardProbeStatusPending
			rule.ProbeReason = "等待首次 HTTP 探测"
		}
		return
	}
	rule.Live = state.Live || !state.Banned
	rule.Banned = state.Banned
	rule.ProbeStatus = strings.TrimSpace(state.LastResult)
	rule.ProbeReason = strings.TrimSpace(state.BanReason)
	if rule.ProbeReason == "" {
		if state.LastResult == PortForwardProbeStatusHTTPWhitelisted {
			rule.ProbeReason = buildWhitelistReason(state.WhitelistScope, state.LastHTTPStatusCode)
		} else if state.LastResult == PortForwardProbeStatusClear {
			rule.ProbeReason = "最近一次探测未发现明文 HTTP 服务"
		} else if state.LastResult == PortForwardProbeStatusError {
			rule.ProbeReason = strings.TrimSpace(state.LastError)
		}
	}
	rule.ProbeWhitelistScope = strings.TrimSpace(state.WhitelistScope)
	rule.ProbeHTTPStatusCode = state.LastHTTPStatusCode
	rule.ProbeLastCheckedAt = formatTimePointer(state.LastCheckedAt)
	if strings.EqualFold(strings.TrimSpace(rule.Protocol), "udp") && rule.ProbeStatus == "" {
		rule.ProbeStatus = PortForwardProbeStatusNotApplicable
		rule.ProbeReason = "仅 TCP 转发参与 HTTP 探测"
	}
}

func buildPortForwardRuleFromProbeState(state *model.PortForwardProbeState) PortForwardRule {
	rule := PortForwardRule{
		ID:                    -int(state.ID),
		Protocol:              strings.ToUpper(strings.TrimSpace(state.Protocol)),
		HostPort:              strings.TrimSpace(state.HostPort),
		AccessIP:              getHostIP(),
		AccessAddress:         buildPortForwardAccessAddress(getHostIP(), state.HostPort),
		DestIP:                strings.TrimSpace(state.DestIP),
		DestPort:              strings.TrimSpace(state.DestPort),
		VMName:                strings.TrimSpace(state.VMName),
		OwnerUsername:         strings.TrimSpace(state.OwnerUsername),
		FirewallKey:           strings.TrimSpace(state.RuleKey),
		RegionFilterEnabled:   true,
		RegionFilterInherited: true,
		RuleKey:               strings.TrimSpace(state.RuleKey),
		Live:                  false,
		Banned:                state.Banned,
	}
	applyProbeStateToRule(&rule, state)
	rule.Live = false
	return rule
}

func formatTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02 15:04:05")
}

func buildWhitelistReason(scope string, statusCode int) string {
	switch strings.TrimSpace(scope) {
	case PortForwardWhitelistScopeAdmin:
		return fmt.Sprintf("检测到明文 HTTP 响应，状态码 %d，但该转发归属管理员，默认白名单放行", statusCode)
	case model.PortForwardWhitelistScopeUser:
		return fmt.Sprintf("检测到明文 HTTP 响应，状态码 %d，但归属用户已在白名单中", statusCode)
	case model.PortForwardWhitelistScopeVM:
		return fmt.Sprintf("检测到明文 HTTP 响应，状态码 %d，但虚拟机已在白名单中", statusCode)
	default:
		return fmt.Sprintf("检测到明文 HTTP 响应，状态码 %d，但当前规则命中白名单", statusCode)
	}
}

func startPortForwardProbeEvent(rule *PortForwardRule, statusCode int) *model.SchedulerEvent {
	if rule == nil {
		return nil
	}
	return startPortForwardProbeSchedulerEvent(rule.VMName, strings.ToLower(strings.TrimSpace(rule.Protocol)), fmt.Sprintf("探测到明文 HTTP 响应状态码 %d，准备自动封禁端口转发 %s", statusCode, rule.AccessAddress))
}

func startPortForwardProbeSchedulerEvent(vmName, backend, reason string) *model.SchedulerEvent {
	event, err := StartSchedulerEvent(SchedulerEventStartInput{
		SchedulerKey:   portForwardProbeSchedulerKey,
		SchedulerName:  portForwardProbeSchedulerName,
		SchedulerGroup: portForwardProbeSchedulerGroup,
		VMName:         strings.TrimSpace(vmName),
		VMBackend:      strings.TrimSpace(backend),
		TriggerReason:  strings.TrimSpace(reason),
	})
	if err != nil {
		logger.App.Warn("端口转发HTTP探测记录调度事件失败", "error", err)
		return nil
	}
	return event
}

func finishPortForwardProbeEventSuccess(event *model.SchedulerEvent, message string) {
	if event == nil {
		return
	}
	if err := FinishSchedulerEventSuccess(event, message); err != nil {
		logger.App.Warn("端口转发HTTP探测更新成功事件失败", "error", err)
	}
}

func finishPortForwardProbeEventFailure(event *model.SchedulerEvent, message string) {
	if event == nil {
		return
	}
	if err := FinishSchedulerEventFailed(event, message); err != nil {
		logger.App.Warn("端口转发HTTP探测更新失败事件失败", "error", err)
	}
}
