# vmlist视图自适应宽度

虚拟机列表新增 ​**CPU**​、**内存** 两列，且所有**内容型列**都按列中最长字段自适应宽度。

### 1. 新增 CPU、内存两列

在「标签」与「配置(资源使用)」之间插入（[VmTableView.tsx:136-149](/web/src/views/vm/components/VmTableView.tsx#L136-L149)）：

|列|显示|数据来源|
| ------| -----------------| ----------------------------------|
|CPU|2 核|vm.vcpu|
|内存|4.0 GB / 512 MB|vm.memory（复用 formatMemoryMB）|

与「配置(资源使用)」列不冲突——后者展示 4C/8G/100G 摘要 + CPU/MEM 使用率进度条，新增两列是独立的核数/容量展示，便于快速扫描。

### 2. 提取通用 autoColWidth 工具函数

在 [vm/utils.ts:56-63](/web/src/views/vm/utils.ts#L56-L63) 新增通用函数，按列内容最长字符 × 系数 + 内边距估算宽度，clamp 上下限：

```ts
export function autoColWidth(
  values: string[],
  opts: { charWidth?: number; padding?: number; min?: number; max?: number } = {},
): number
```

3. 所有内容型列自适应宽度

[VmTableView.tsx:71-82](/web/src/views/vm/components/VmTableView.tsx#L71-L82) 用一个 colWidths useMemo 统一计算，每列按 vms 中最长字段动态算宽：

|列|自适应范围|说明|
| ----------| -------------| --------------------------------------|
|名称|120\~240|charWidth 9 + 60(图标/锁定/分组余量)|
|模板|90\~200|charWidth 8|
|**CPU**|72\~120|charWidth 9|
|**内存**|80\~120|charWidth 8|
|IP|120\~160|charWidth 8|
|MAC|140\~180|charWidth 8|
|运行时长|90\~140|charWidth 8|

**组件列保持固定**（内容是编辑器/资源条/按钮，不适合按文本算）：

- 标签 130、配置(资源使用) 230、操作 120、状态 56

### 4. 样式

[vm.css:441-447](/web/src/views/vm/vm.css#L441-L447) 新增 .qvm-vm-spec（CPU/内存数字单元格：等宽数字字体、text-1、12px）。

### 最终列顺序

名称 → 状态 → 模板 → 标签 → **CPU** → **内存** → 配置(资源使用) → IP → MAC → 运行时长 → 操作

vms 数据变化时，各内容列宽度随最长字段重算（依赖数组已含 colWidths）。
