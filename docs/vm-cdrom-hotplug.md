# 虚拟机运行中热添加设备 PCIe 插槽兼容修复

## 问题背景

部分使用 `q35` 机型的虚拟机在运行中热添加磁盘或光驱时，libvirt 可能报错：

- **光驱**: `Operation not supported: disk bus 'sata' cannot be hotplugged`
- **磁盘**: `No more available PCI slots`

原因分别是：
- 光驱原先固定走 `sata` 总线，而 `sata` 光驱不支持在运行中直接热添加
- 磁盘热添加时未指定 PCI 地址，q35/PCIe 机型下 libvirt 需要将设备挂载到空闲的 `pcie-root-port` 下游总线，若无空闲则报插槽不足

**插槽占满的典型原因**：q35 虚拟机启动时按设备数量自动生成 `pcie-root-port`，每个 virtio 设备（磁盘、网卡、气球、串口等）占用一个。当所有 port 被占满后无法再添加 virtio 总线的新设备。

## 现在的处理方式

### 光驱热添加

后端已调整为按虚拟机状态选择新增光驱的总线：

- 运行中新增光驱：自动切换为 `scsi` 总线
- 关机状态新增光驱：优先沿用现有光驱总线；如果没有历史光驱，则保持原来的 `sata`
- 已存在光驱的"插入 / 更换 ISO"：仍然优先复用原设备，不改变已有光驱总线

当运行中的虚拟机还没有 SCSI 控制器时，后端会先补齐控制器，再执行光驱热添加。

### 磁盘热添加（两级策略）

#### 第一级：virtio + PCIe 地址（默认）

- `q35` / `PCIe` 机型：通过 `virsh qemu-monitor-command --hmp info pci` 查找空闲的 `pcie-root-port` 下游总线，并为磁盘 XML 补全 PCI 地址
- `i440fx` 等非 PCIe 机型：不做额外处理，由 libvirt 自动分配

#### 第二级：scsi 总线降级（PCIe 插槽不足时自动触发）

当 virtio 路径因 `pcie-root-port` 插槽全部占满而失败时，后端自动尝试降级：

1. 检查虚拟机是否已有 `virtio-scsi` 控制器
2. 如果有：将磁盘总线从 `virtio` 切换为 `scsi`，通过已有控制器挂载，**不消耗新 PCIe 插槽**
3. 如果没有：尝试热添加 `virtio-scsi` 控制器（也需要 PCIe 插槽，通常也会失败）
4. 全部失败则返回明确的中文错误提示，建议关机后再操作

#### 预防：自定义 pcie-root-port 预留数量

新建 q35 虚拟机时可在"高级设置"中自定义预留的 `pcie-root-port` 数量（新建默认 4，编辑模式仅关机时可修改）。足够的插槽可避免后续热添加磁盘时提示"无可用 PCI 插槽"。

- **新建虚拟机**：在"高级选项"步骤中设置"PCIe 热插槽"数值
- **编辑虚拟机**：在"高级设置"标签页中修改（需先关机）
- 设为 0 表示使用默认值（新建 4 个）
- 最大值 32，最小值 0

影响接口：
- `POST /api/vm/:name/disk`（新增磁盘）
- `POST /api/vm/:name/disk/attach`（挂载已有磁盘）
- `POST /api/vm/create`（新建虚拟机时预留 port）

## 使用说明

在编辑虚拟机的"磁盘与光驱"页：

1. 如果已经有空光驱，优先使用"插入"
2. 如果需要额外新增一个光驱，可直接点击"添加光驱"
3. 添加磁盘时系统会自动选择最佳总线类型（virtio 优先，插槽不足时降级为 scsi）
4. 若提示无法添加，建议先关机后再操作

## 影响范围

- 后端：`POST /api/vm/:name/cdrom`、`POST /api/vm/:name/disk`、`POST /api/vm/:name/disk/attach`、`POST /api/vm/create`、`PUT /api/vm/:name`（pcie_root_ports 字段）
- 前端：虚拟机编辑弹窗的"磁盘与光驱"页和"高级设置"标签页

## 兼容性说明

- 本次修改不影响创建虚拟机时一次挂载多个磁盘/ISO 的逻辑
- 不改动已有设备的总线类型，避免对已上线虚拟机产生额外兼容风险
- scsi 降级只在 virtio 路径失败时自动触发，不影响正常 virtio 热添加流程
- 新建虚拟机预留的额外 pcie-root-port 是空端口，不影响任何已有功能
