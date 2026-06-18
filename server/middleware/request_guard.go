package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kvm_console/config"
	"kvm_console/logger"

	"github.com/gin-gonic/gin"
)

var largeBodyPaths = []string{
	"/api/template/import",
	"/api/vm/export",
	"/api/vm/import",
}

func RequestGuardMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// --- Body size limit ---
		maxBodyMB := config.GlobalConfig.APIMaxBodySizeMB
		if maxBodyMB <= 0 {
			maxBodyMB = 2
		}
		maxBodyBytes := int64(maxBodyMB) * 1024 * 1024

		isLargePath := false
		for _, p := range largeBodyPaths {
			if c.Request.URL.Path == p {
				isLargePath = true
				break
			}
		}

		if !isLargePath {
			contentLength := c.Request.Header.Get("Content-Length")
			if contentLength != "" {
				if cl, err := strconv.ParseInt(contentLength, 10, 64); err == nil && cl > maxBodyBytes {
					c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
						"code":    413,
						"message": "Request Entity Too Large",
					})
					return
				}
			} else {
				// No Content-Length header, wrap body with MaxBytesReader
				c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
			}
		}

		// --- Content-Type enforcement ---
		method := c.Request.Method
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
			ct := c.Request.Header.Get("Content-Type")
			if ct == "" {
				// 无 Content-Type 的 POST 请求：若请求体为空则放行（如无参 POST 触发检查/修复等操作）
				cl := c.Request.Header.Get("Content-Length")
				if cl == "" || cl == "0" {
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
					"code":    415,
					"message": "Unsupported Media Type",
				})
				return
			}

			allowed := false
			allowedPrefixes := []string{
				"application/json",
				"multipart/form-data",
				"application/x-www-form-urlencoded",
			}
			for _, prefix := range allowedPrefixes {
				if strings.HasPrefix(ct, prefix) {
					allowed = true
					break
				}
			}

			if !allowed {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{
					"code":    415,
					"message": "Unsupported Media Type",
				})
				return
			}
		}

		c.Next()
	}
}

func SafeRecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.App.Error("请求 panic",
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
					"error", fmt.Sprintf("%v", err),
				)

				if config.GlobalConfig.ErrorDetailInResponse {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"code":    500,
						"message": fmt.Sprintf("Internal Server Error: %v", err),
					})
				} else {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"code":    500,
						"message": "Internal Server Error",
					})
				}
			}
		}()
		c.Next()
	}
}
