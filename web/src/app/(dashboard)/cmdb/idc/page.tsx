import IdcWorkspace from '@/components/cmdb/idc/idc-workspace'

// 机房机柜：三栏工作台（机房列表 / 平面图·3D 机房 / 容量与告警统计），
// 点击机柜进入机柜 U 位视图（可绑定/解绑设备）。
export default function CMDBIDCPage() {
  return <IdcWorkspace />
}
