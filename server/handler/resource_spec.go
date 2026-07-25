package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kvm_console/model"
)

func ListResourceSpecs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	keyword := c.Query("keyword")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	query := model.DB.Model(&model.ResourceSpec{})

	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询资源规格列表失败: " + err.Error(),
		})
		return
	}

	var specs []model.ResourceSpec
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&specs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询资源规格列表失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "ok",
		"data": model.ResourceSpecListResponse{
			List:     specs,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
	})
}

func CreateResourceSpec(c *gin.Context) {
	var req model.CreateResourceSpecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数校验失败: " + err.Error(),
		})
		return
	}

	if req.CPUCores <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "CPU核心数必须大于0",
		})
		return
	}
	if req.MemoryGB <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "内存必须大于0",
		})
		return
	}

	var existing model.ResourceSpec
	if err := model.DB.Where("name = ?", req.Name).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"message": "规格名称已存在",
		})
		return
	}

	spec := model.ResourceSpec{
		Name:     req.Name,
		CPUCores: req.CPUCores,
		MemoryGB: req.MemoryGB,
	}

	if err := model.DB.Create(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "创建资源规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "创建成功",
		"data":    spec,
	})
}

func UpdateResourceSpec(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "无效的规格 ID",
		})
		return
	}

	var req model.UpdateResourceSpecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "参数校验失败: " + err.Error(),
		})
		return
	}

	if req.CPUCores <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "CPU核心数必须大于0",
		})
		return
	}
	if req.MemoryGB <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "内存必须大于0",
		})
		return
	}

	var spec model.ResourceSpec
	if err := model.DB.First(&spec, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "资源规格不存在",
		})
		return
	}

	var existing model.ResourceSpec
	if err := model.DB.Where("name = ? AND id != ?", req.Name, id).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"code":    409,
			"message": "规格名称已存在",
		})
		return
	}

	spec.Name = req.Name
	spec.CPUCores = req.CPUCores
	spec.MemoryGB = req.MemoryGB

	if err := model.DB.Save(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "更新资源规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "更新成功",
		"data":    spec,
	})
}

func DeleteResourceSpec(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "无效的规格 ID",
		})
		return
	}

	var spec model.ResourceSpec
	if err := model.DB.First(&spec, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "资源规格不存在",
		})
		return
	}

	if err := model.DB.Delete(&spec).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "删除资源规格失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "删除成功",
	})
}

func BatchDeleteResourceSpecs(c *gin.Context) {
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

	var specs []model.ResourceSpec
	if err := model.DB.Where("id IN ?", req.IDs).Find(&specs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "查询资源规格失败: " + err.Error(),
		})
		return
	}

	if len(specs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "未找到要删除的资源规格",
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
