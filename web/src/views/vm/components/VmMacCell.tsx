/**
 * MAC 地址单元格（列表随 SSE 直接下发，无需额外请求）
 */
import type { VmListItem } from '@/api/vm'

interface VmMacCellProps {
  vm: VmListItem
}

export default function VmMacCell({ vm }: VmMacCellProps) {
  if (vm.mac_address) {
    return <span className="qvm-mac-addr">{vm.mac_address}</span>
  }
  return <span className="qvm-mac-addr na">未分配</span>
}
