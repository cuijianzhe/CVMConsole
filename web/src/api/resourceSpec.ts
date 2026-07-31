/**
 * 资源规格管理 API
 * 对应后端 /resource-specs 路由组（管理员可写，所有已认证用户可读列表）
 */
import service from './client'
import type { ApiResponse } from '@/types/api'

/** 资源规格项（与后端 model.ResourceSpec 对齐） */
export interface ResourceSpecItem {
  id: number
  name: string
  /** CPU 核心数 */
  cpu_cores: number
  /** 内存大小（GB） */
  memory_gb: number
  created_at: string
  updated_at: string
}

/** 列表响应（与后端 ResourceSpecListResponse 对齐） */
export interface ResourceSpecListResult {
  list: ResourceSpecItem[]
  total: number
  page: number
  page_size: number
}

/** 创建/更新请求体 */
export interface ResourceSpecPayload {
  name: string
  cpu_cores: number
  memory_gb: number
}

/** 获取资源规格列表（支持分页与关键字搜索） */
export function listResourceSpecs(params?: { page?: number; page_size?: number; keyword?: string }) {
  return service.get<unknown, ApiResponse<ResourceSpecListResult>>('/resource-specs', {
    params,
    silent: true,
  })
}

/** 创建资源规格 */
export function createResourceSpec(data: ResourceSpecPayload) {
  return service.post<unknown, ApiResponse<ResourceSpecItem>>('/resource-specs', data)
}

/** 更新资源规格 */
export function updateResourceSpec(id: number, data: ResourceSpecPayload) {
  return service.put<unknown, ApiResponse<ResourceSpecItem>>(`/resource-specs/${id}`, data)
}

/** 删除资源规格 */
export function deleteResourceSpec(id: number) {
  return service.delete<unknown, ApiResponse>(`/resource-specs/${id}`)
}

/** 批量删除资源规格 */
export function batchDeleteResourceSpecs(ids: number[]) {
  return service.post<unknown, ApiResponse>('/resource-specs/batch-delete', { ids })
}
