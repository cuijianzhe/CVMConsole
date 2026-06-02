# 虚拟机跨节点迁移

## 功能概述

跨节点迁移由源节点面板发起，目标节点面板接管。迁移完成后源节点 VM、磁盘、用户授权和网络绑定都会保留，目标节点会补齐用户、VM 授权、VPC/轻量云 VPC、端口转发和 VM 登录凭据。

当前版本仅管理员可发起迁移，迁移任务类型为 `vm_migrate`。

如果只需要把某台 VM 的指定硬盘迁移到本机其它存储池，请使用迁移弹窗中的「迁移硬盘」。该能力不会跨节点，任务类型为 `vm_disk_migrate`，详细说明见 `docs/vm-disk-migration.md`。

## 节点管理

管理员在侧边栏进入「节点管理」，添加目标节点：

- 节点名称
- 目标面板 API 地址
- 目标面板管理员 API ID / API Key
- SSH 地址、端口、root 密码

root 密码和 API Key 会加密保存，接口不会回显明文。节点探测会检查面板 API、SSH、`virsh`、`qemu-img`、`rsync`、镜像目录、模板目录和 OVS 网桥。

## 迁移规则

- 源 VM 已关机时自动按冷迁移执行；源 VM 正在运行时自动按热迁移执行，前端不再手动选择冷热迁移模式。
- 运行中 VM 热迁移预检会先做线路测速：源节点临时提供 100MB 文件，目标节点下载后计算平均带宽。
- 热迁移会使用 `virsh domdirtyrate-calc` / `virsh domstats --dirtyrate` 读取脏页速率，并尽量读取 `kvm_stat` 的 `kvm_page_fault` 作为辅助指标。
- 脏页速率低于平均带宽 20% 时允许热迁移；达到 20% 但低于 50% 时允许迁移但强制按表单值限制 VM CPU 使用率（默认 50%）；达到或超过 50% 时阻止热迁移并返回测速和脏页数据。
- VM 迁移任务等待或运行期间，列表和详情状态显示为“迁移中”，开关机、重启、重置、删除、快照、磁盘、救援、密码重置等用户侧操作会被阻止。
- 管理员打开迁移弹窗时只加载目标节点的存储、VPC 和安全组选项，不会立即执行磁盘链 hash 预检。
- 管理员补全目标节点、目标存储、目标 VPC/轻量云 VPC 和安全组后，可以手动点击“执行预检”，也可以直接提交迁移任务。
- 预检通过后后端返回 `preview_id` 并缓存预检结果；提交时携带 `preview_id` 会复用预检结果，不再重复计算 backing hash。
- 未携带 `preview_id` 直接提交时，任务开始后会自动生成迁移执行计划。
- 勾选“跳过完整预检”时，任务不会提前计算 backing hash；仍会检查目标存储、VM 同名、网络选择等必要执行条件。
- 如果提交前 VM 状态、目标节点、目标存储、目标 VPC 或安全组发生变化，已缓存预检会失效，需要重新预检或直接提交由任务重新生成计划。
- 链式克隆磁盘不会转换为独立 qcow2。
- 默认预检要求每个 backing 文件在目标节点同路径存在，并且 format、virtual size、sha256 完全一致；跳过完整预检时不计算 sha256，只做轻量执行检查。
- 任一 backing 校验失败时直接阻止迁移。
- overlay 磁盘会复制或迁移到管理员选择的目标存储 VM 目录。
- 多硬盘 VM 可为每块硬盘单独选择目标节点存储位置；未单独指定的硬盘会使用表单中的默认目标存储。
- 热迁移使用 `--copy-storage-inc` 前，会在目标节点按源 overlay 元数据预创建目标 overlay，避免 libvirt 自动预创建增量迁移目标盘失败。
- 热迁移命令会显式使用节点配置中的 SSH 地址作为 `--migrateuri` 和 `--disks-uri` 主机，避免目标节点 hostname 无法被源节点解析导致 NBD 连接失败。
- 热迁移接管目标 VPC 时会修正运行态 OVS 网卡 VLAN，并在 VLAN 不一致时热重插同 MAC 网卡，触发 guest 重新 DHCP；冷迁移 VM 未运行，只需要重写持久 XML。
- 热迁移失败时，只会清理本次迁移任务刚创建的目标 overlay；源 VM 和源磁盘仍保留。
- 端口转发优先迁移相同 host port；目标端口占用时自动分配可用端口。
- VM 凭据会读取源面板明文解密结果，再通过目标面板重新加密保存。
- 定时任务、历史统计、流量配额不迁移。
- UEFI 虚拟机迁移到目标节点后，接管流程会自动检测引导方式，若为 UEFI 模式则自动创建 qcow2 格式的 NVRAM 变量文件（从 `/usr/share/OVMF/OVMF_VARS_4M.fd` 模板转换）。这是因为部分 QEMU/libvirt 版本不支持在启动时自动将 raw 模板转换为 qcow2 格式的 NVRAM。
- 热迁移时会在执行 `virsh migrate` 前，通过 SSH 在目标节点预创建 qcow2 格式的 NVRAM 文件，并将迁移 XML 中的 `template`/`templateFormat` 属性移除，避免 libvirt 在目标端再次尝试转换。热迁移失败时 NVRAM 文件会同磁盘文件一起被清理。

## 用户和网络接管

- 目标节点已有同名用户时，直接绑定迁移后的 VM，并同步源用户的面板登录密码哈希、邮箱和邮箱验证状态。
- 目标节点没有同名用户时，自动创建同名普通用户，并同步源用户的面板登录密码哈希、邮箱和邮箱验证状态。
- 系统用户密码无法从源节点反推明文，目标节点会为系统用户生成随机密码；这不影响面板网页登录。
- 源用户是轻量云类型时，目标自动创建的用户也保持轻量云类型。
- 目标节点没有同名用户时，迁移表单不展示 VPC/安全组选择；目标面板会先创建用户，再使用该用户默认 VPC/安全组绑定迁移后的 VM。
- 目标节点已有同名用户时，迁移表单只展示该用户下的 VPC 和安全组，管理员需要从该用户资源中选择。
- 轻量云 VM 若目标是新用户，会自动为新轻量云用户创建默认专用 VPC；若目标已有用户，则必须选择该用户下的轻量云 VPC。

## API

- `GET /api/nodes`：节点列表。
- `POST /api/nodes`：添加节点。
- `PUT /api/nodes/:id`：更新节点。
- `DELETE /api/nodes/:id`：删除节点。
- `POST /api/nodes/:id/probe`：探测节点。
- `GET /api/nodes/:id/migration-options?vm_name=<name>`：加载迁移表单选项，返回自动迁移模式、目标存储、目标网络和目标用户处理方式。
- `POST /api/vm/:name/migration/preview`：迁移预检。
- `POST /api/vm/:name/migrate`：提交迁移任务。
- `POST /api/migration/adopt-vm`：目标面板接管迁移 VM，供源节点任务调用。

预检请求体示例：

```json
{
  "node_id": 1,
  "target_storage_pool_id": "sda",
  "disk_storage_targets": [
    {"target": "vda", "target_storage_pool_id": "sda"},
    {"target": "vdb", "target_storage_pool_id": "sdb1"}
  ],
  "target_switch_id": 2,
  "target_security_group_id": 4,
  "enable_cpu_throttle": false,
  "cpu_throttle_percent": 50
}
```

提交迁移请求体示例：

```json
{
  "node_id": 1,
  "preview_id": "mig_xxx",
  "skip_precheck": false,
  "target_storage_pool_id": "sda",
  "disk_storage_targets": [
    {"target": "vda", "target_storage_pool_id": "sda"},
    {"target": "vdb", "target_storage_pool_id": "sdb1"}
  ],
  "target_switch_id": 2,
  "target_security_group_id": 4,
  "enable_cpu_throttle": false,
  "cpu_throttle_percent": 50
}
```

管理员 API Key 请求高风险接口时不会触发二级验证；普通用户 API Key 和浏览器 Session 仍按原规则处理。
