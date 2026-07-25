package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kvm_console/model"
)

func ListCloudDiskSpecs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	keyword := c.Query("keyword")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := model.DB.Model(&model.CloudDiskSpec{})

	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询云盘规格列表失败: " + err.Error(),
		})
		return
	}

	var specs []model.CloudDiskSpec
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&specs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询云盘规格列表失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data": model.CloudDiskSpecListResponse{
			List:     specs,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
	})
}

func CreateCloudDiskSpec(c *gin.Context) {
	var req model.CreateCloudDiskSpecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数校验失败: " + err.Error(),
		})
		return
	}

	if err := validateCloudDiskSpecRequest(c, &req); err != nil {
		return
	}

	var existing model.CloudDiskSpec
	if err := model.DB.Where("name = ?", req.Name).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"message": "规格名称已存在",
		})
		return
	}

	spec := buildCloudDiskSpecFromRequest(&req)

	if err := model.DB.Create(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "创建云盘规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "创建成功",
		"data":    spec,
	})
}

func UpdateCloudDiskSpec(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "无效的规格 ID",
		})
		return
	}

	var req model.UpdateCloudDiskSpecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数校验失败: " + err.Error(),
		})
		return
	}

	if err := validateCloudDiskSpecUpdateRequest(c, &req); err != nil {
		return
	}

	var spec model.CloudDiskSpec
	if err := model.DB.First(&spec, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "云盘规格不存在",
		})
		return
	}

	var existing model.CloudDiskSpec
	if err := model.DB.Where("name = ? AND id != ?", req.Name, id).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"message": "规格名称已存在",
		})
		return
	}

	spec.Name = req.Name
	spec.CapacityGB = req.CapacityGB
	spec.IOPSMode = req.IOPSMode
	spec.TotalIOPS = req.TotalIOPS
	spec.ReadIOPS = req.ReadIOPS
	spec.WriteIOPS = req.WriteIOPS
	spec.Description = req.Description

	if err := model.DB.Save(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "更新云盘规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "更新成功",
		"data":    spec,
	})
}

func DeleteCloudDiskSpec(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "无效的规格 ID",
		})
		return
	}

	var spec model.CloudDiskSpec
	if err := model.DB.First(&spec, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "云盘规格不存在",
		})
		return
	}

	if err := model.DB.Delete(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "删除云盘规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "删除成功",
	})
}

func BatchDeleteCloudDiskSpecs(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数校验失败: " + err.Error(),
		})
		return
	}

	var specs []model.CloudDiskSpec
	if err := model.DB.Where("id IN ?", req.IDs).Find(&specs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询云盘规格失败: " + err.Error(),
		})
		return
	}

	if len(specs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "未找到要删除的云盘规格",
		})
		return
	}

	deletedCount := 0
	for _, spec := range specs {
		if err := model.DB.Delete(&spec).Error; err == nil {
			deletedCount++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "删除成功",
		"data": gin.H{
			"deleted": deletedCount,
		},
	})
}

func validateCloudDiskSpecRequest(c *gin.Context, req *model.CreateCloudDiskSpecRequest) error {
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "规格名称不能为空"})
		return errReturn
	}
	if len(req.Name) < 3 || len(req.Name) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "规格名称长度必须在3-50个字符之间"})
		return errReturn
	}
	if req.CapacityGB <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "容量必须大于0"})
		return errReturn
	}
	if req.IOPSMode != model.IOPSModeTotal && req.IOPSMode != model.IOPSModeReadWrite {
		req.IOPSMode = model.IOPSModeReadWrite
	}
	if req.IOPSMode == model.IOPSModeTotal {
		if req.TotalIOPS < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "总IOPS必须为非负整数"})
			return errReturn
		}
	} else {
		if req.ReadIOPS < 0 || req.WriteIOPS < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读/写IOPS必须为非负整数"})
			return errReturn
		}
	}
	if len(req.Description) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "简介长度不能超过200个字符"})
		return errReturn
	}
	return nil
}

func validateCloudDiskSpecUpdateRequest(c *gin.Context, req *model.UpdateCloudDiskSpecRequest) error {
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "规格名称不能为空"})
		return errReturn
	}
	if len(req.Name) < 3 || len(req.Name) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "规格名称长度必须在3-50个字符之间"})
		return errReturn
	}
	if req.CapacityGB <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "容量必须大于0"})
		return errReturn
	}
	if req.IOPSMode != model.IOPSModeTotal && req.IOPSMode != model.IOPSModeReadWrite {
		req.IOPSMode = model.IOPSModeReadWrite
	}
	if req.IOPSMode == model.IOPSModeTotal {
		if req.TotalIOPS < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "总IOPS必须为非负整数"})
			return errReturn
		}
	} else {
		if req.ReadIOPS < 0 || req.WriteIOPS < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "读/写IOPS必须为非负整数"})
			return errReturn
		}
	}
	if len(req.Description) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "简介长度不能超过200个字符"})
		return errReturn
	}
	return nil
}

func buildCloudDiskSpecFromRequest(req *model.CreateCloudDiskSpecRequest) model.CloudDiskSpec {
	return model.CloudDiskSpec{
		Name:        req.Name,
		CapacityGB:  req.CapacityGB,
		IOPSMode:    req.IOPSMode,
		TotalIOPS:   req.TotalIOPS,
		ReadIOPS:    req.ReadIOPS,
		WriteIOPS:   req.WriteIOPS,
		Description: req.Description,
	}
}

var errReturn = errStop{}

type errStop struct{}

func (errStop) Error() string { return "" }
