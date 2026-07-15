package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"kvm_console/service"
	"kvm_console/service/host"
)

type HostKVMIntelUnrestrictedGuestRequest struct {
	Enabled bool `json:"enabled"`
}

type HostKSMProfileRequest struct {
	Profile string `json:"profile" binding:"required"`
}

type HostZRAMProfileRequest struct {
	Profile string `json:"profile" binding:"required"`
}

// GetHostStats 获取宿主机资源信息
func GetHostStats(c *gin.Context) {
	stats, err := service.GetHostStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取宿主机信息失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    stats,
	})
}

// GetHostKVMIntelUnrestrictedGuestStatus 获取 Intel KVM unrestricted_guest 状态
func GetHostKVMIntelUnrestrictedGuestStatus(c *gin.Context) {
	status := service.GetHostKVMIntelUnrestrictedGuestStatus()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    status,
	})
}

// UpdateHostKVMIntelUnrestrictedGuest 设置 Intel KVM unrestricted_guest
func UpdateHostKVMIntelUnrestrictedGuest(c *gin.Context) {
	if !requireHighRiskVerification(c, "update_kvm_unrestricted_guest") {
		return
	}

	var req HostKVMIntelUnrestrictedGuestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	status, err := service.SetHostKVMIntelUnrestrictedGuest(req.Enabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "设置 KVM Unrestricted Guest 失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": status.Message,
		"data":    status,
	})
}

// GetHostKSMStatus 获取宿主机 KSM 状态
func GetHostKSMStatus(c *gin.Context) {
	status := service.GetHostKSMStatus()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    status,
	})
}

// UpdateHostKSMProfile 设置宿主机 KSM 挡位
func UpdateHostKSMProfile(c *gin.Context) {
	if !requireHighRiskVerification(c, "update_host_ksm") {
		return
	}

	var req HostKSMProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	status, err := service.SetHostKSMProfile(req.Profile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "设置 KSM 失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": status.Message,
		"data":    status,
	})
}

// GetHostZRAMStatus 获取宿主机 zRAM 状态
func GetHostZRAMStatus(c *gin.Context) {
	status := service.GetHostZRAMStatus()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    status,
	})
}

// UpdateHostZRAMProfile 设置宿主机 zRAM 挡位
func UpdateHostZRAMProfile(c *gin.Context) {
	if !requireHighRiskVerification(c, "update_host_zram") {
		return
	}

	var req HostZRAMProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	status, err := service.SetHostZRAMProfile(req.Profile)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "设置 zRAM 失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": status.Message,
		"data":    status,
	})
}

// GetHostStatsHistory 获取宿主机资源使用历史（按日期范围查询）
func GetHostStatsHistory(c *gin.Context) {
	startStr := c.Query("start")
	endStr := c.Query("end")

	if startStr == "" || endStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "请指定查询时间范围（start, end），格式: 2006-01-02 或 2006-01-02T15:04:05",
		})
		return
	}

	// 支持两种日期格式
	var start, end time.Time
	var err error

	start, err = time.ParseInLocation("2006-01-02", startStr, time.Local)
	if err != nil {
		start, err = time.ParseInLocation("2006-01-02T15:04:05", startStr, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": "start 日期格式错误，支持: 2006-01-02 或 2006-01-02T15:04:05",
			})
			return
		}
	}

	end, err = time.ParseInLocation("2006-01-02", endStr, time.Local)
	if err != nil {
		end, err = time.ParseInLocation("2006-01-02T15:04:05", endStr, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": "end 日期格式错误，支持: 2006-01-02 或 2006-01-02T15:04:05",
			})
			return
		}
	}

	// 如果 end 只有日期没有时间，将其设为当天的 23:59:59
	if end.Hour() == 0 && end.Minute() == 0 && end.Second() == 0 {
		end = end.Add(24*time.Hour - time.Second)
	}

	records, err := service.QueryHostStatsHistory(start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询历史记录失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    records,
	})
}

// GetHostDisks 获取宿主机挂载磁盘列表
func GetHostDisks(c *gin.Context) {
	disks, err := service.GetHostDiskInfos()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取磁盘列表失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    disks,
	})
}

// GetHostStatsSSE SSE 实时推送宿主机资源数据
func GetHostStatsSSE(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	ctx := c.Request.Context()

	// 立即发送一次
	sendHostStatsSSE(c.Writer)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendHostStatsSSE(c.Writer)
		}
	}
}

func sendHostStatsSSE(w http.ResponseWriter) {
	stats, err := service.GetHostStats()
	if err != nil {
		return
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// GetHostCPUCores 返回宿主机 CPU 核心总数
func GetHostCPUCores(c *gin.Context) {
	cores, err := service.GetSystemCPUCores()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取宿主机 CPU 信息失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": gin.H{
			"cores": cores,
		},
	})
}

// GetHardwarePassthroughStatus 获取硬件直通状态
func GetHardwarePassthroughStatus(c *gin.Context) {
	status := service.GetHardwarePassthroughStatus()
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    status,
	})
}

// EnableIommu 一键开启 IOMMU（写入 grub 参数并 update-grub）
func EnableIommu(c *gin.Context) {
	if !requireHighRiskVerification(c, "enable_host_iommu") {
		return
	}

	result := service.EnableIommuInGrub()
	code := 200
	if !result.Success {
		code = 400
	}
	c.JSON(code, gin.H{
		"code":    code,
		"message": result.Message,
		"data":    result,
	})
}

// LoadVfioPci 一键加载 vfio-pci 模块
func LoadVfioPci(c *gin.Context) {
	if !requireHighRiskVerification(c, "load_vfio_pci") {
		return
	}

	result := service.LoadVfioPciModule()
	code := 200
	if !result.Success {
		code = 400
	}
	c.JSON(code, gin.H{
		"code":    code,
		"message": result.Message,
		"data":    result,
	})
}

type CreateVGPUInstanceRequest struct {
	ProfileID uint `json:"profile_id" binding:"required"`
}

type AttachVGPURequest struct {
	VMName string `json:"vm_name" binding:"required"`
}

func GetVGPUProfiles(c *gin.Context) {
	profiles, err := host.GetVGPUProfiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取 vGPU 类型失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    profiles,
	})
}

func DiscoverVGPUProfiles(c *gin.Context) {
	if err := host.UpdateVGPUProfilesInDB(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "发现 vGPU 设备失败: " + err.Error(),
		})
		return
	}
	profiles, err := host.GetVGPUProfiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取 vGPU 类型失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "发现 vGPU 设备成功",
		"data":    profiles,
	})
}

func GetVGPUInstances(c *gin.Context) {
	instances, err := host.GetVGPUInstances()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "获取 vGPU 实例失败: " + err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data":    instances,
	})
}

func CreateVGPUInstance(c *gin.Context) {
	if !requireHighRiskVerification(c, "create_vgpu_instance") {
		return
	}

	var req CreateVGPUInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	instance, err := host.CreateVGPUInstance(req.ProfileID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "创建 vGPU 实例成功",
		"data":    instance,
	})
}

func DestroyVGPUInstance(c *gin.Context) {
	if !requireHighRiskVerification(c, "destroy_vgpu_instance") {
		return
	}

	uuid := c.Param("uuid")
	if err := host.DestroyVGPUInstanceByUUID(uuid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "删除 vGPU 实例成功",
	})
}

func AttachVGPUToVM(c *gin.Context) {
	if !requireHighRiskVerification(c, "attach_vgpu_to_vm") {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	var req AttachVGPURequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	if err := host.AttachVGPUToVM(uint(id), req.VMName); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "绑定 vGPU 到虚拟机成功",
	})
}

func DetachVGPUFromVM(c *gin.Context) {
	if !requireHighRiskVerification(c, "detach_vgpu_from_vm") {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数错误",
		})
		return
	}

	if err := host.DetachVGPUFromVM(uint(id)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "从虚拟机解绑 vGPU 成功",
	})
}
