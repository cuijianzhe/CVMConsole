# 虚拟机磁盘扩容兼容修复

这是虚拟机 系统盘扩容 的逻辑（ disk.go ），分两个阶段：

## 整体流程

‍

|阶段|说明|
| -----------------| ----------------------------------------------------------------------------|
|host\_stage|宿主机上用 qemu-img resize 扩大磁盘镜像 → 本例已成功|
|guest\_stage|通过 QGA（QEMU Guest Agent）在**虚拟机内**执行 shell 脚本扩容分区+文件系统 → 本例失败|

## 失败原因

linuxGrowScript 脚本（ disk.go:334 ）调用了：

```ts
lsblk -bnro START "$part"
```

START 列在 util-linux ≥ 2.27 （约2015年）才引入。你这台虚拟机的 util-linux 版本较老，不认识该列，所以报 lsblk: 未知列: START 。

## 什么情况下可以扩容

必须 同时满足 ：

1. 虚拟机 运行中 （QGA 要在线）
2. QGA 启用了 guest-exec + guest-exec-status 能力
3. Linux 虚拟机（Windows 不支持自动系统分区扩容）
4. 根文件系统为 ext4 / xfs / btrfs 之一
5. Btrfs 若是多设备，不执行
6. 宿主机已安装 growpart （ cloud-guest-utils 包）

## 磁盘格式

- 系统盘需是 qcow2 或 raw （ qemu-img resize 支持的格式）
- 分区格式：MBR 或 GPT 均可，脚本通过 lsblk 识别分区号

## 修复方案

用 parted 替代 lsblk START ，兼容所有版本的虚拟机。修复 linuxGrowScript 中两处 lsblk START 调用：

## 修复说明

问题： linuxGrowScript 在虚拟机内通过 QGA 执行时，调用 lsblk -bnro START 获取分区起始扇区。该列在 util-linux < 2.27 （2015年之前）中不存在，老版本系统（如 CentOS 7、Debian 8 等）会报 lsblk: 未知列: START 。

修复方式： 在 disk.go:334 中加了兼容回退：

1. 先尝试 lsblk -bnro START （新版 util-linux 直接成功）
2. 若为空 → 回退 sfdisk -d 解析（ sfdisk -d 输出 start=<扇区> ，所有版本通用）
3. 分区阻挡检测 同样加了 sfdisk 回退：先检测 lsblk START 是否可用，不可用则用 sfdisk -d 的输出做比较  
   什么情况下可以扩容：

- 虚拟机运行中 + QGA 在线 + guest-exec 可用
- 仅 Linux （Windows 不支持自动系统分区扩容）
- 根文件系统为 ext4 / xfs / btrfs
- 磁盘格式 qcow2 或 raw（宿主机 qemu-img resize 支持即可）
- 宿主机已安装 growpart （ cloud-guest-utils 包）
- 修复后不再要求 虚拟机内 util-linux ≥ 2.27

‍

磁盘布局

```ts
vda (40G, 已扩容到40G)
├─vda1 (512M) → EFI
└─vda2 (19.5G) → LVM PV
  ├─vgubuntu-root (17.5G) → ext4, /     ← 这是根分区
  └─vgubuntu-swap_1 (2G) → swap
```

## 当前脚本对 LVM 扩容的支持情况

脚本 linuxGrowScript **其实已经完整支持 LVM 扩容**，关键逻辑在 [disk.go:350-358](/server/service/guest_automation/disk.go#L350-L358)：

```ts
# 检测到根分区所在设备是 LVM 类型
if [ "$(lsblk -dnro TYPE "$root_real")" = "lvm" ]; then
  pvresize "$pv"                    # 扩展物理卷
  lvextend -l +100%FREE "$root_src" # 扩展逻辑卷
fi
# 最后按文件系统类型扩容
resize2fs "$root_real"   # ext4
```

你手动执行的 4 条命令（ growpart → pvresize → lvextend → resize2fs ），脚本里 全都有 。

## 唯一的卡点就是 START 列

之前报错 lsblk: 未知列: START 只是卡在 前面的兼容性检查 （确认根分区后面没有其他分区阻挡），还没走到 LVM 扩容那一步就已经退出了。刚才的修复就是解决这个兼容性问题。

## 结论

LVM 扩容本身是支持的 ，之前失败纯粹是 lsblk START 列不兼容导致的前置检查就挂了。修复后重试 guest_grow 操作应该能走通。你说先不改代码，那当前修复已经到位，下次重试即可。
