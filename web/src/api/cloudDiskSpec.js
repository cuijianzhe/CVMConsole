import request from '@/utils/request'

export function listCloudDiskSpecs(params) {
  return request({
    url: '/cloud-disk-specs',
    method: 'get',
    params
  })
}

export function createCloudDiskSpec(data) {
  return request({
    url: '/cloud-disk-specs',
    method: 'post',
    data
  })
}

export function updateCloudDiskSpec(id, data) {
  return request({
    url: `/cloud-disk-specs/${id}`,
    method: 'put',
    data
  })
}

export function deleteCloudDiskSpec(id) {
  return request({
    url: `/cloud-disk-specs/${id}`,
    method: 'delete'
  })
}

export function batchDeleteCloudDiskSpecs(ids) {
  return request({
    url: '/cloud-disk-specs/batch-delete',
    method: 'post',
    data: { ids }
  })
}
