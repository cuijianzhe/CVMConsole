修改内容总结 ：

文件 修改内容 task.go 添加 GORM 标签（主键、索引、字段类型） db.go 在 AutoMigrate 中添加 &Task{} queue.go 重构任务存储逻辑

核心改动 ：

1. 双存储架构 ：任务同时存储在内存缓存和数据库中
   
   - 内存缓存：提高查询性能
   - 数据库：持久化，服务重启后恢复
2. 启动时恢复 ： Start() 函数会从数据库加载所有任务记录
3. ID 序列初始化 ：从数据库最大 ID 开始，避免重复
4. 所有操作同步 ：提交、更新、取消、清理任务都会同步到数据库
数据库表结构 （GORM 自动创建）：

字段 类型 说明 id INTEGER 主键 type VARCHAR(50) 任务类型 status VARCHAR(20) 任务状态 params TEXT 参数（JSON） result TEXT 结果（JSON） progress INT 进度 0-100 message VARCHAR(500) 状态消息 created_by VARCHAR(100) 创建人 created_at DATETIME 创建时间 updated_at DATETIME 更新时间