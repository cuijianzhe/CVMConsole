/**
 * 云盘规格管理 API
 * 对应后端 /cloud-disk-specs 路由组（管理员可写，所有已认证用户可读列表）
 */
import service from './client'
import type { ApiResponse } from '@/types/api'

/** 磁盘类型：SYSTEM=系统盘 / DATA=数据盘 */
export type DiskType = 'SYSTEM' | 'DATA'

/** 磁盘格式 */
export type DiskFormat = 'QCOW2' | 'RAW'

/** IOPS 限制模式 */
export type IOPSMode = 'TOTAL' | 'READ_WRITE'

/** 云盘规格项（与后端 model.CloudDiskSpec 对齐） */
export interface CloudDiskSpecItem {
  id: number
  name: string
  /** 磁盘类型 */
  disk_type: DiskType
  /** 容量（GB） */
  capacity_gb: number
  /** 存储位置（可选） */
  storage_location: string
  /** 磁盘格式 */
  disk_format: DiskFormat
  /** IOPS 限制模式 */
  iops_mode: IOPSMode
  /** 总 IOPS 上限（iops_mode=TOTAL 时生效） */
  total_iops: number
  /** 读 IOPS 上限（iops_mode=READ_WRITE 时生效） */
  read_iops: number
  /** 写 IOPS 上限（iops_mode=READ_WRITE 时生效） */
  write_iops: number
  /** 描述 */
  description: string
  created_at: string
  updated_at: string
}

/** 列表响应（与后端 CloudDiskSpecListResponse 对齐） */
export interface CloudDiskSpecListResult {
  list: CloudDiskSpecItem[]
  total: number
  page: number
  page_size: number
}

/** 创建/更新请求体 */
export interface CloudDiskSpecPayload {
  name: string
  disk_type: DiskType
  capacity_gb: number
  storage_location?: string
  disk_format?: DiskFormat
  iops_mode?: IOPSMode
  total_iops?: number
  read_iops?: number
  write_iops?: number
  description?: string
}

/** 获取云盘规格列表（支持分页与关键字搜索） */
export function listCloudDiskSpecs(params?: {
  page?: number
  page_size?: number
  keyword?: string
}) {
  return service.get<unknown, ApiResponse<CloudDiskSpecListResult>>('/cloud-disk-specs', {
    params,
    silent: true,
  })
}

/** 创建云盘规格 */
export function createCloudDiskSpec(data: CloudDiskSpecPayload) {
  return service.post<unknown, ApiResponse<CloudDiskSpecItem>>('/cloud-disk-specs', data)
}

/** 更新云盘规格 */
export function updateCloudDiskSpec(id: number, data: CloudDiskSpecPayload) {
  return service.put<unknown, ApiResponse<CloudDiskSpecItem>>(`/cloud-disk-specs/${id}`, data)
}

/** 删除云盘规格 */
export function deleteCloudDiskSpec(id: number) {
  return service.delete<unknown, ApiResponse>(`/cloud-disk-specs/${id}`)
}

/** 批量删除云盘规格 */
export function batchDeleteCloudDiskSpecs(ids: number[]) {
  return service.post<unknown, ApiResponse>('/cloud-disk-specs/batch-delete', { ids })
}
