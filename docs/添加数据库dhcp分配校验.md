## 实现总结
### 1. 数据库模型 - network_bridge.go
新增 BridgeStaticHostDB 模型：

### 2. 数据库迁移 - db.go
在 AutoMigrate 中添加 &BridgeStaticHostDB{}

### 3. 核心逻辑 - deps.go
并发安全 ：

- dhcpHostsMutex sync.Mutex 保护并发读写
- 所有写操作（Upsert/Remove）都先获取锁
唯一性校验 ：

- validateStaticHostUniqueness() 检查 IP 和 MAC 是否在同一网桥内重复
- 在写入数据库前先校验，避免冲突
数据库主存 ：

- ListBridgeStaticHosts() 从数据库读取
- UpsertBridgeStaticHost() 写入数据库 + 同步文件 + reload dnsmasq
- RemoveBridgeStaticHost() 从数据库删除 + 同步文件 + reload dnsmasq
向后兼容 ：

- MigrateStaticHostsFromFile() 首次启动时从文件迁移到数据库
- 降级模式：数据库不可用时回退到文件操作
辅助函数 ：

- syncBridgeStaticHostsToFile() 从数据库同步到 dhcp-hosts 文件
- EnsureBridgeStaticHostsFile() dnsmasq 启动前同步
- GenerateUniqueIP() 生成不冲突的唯一 IP
- SyncAllBridgeStaticHosts() 启动时批量同步
### 4. dnsmasq 集成 - create.go
- startBridgeDNSMasq() 启动前调用 EnsureBridgeStaticHostsFile()
- reloadBridgeDNSMasq() reload 前同步文件
## 解决的问题
问题 解决方案 并发写入安全性 Go mutex 保证进程内并发安全 唯一性约束 应用层校验 + 数据库唯一索引 查询能力 SQL 支持任意条件查询（IP→VM 反查、统计等） 审计追踪 CreatedAt/UpdatedAt 时间戳 + 操作日志 崩溃安全 数据库事务保证原子性

## 向后兼容
- 首次启动时自动从现有 dhcp-hosts-* 文件迁移到数据库
- 数据库不可用时自动降级到文件模式
- 保留所有原有函数签名，不影响其他模块