# go-libvirt 渐进迁移方案

## 目标

将高频 libvirt 操作从 `virsh` 命令行调用迁移至 DigitalOcean 的 [go-libvirt](https://github.com/digitalocean/go-libvirt) 纯 Go RPC 库，低频操作保留 `virsh` 命令不变。两者混用，逐步过渡。所有 RPC 调用保留 virsh fallback 路径。

> **当前版本**: `v0.0.0-20260217163227-273eaa321819`（2026-02-17，与 libvirt 10.x+ 协议兼容）
> **启动策略**: 程序启动时强制校验 go-libvirt 连接，连接失败则拒绝启动。

## 为什么迁移

| 对比维度 | virsh 方案 | go-libvirt 方案 |
|---------|-----------|-----------------|
| 执行方式 | 每次调用 fork 子进程 | 复用一条 Unix socket RPC 连接 |
| 返回数据 | 字符串解析（正则/awk/grep） | Go 原生类型（struct/int/string） |
| 100 台 VM 列表查询 | ~400 次进程 fork | 2-3 次 RPC 调用 |
| 错误处理 | 解析 stderr 文本 | Go error 类型 |
| 事件通知 | 轮询 `virsh domstate` | 原生 `DomainEventLifecycle` 订阅 |
| CGO 依赖 | 无 | 无（同样纯 Go） |
| 跨平台编译 | 简单 | 同样简单 |

## 当前迁移状态

```
基础设施:        ✅✅✅✅✅ 已完成    连接管理 + 18 个 Domain RPC 封装函数
只读查询:        ✅✅✅✅⬜ 基本完成  ListVMs / GetVM / GetVMXML / 资源采集器均优先走 RPC
控制操作:        ✅✅⬜⬜⬜ 部分完成  Start / Shutdown / Destroy / Resume 已走 RPC，其余待迁移
CPU/内存管理:    ⬜⬜⬜⬜⬜ 未开始    setvcpus / setmem / metadata 仍用 virsh
快照/磁盘:       ⬜⬜⬜⬜⬜ 未开始    全部仍用 virsh，需新增 RPC 封装
事件订阅:        ⬜⬜⬜⬜⬜ 未开始    可选增强，非必须
```

### 已完成的文件

| 文件 | 状态 | 说明 |
|------|------|------|
| `server/service/libvirt_rpc.go` | ✅ | 连接管理（单例、自动重连、连接存活检测） |
| `server/service/libvirt_rpc_domain.go` | ✅ | 18 个 Domain RPC 封装（列表/状态/信息/XML/统计/启停/CPU/内存/自动启动） |
| `server/service/stats_collector.go` | ✅ | 资源采集器优先走 RPC（getRunningVMNamesRPC / collectVMStatsRPC） |
| `server/service/libvirt.go` | ⚠️ | ListVMs / GetVM / StartVM 部分走 RPC，其余仍用 virsh |
| `server/config/config.go` | ✅ | `use_go_libvirt` 配置开关 |
| `server/main.go` | ✅ | 启动时 InitLibvirtRPC() 强制校验 |
| `server/logger/logger.go` | ✅ | libvirt 专用日志通道 `logger.Libvirt` |

### 待迁移的文件（按优先级排序）

| 优先级 | 文件 | virsh 调用数 | 涉及操作 |
|--------|------|------------|---------|
| 🔴 P0 | `vm_cpu_topology.go` | 8 | `virsh dumpxml`, `setvcpus`, `define`, `domstate` |
| 🔴 P0 | `vm_dynamic_memory.go` | 12 | `virsh setmem`, `metadata`, `dommemstat`, `dumpxml`, `define` |
| 🟠 P1 | `snapshot.go` | 15 | `virsh snapshot-*`, `domstate`, `suspend`, `resume` |
| 🟠 P1 | `disk.go` | 15 | `virsh domblklist`, `domblkinfo`, `dumpxml`, `attach-device` |
| 🟡 P2 | `vnc.go` | 15 | `virsh dumpxml`, `qemu-monitor-command`, `destroy`, `define` |
| 🟡 P2 | `network.go` | 12 | `virsh domiflist`, `detach-device`, `attach-device` |
| 🟢 P3 | `vm_monitor.go` | 3 | `virsh qemu-monitor-command --hmp` |
| 🟢 P3 | `bandwidth.go` | 3 | `virsh domiflist`, `domiftune` |
| 🟢 P3 | `clone.go` | 8 | `virsh destroy`, `undefine`, `dominfo`, `autostart` |
| 🟢 P3 | `rescue.go` | 6 | `virsh domstate`, `dumpxml`, `define` |
| 🟢 P3 | `template.go` | 8 | `virsh dumpxml`, `dominfo`, `domstate` |
| 🟢 P3 | `linked_clone.go` | 6 | `virsh dominfo`, `define`, `destroy`, `undefine` |
| ⚫ 保留 | `vm_migration.go` | 15 | 跨主机 SSH + `virsh migrate`，链路过于复杂 |
| ⚫ 保留 | — | — | `qemu-img` / `iptables` / `tc` / `ovs-vsctl` / `virt-install` / `tcpdump` / `systemctl` / `stat` / `cp` / `chown` 等非 libvirt API 范畴 |

## 操作频率分级（含迁移状态）

### 高频操作 ✅ 基本完成

| # | 操作 | go-libvirt API | 状态 | 调用场景 |
|---|------|---------------|------|---------|
| 1 | 列出所有 VM | `ConnectListAllDomains()` | ✅ 已迁移 | VM 列表页、缓存刷新 |
| 2 | 列出运行中 VM | `ConnectListAllDomains(flags)` | ✅ 已迁移 | 资源采集器 |
| 3 | 获取 VM 状态 | `DomainGetInfo()` → `State` | ✅ 已迁移 | 列表/详情页 |
| 4 | 获取 VM 基本信息 | `DomainGetInfo()` | ✅ 已迁移 | CPU/内存/状态 |
| 5 | 获取 VM XML | `DomainGetXMLDesc()` | ✅ 已迁移 | 配置解析 |
| 6 | CPU 统计 | `DomainGetInfo()` → cpuTime | ✅ 已迁移 | 资源采集器 |
| 7 | 内存统计 | `DomainMemoryStats()` | ✅ 已迁移 | 资源采集器 |
| 8 | 磁盘 I/O | `DomainBlockStats()` | ✅ 已迁移 | 资源采集器 |
| 9 | 网络 I/O | `DomainInterfaceStats()` | ✅ 已迁移 | 资源采集器 |
| 10 | VNC 端口 | `DomainGetXMLDesc()` → parse | ⚠️ 部分（详情页走 RPC，列表页仍用 virsh） | 详情页 |
| 11 | 磁盘列表 | `DomainGetXMLDesc()` → parse | ⚠️ 部分（详情页走 RPC，disk.go 仍用 virsh） | 详情页 |
| 12 | 网卡列表 | `DomainGetXMLDesc()` → parse | ⚠️ 部分（详情页走 RPC，network.go 仍用 virsh） | 详情页 |

### 中频操作 ⚠️ 部分完成

| # | 操作 | go-libvirt API | 状态 |
|---|------|---------------|------|
| 13 | 启动 VM | `DomainCreate()` | ✅ 已迁移（含 fallback） |
| 14 | 启动 VM（暂停） | `DomainCreateWithFlags()` | ✅ 已迁移（含 fallback） |
| 15 | 正常关机 | `DomainShutdown()` | ✅ 已迁移 |
| 16 | 强制断电 | `DomainDestroy()` | ✅ 已迁移 |
| 17 | 重启 | `DomainReboot()` | ✅ RPC 封装就绪，`libvirt.go` 中待接入 |
| 18 | 硬重置 | `DomainReset()` | ✅ RPC 封装就绪，`libvirt.go` 中待接入 |
| 19 | 开机自启 | `DomainSetAutostart()` | ✅ 已迁移 |
| 20 | 暂停/恢复 | `DomainSuspend()` / `DomainResume()` | ⚠️ 恢复已迁移，暂停待接入 |
| 21 | 持久化配置 | `DomainDefineXML()` | ✅ RPC 封装就绪，多处仍用 virsh |
| 22 | 设置 CPU | `DomainSetVcpusFlags()` | ❌ RPC 封装就绪，`vm_cpu_topology.go` 待迁移 |
| 23 | 设置内存 | `DomainSetMemoryFlags()` | ❌ RPC 封装就绪，`vm_dynamic_memory.go` 待迁移 |

### 低频操作 — 保留 virsh

| 操作类别 | 说明 | 原因 |
|---------|------|------|
| 虚拟机迁移 | migrate + SSH 远程执行 | 跨主机 SSH、证书、存储迁移链路极其复杂 |
| 虚拟机创建 | virt-install | 非 libvirt API 范畴 |
| 模板操作 | qemu-img + virt-sysprep | 非 libvirt API 范畴 |
| 网络/防火墙 | brctl/ovs-vsctl/iptables/nft | 非 libvirt API 范畴 |
| 带宽限速 | tc/ovs QoS | 非 libvirt API 范畴 |
| 光盘热插拔 | change-media | 操作简单，频率极低 |

### 永远不迁移的操作（非 libvirt API 范畴）

这些命令由其他系统工具提供，永远不可能用 go-libvirt 替代：

| 类目 | 涉及文件 | 命令 |
|------|---------|------|
| 磁盘映像操作 | `template.go`, `clone.go`, `linked_clone.go`, `vm_create.go`, `vm_export.go`, `disk.go` | `qemu-img create/convert/info/resize` |
| 虚拟机安装 | `vm_create.go` | `virt-install` |
| 系统管理 | `host_zram.go`, `libvirt.go`, `ovs_network.go` | `systemctl`, `modprobe`, `zramctl`, `mkswap`, `swapon` |
| 网络层 | `bandwidth.go` | `tc qdisc/class` |
| 防火墙(IPv4) | `public_ip.go`, `network.go` | `iptables` |
| 防火墙(IPv6/nft) | `firewall.go`, `host_firewall.go` | `nft`, `ufw` |
| OVS | `ovs_network.go`, `bandwidth.go` | `ovs-vsctl`, `ip link/addr/route` |
| 文件系统 | 多处 | `stat`, `cp`, `mkdir`, `chown`, `rm`, `cat`, `test` |
| 网络诊断 | `network_diagnostics.go` | `tcpdump` |
| 系统信息采集 | `libvirt.go` | `nproc`, `free`, `df`, `iostat`, `top`, `/proc/meminfo` |
| SSH 远程执行 | `vm_migration.go` | `ssh` (migration / remote dominfo 等) |

---

## 架构设计（已实现）

### 1. 连接管理层

文件: `server/service/libvirt_rpc.go`

```
InitLibvirtRPC()         → 启动时调用，连接失败阻止启动
GetLibvirt()             → 获取连接（自动重连，读锁快速路径）
IsLibvirtRPCAvailable()  → O(1) 内存检测，不触发网络 I/O
CloseLibvirt()           → 程序退出时调用
reconnectLibvirt()       → 重连逻辑（3 次退避: 1s / 2s / 4s）
dialLibvirt()            → Unix socket Dial + RPC 握手
```

连接生命周期:

```
程序启动 → InitLibvirtRPC() 强制连接
         → 失败 → log.Fatal 拒绝启动
         → 成功 → 后续所有调用复用同一连接
         
程序运行 → GetLibvirt() 读写锁快速检测连接存活
         → 断开 → 自动重连（3 次退避）
         → 重连失败 → 返回 error，调用方走 virsh fallback
         
程序退出 → CloseLibvirt()
```

### 2. Domain RPC 封装（已实现）

文件: `server/service/libvirt_rpc_domain.go`

已封装的 18 个函数:

```
━━━ 高频只读 ━━━
listAllDomainsRPC()           → ConnectListAllDomains()
getDomainStateRPC(name)       → DomainGetInfo().State
getDomainInfoRPC(name)        → DomainGetInfo() + DomainGetAutostart()
getDomainXMLRPC(name, flags)  → DomainGetXMLDesc()
getDomainCPUStatsRPC(name)    → DomainGetInfo().cpuTime
getDomainMemoryStatsRPC(name) → DomainMemoryStats()
getDomainBlockStatsRPC(n, dev)→ DomainBlockStats()
getDomainInterfaceStatsRPC()  → DomainInterfaceStats()

━━━ 中频控制 ━━━
startDomainRPC(name)          → DomainCreate()
startDomainPausedRPC(name)    → DomainCreateWithFlags()
shutdownDomainRPC(name)       → DomainShutdown()
destroyDomainRPC(name)        → DomainDestroy()
rebootDomainRPC(name)         → DomainReboot()
resetDomainRPC(name)          → DomainReset()
suspendDomainRPC(name)        → DomainSuspend()
resumeDomainRPC(name)         → DomainResume()
setDomainAutostartRPC(name,b) → DomainSetAutostart()
defineDomainXMLRPC(xml)       → DomainDefineXML()
setDomainVcpusFlagsRPC(n,c,f) → DomainSetVcpusFlags()
setDomainMemoryFlagsRPC(n,m,f)→ DomainSetMemoryFlags()
getDomainVcpuCountRPC(n,f)    → DomainGetVcpusFlags()
```

### 3. 现有函数改造模式

所有函数保持 go-libvirt 优先 → virsh fallback 模式：

```
go-libvirt 可用?
  ├─ 是 → 调用 RPC 函数
  │       ├─ 成功 → 返回结果
  │       └─ 失败 → 记录日志 → 降级为 virsh → 返回结果
  └─ 否 → 直接调用 virsh → 返回结果
```

示例（来自 `libvirt.go` ListVMs）:

```go
if IsLibvirtRPCAvailable() {
    if domains, err := listAllDomainsRPC(); err == nil {
        for _, dom := range domains {
            names = append(names, dom.Name)
        }
    } else {
        logger.Libvirt.Warn("listAllDomainsRPC 失败，降级为 virsh", "error", err)
    }
}
if len(names) == 0 {
    result := utils.ExecCommand("virsh", "list", "--all", "--name")
    // ...
}
```

---

## 剩余迁移计划

### Phase 0: 新增 RPC 封装（⌛ 1-2 天）

在 `libvirt_rpc_domain.go` 中补充以下函数（当前缺失）:

| 函数 | go-libvirt API | 替代命令 |
|------|---------------|---------|
| `qemuMonitorCommandRPC(name, cmd)` | `QemuMonitorCommand()` | `virsh qemu-monitor-command --hmp` |
| `getDomainMetadataRPC(name, uri)` | `DomainGetMetadata()` | `virsh metadata` |
| `setDomainMetadataRPC(name, uri, val)` | `DomainSetMetadata()` | `virsh metadata --set` |
| `domainSnapshotListNamesRPC(name)` | `DomainSnapshotListNames()` | `virsh snapshot-list --name` |
| `domainSnapshotGetXMLDescRPC(name, snap)` | `DomainSnapshotGetXMLDesc()` | `virsh snapshot-dumpxml` |
| `domainSnapshotCreateXMLRPC(name, xml)` | `DomainSnapshotCreateXML()` | `virsh snapshot-create` |
| `domainRevertToSnapshotRPC(name, snap)` | `DomainRevertToSnapshot()` | `virsh snapshot-revert` |
| `domainSnapshotDeleteRPC(name, snap)` | `DomainSnapshotDelete()` | `virsh snapshot-delete` |
| `domainSnapshotCurrentRPC(name)` | `DomainSnapshotCurrent()` | `virsh snapshot-current --name` |
| `domainSnapshotLookupByNameRPC(name, snap)` | `DomainSnapshotLookupByName()` | （辅助函数） |
| `getBlockInfoRPC(name, dev)` | `DomainGetBlockInfo()` | `virsh domblkinfo` |
| `attachDeviceRPC(name, xml)` | `DomainAttachDeviceFlags()` | `virsh attach-device` |
| `detachDeviceRPC(name, xml)` | `DomainDetachDeviceFlags()` | `virsh detach-device` |
| `blockCopyRPC(name, dev, xml, params)` | `DomainBlockCopy()` | `virsh blockcopy` |
| `blockPullRPC(name, dev, ...)` | `DomainBlockPull()` | `virsh blockpull` |

> **注意**: `QemuMonitorCommand` 是 go-libvirt v0.0.0-2025+ 的新增 API，当前版本已支持。
> `DomainGetMetadata` / `DomainSetMetadata` 均已支持。
> `DomainInterfaceAddresses()` 已支持，可用于替代 `virsh domifaddr`。

### Phase 1: CPU 拓扑 & 动态内存（⌛ 3-5 天）🔴 P0

**文件**: `vm_cpu_topology.go` + `vm_dynamic_memory.go`

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `vm_cpu_topology.go` | `virsh dumpxml` → `getDomainXMLRPC()` |
| 2 | `vm_cpu_topology.go` | `virsh setvcpus` → `setDomainVcpusFlagsRPC()`（兼容 topology 校验分支） |
| 3 | `vm_cpu_topology.go` | `virsh define` → `defineDomainXMLRPC()` |
| 4 | `vm_cpu_topology.go` | `virsh domstate` → `getDomainStateRPC()` |
| 5 | `vm_dynamic_memory.go` | `virsh setmem` → `setDomainMemoryFlagsRPC()` |
| 6 | `vm_dynamic_memory.go` | `virsh metadata` → `getDomainMetadataRPC()` / `setDomainMetadataRPC()` |
| 7 | `vm_dynamic_memory.go` | `virsh dommemstat` → `getDomainMemoryStatsRPC()` |
| 8 | `vm_dynamic_memory.go` | `virsh dumpxml` / `virsh define` → RPC 路径 |

关键风险点:
- CPU topology 校验：`virsh setvcpus --maximum` 与 `virsh define` 对 sockets×dies×cores×threads == vcpu 的校验规则不同，RPC 路径需同样处理
- `virsh metadata` 的 URI 格式在 RPC 中可能有差异，需要逐一验证

### Phase 2: 快照 & 磁盘（⌛ 3-4 天）🟠 P1

**文件**: `snapshot.go` + `disk.go`

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `snapshot.go` | 引入 Phase 0 新增的 snapshot RPC 封装 |
| 2 | `snapshot.go` | `virsh snapshot-list/current/dumpxml` → RPC |
| 3 | `snapshot.go` | `virsh snapshot-create/revert/delete` → RPC（保留 virsh fallback） |
| 4 | `disk.go` | `virsh domblklist` → `DomainGetXMLDesc()` 解析（已有解析逻辑） |
| 5 | `disk.go` | `virsh domblkinfo` → `getBlockInfoRPC()` |
| 6 | `disk.go` | `virsh attach-device/detach-device` → `attachDeviceRPC()` / `detachDeviceRPC()` |

关键风险点:
- snapshot.go 中有多个 virsh 命令链式调用（snapshot-create 后 resume），RPC 路径需保持等价
- 外部快照的 `--disk-only` / `--reuse-external` 等复杂参数，go-libvirt 需传完整 XML

### Phase 3: VNC / 网络 / 监控（⌛ 3-4 天）🟡 P2

**文件**: `vnc.go` + `network.go` + `vm_monitor.go` + `bandwidth.go`

| 步骤 | 文件 | 改动 |
|------|------|------|
| 1 | `vnc.go` | `virsh dumpxml` → `getDomainXMLRPC()` |
| 2 | `vnc.go` | `virsh qemu-monitor-command --hmp "info vnc"` → `qemuMonitorCommandRPC()` |
| 3 | `vnc.go` | `virsh destroy` / `virsh define` → RPC 路径 |
| 4 | `network.go` | `virsh domiflist` → `DomainGetXMLDesc()` 解析 |
| 5 | `network.go` | `virsh detach-device/attach-device` → RPC |
| 6 | `vm_monitor.go` | `virsh qemu-monitor-command --hmp` → `qemuMonitorCommandRPC()` |
| 7 | `bandwidth.go` | `virsh domiflist` → `DomainGetXMLDesc()` 解析 |
| 8 | `bandwidth.go` | `virsh domiftune` → `DomainInterfaceParameters()` 相关（需确认 API 可用性） |

### Phase 4: 低频模块收尾（⌛ 2-3 天）🟢 P3

**文件**: `clone.go` + `rescue.go` + `template.go` + `linked_clone.go` + libvirt.go 剩余 fallbacks

这些模块中的 virsh 调用大多可被已有 RPC 封装直接替换:
- `virsh destroy` → `destroyDomainRPC()`
- `virsh undefine` → `DomainUndefine()` — 需新增，但注意 `--nvram` / `--snapshots-metadata` flag
- `virsh dominfo` → `getDomainInfoRPC()`
- `virsh autostart` → `setDomainAutostartRPC()`
- `virsh define` → `defineDomainXMLRPC()`
- `virsh domstate` / `virsh dumpxml` → 已有 RPC

### Phase 5: 事件订阅（可选，⌛ 2-3 天）

使用 `ConnectDomainEventCallbackRegister()` 订阅虚拟机生命周期事件，替代轮询:

```go
// 概念代码
l.ConnectDomainEventCallbackRegister(
    -1, // 所有域
    libvirt.ConnectDomainEventCallbackRegisterParams{
        EventID: libvirt.DomainEventIDLifecycle,
        Cb: func(callback *libvirt.DomainEventCallbackLifecycleMsg) {
            // 实时更新 VM 运行状态缓存
            name := callback.Dom.Name
            state := domainStateToString(callback.Event)
            UpdateVMRuntimeState(name, state, time.Now())
        },
    },
)
```

无需 10 秒轮询 `virsh domstate`，状态更新延迟从秒级降至毫秒级。

---

## 总工作量预估

| 阶段 | 内容 | 预估人天 | 风险 |
|------|------|---------|------|
| Phase 0 | 新增 RPC 封装（14 个函数） | 1-2 | 🟢 低 |
| Phase 1 | CPU 拓扑 + 动态内存 | 3-5 | 🟡 中（topology 校验一致性） |
| Phase 2 | 快照 + 磁盘 | 3-4 | 🟡 中（外部快照复杂参数） |
| Phase 3 | VNC + 网络 + 监控 | 3-4 | 🟢 低 |
| Phase 4 | 低频模块收尾 | 2-3 | 🟢 低 |
| Phase 5 | 事件订阅（可选） | 2-3 | 🟢 低 |
| **合计 (Phase 0-4)** | | **12-18 人天** | |
| **合计 (含 Phase 5)** | | **14-21 人天** | |

> 预估前提：一次只改一个模块文件，改完测试验证，不留遗留问题。

---

## 注意事项

### 1. go-libvirt API 更新说明

相对于迁移文档初版时，go-libvirt 现已支持以下之前标注为"不支持"的 API:

| API | 状态 | 说明 |
|-----|------|------|
| `QemuMonitorCommand()` | ✅ 已支持 | 直接发送 QMP/HMP 命令，替代 `virsh qemu-monitor-command` |
| `DomainGetMetadata()` / `DomainSetMetadata()` | ✅ 已支持 | 替代 `virsh metadata` |
| `DomainInterfaceAddresses()` | ✅ 已支持 | 替代 `virsh domifaddr`，但多层 fallback (agent/arp/lease) 仍需保留 |

### 2. XML 解析

- go-libvirt 的 `DomainGetXMLDesc()` 返回的 XML **完全等价**于 `virsh dumpxml`
- 现有的 `ParseVMBootTypeFromDomainXML()` 等解析函数无需修改
- 磁盘列表、网卡列表优先从 XML 解析（已有成熟解析代码），不再额外调用 `virsh domblklist/domiflist`

### 3. 错误处理与降级

每个使用 go-libvirt 的函数必须保留 virsh fallback:

```
go-libvirt 调用成功 → 返回结果
go-libvirt 调用失败 → 记录日志 → 降级为 virsh → 返回结果
virsh 也失败 → 返回错误
```

### 4. 连接断开恢复

- 每次调用前通过 `GetLibvirt()` 检查连接状态
- 断开时自动重连（最多重试 3 次，间隔 1s/2s/4s）
- 重连失败时触发 fallback

### 5. CPU 拓扑校验兼容

`virsh setvcpus --config --maximum` 在 domain XML 存在 `<cpu><topology>` 时会校验 `sockets×dies×cores×threads == vcpu`，校验失败会报错。`virsh define` 同样校验。RPC 路径的 `DomainSetVcpusFlags` 和 `DomainDefineXML` 需验证是否有相同校验行为。

处理策略:
- 存在 topology 时：修改 vcpu + topology → `DomainDefineXML()`
- 不存在 topology 时：`DomainSetVcpusFlags()`

### 6. 快照操作注意事项

- go-libvirt 的 `DomainSnapshotCreateXML()` 需要传入完整 XML，行为与 `virsh snapshot-create --xmlfile` 类似
- 外部快照 (`--disk-only` / `--reuse-external`) 需要构建正确的 snapshot XML
- 快照链操作（revert/delete 影响 backing chain）需保持与 virsh 行为一致
- 现有多处快照操作是 virsh 命令链（create + resume），迁移时保持等价

### 7. 依赖说明

- `libvirt-client` 包仍需保留（virsh 作为 fallback + 低频操作仍使用）
- go-libvirt 纯 Go 实现，无 CGO 依赖，跨平台编译不受影响

### 8. 统计文件不要迁移

- `host_stats_*.go` — Host 级别的资源统计涉及 `/proc`、`/sys`、`iostat`、`free` 等系统接口，非 libvirt API 范畴
- `host_zram.go` — 涉及 `modprobe`、`zramctl`、`mkswap` 等内核/系统管理命令
- `host_ksm.go` — 涉及 `/sys/kernel/mm/ksm/` sysfs 接口

---

## 性能预期（已部分验证）

以 50 台 VM、列表页加载为例:

| 指标 | 迁移前 (纯 virsh) | 迁移后 (go-libvirt) | 提升 |
|------|-----------|-----------|------|
| 进程 fork 次数 | ~200+ / 请求 | 0 (查询部分) | 100% |
| 列表页查询耗时 | ~3-8 秒 | ~0.3-1 秒 | ~5-10x |
| 资源采集 (10s 周期) | fork 50+ 进程 | RPC 零 fork | 100% |
| 错误解析方式 | 正则/awk 文本解析 | Go 类型安全 struct | — |

---

## 回退方案

如果 go-libvirt 出现兼容性或稳定性问题:

1. 设置配置项 `use_go_libvirt: false` 全局禁用
2. 所有函数自动回退到 virsh 路径（fallback 逻辑已内置于每个函数中）
3. go-libvirt 作为可选增强，不影响核心功能
4. libvirtd 重启导致 RPC 连接断开时，自动重连机制 + fallback 保证服务不中断

---

## 变更记录

| 日期 | 变更 |
|------|------|
| 2026-03 | 初版迁移方案（目标定义 + 架构设计） |
| 2026-04 | Phase 1-3 实施：基础设施 + 只读查询 + 资源采集 + 基础控制操作 |
| 2026-06 | 更新迁移状态：标注已完成/待迁移模块，细化剩余计划，补充 API 可用性更新，新增永远不迁移操作清单 |
