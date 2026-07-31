# 资源规格 & 云盘规格管理

## 概述

资源管理模块下提供两类规格管理，用于创建虚拟机时快速选择预设配置：

- **资源规格**：预设 CPU 核数和内存容量组合（如 2C4G、4C8G）
- **云盘规格**：预设磁盘类型、容量、格式和 IOPS 限速参数

两类规格的列表接口对所有已认证用户开放（创建虚拟机时选择使用），增删改操作仅管理员可执行。

---

## 资源规格

### 数据结构

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| name | string | 规格名称（唯一，如 "2C4G"） |
| cpu_cores | int | CPU 核数 |
| memory_gb | int | 内存容量（GB） |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

### 接口

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | `/api/resource-specs` | 已认证 | 获取资源规格列表 |
| POST | `/api/resource-specs` | 管理员 | 创建资源规格 |
| PUT | `/api/resource-specs/:id` | 管理员 | 更新资源规格 |
| DELETE | `/api/resource-specs/:id` | 管理员 | 删除资源规格 |
| POST | `/api/resource-specs/batch-delete` | 管理员 | 批量删除资源规格 |

### 创建/更新请求体

```json
{
  "name": "2C4G",
  "cpu_cores": 2,
  "memory_gb": 4
}
```

必填字段：`name`、`cpu_cores`、`memory_gb`

---

## 云盘规格

### 数据结构

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint | 主键 |
| name | string | 规格名称（唯一） |
| disk_type | string | 磁盘类型：`SYSTEM`（系统盘）/ `DATA`（数据盘），默认 `DATA` |
| capacity_gb | int | 容量（GB） |
| storage_location | string | 存储位置（存储池路径） |
| disk_format | string | 磁盘格式：`QCOW2` / `RAW`，默认 `QCOW2` |
| iops_mode | string | IOPS 限速模式：`TOTAL`（总 IOPS）/ `READ_WRITE`（读写分离），默认 `READ_WRITE` |
| total_iops | int | 总 IOPS 限制（`TOTAL` 模式生效，0 = 不限） |
| read_iops | int | 读 IOPS 限制（`READ_WRITE` 模式生效，0 = 不限） |
| write_iops | int | 写 IOPS 限制（`READ_WRITE` 模式生效，0 = 不限） |
| description | string | 描述 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

### 接口

| 方法 | 路径 | 权限 | 说明 |
|------|------|------|------|
| GET | `/api/cloud-disk-specs` | 已认证 | 获取云盘规格列表 |
| POST | `/api/cloud-disk-specs` | 管理员 | 创建云盘规格 |
| PUT | `/api/cloud-disk-specs/:id` | 管理员 | 更新云盘规格 |
| DELETE | `/api/cloud-disk-specs/:id` | 管理员 | 删除云盘规格 |
| POST | `/api/cloud-disk-specs/batch-delete` | 管理员 | 批量删除云盘规格 |

### 创建/更新请求体

```json
{
  "name": "100G系统盘",
  "disk_type": "SYSTEM",
  "capacity_gb": 100,
  "storage_location": "/var/lib/libvirt/images",
  "disk_format": "QCOW2",
  "iops_mode": "READ_WRITE",
  "total_iops": 0,
  "read_iops": 1000,
  "write_iops": 500,
  "description": "100GB 系统盘，读写分离限速"
}
```

必填字段：`name`、`capacity_gb`

### IOPS 限速说明

- `TOTAL` 模式：仅 `total_iops` 生效，限制磁盘总的每秒 I/O 操作数
- `READ_WRITE` 模式：`read_iops` 和 `write_iops` 分别限制读写 IOPS，0 表示不限

---

## 相关文件

| 层级 | 文件 |
|------|------|
| 后端模型 | `server/model/resource_spec.go`、`server/model/cloud_disk_spec.go` |
| 后端 Handler | `server/handler/resource_spec.go`、`server/handler/cloud_disk_spec.go` |
| 后端路由 | `server/router/router.go`（第 417-439 行） |
| 前端 API | `web/src/api/resourceSpec.ts`、`web/src/api/cloudDiskSpec.ts` |
| 前端页面 | `web/src/views/resource-spec/`、`web/src/views/cloud-disk-spec/` |
| API 文档 | `web/src/views/api-docs/endpointDescriptions.ts`（规格管理分组） |
