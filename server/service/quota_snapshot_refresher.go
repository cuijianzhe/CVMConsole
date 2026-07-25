package service

import (
	"time"

	"kvm_console/logger"
	userpkg "kvm_console/service/user"
)

// StartQuotaSnapshotRefresher 启动配额缓存定时刷新任务
// 默认每 5 分钟刷新一次所有用户的配额快照
func StartQuotaSnapshotRefresher() {
	go func() {
		// 首次启动时延迟 30 秒，等待其他服务就绪
		time.Sleep(30 * time.Second)

		logger.App.Info("启动配额缓存定时刷新服务")

		// 启动后立即执行一次刷新
		if err := userpkg.RefreshAllUserQuotaSnapshots(); err != nil {
			logger.App.Warn("首次刷新配额缓存失败", "error", err)
		}

		// 定时刷新
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			if err := userpkg.RefreshAllUserQuotaSnapshots(); err != nil {
				logger.App.Warn("定时刷新配额缓存失败", "error", err)
			}
		}
	}()
}

// RefreshQuotaSnapshotsNow 立即刷新所有用户配额快照（供 API 调用）
func RefreshQuotaSnapshotsNow() error {
	return userpkg.RefreshAllUserQuotaSnapshots()
}
