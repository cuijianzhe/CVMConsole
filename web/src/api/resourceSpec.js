import request from '@/utils/request'

export function listResourceSpecs(params) {
  return request({
    url: '/resource-specs',
    method: 'get',
    params
  })
}

export function createResourceSpec(data) {
  return request({
    url: '/resource-specs',
    method: 'post',
    data
  })
}

export function updateResourceSpec(id, data) {
  return request({
    url: `/resource-specs/${id}`,
    method: 'put',
    data
  })
}

export function deleteResourceSpec(id) {
  return request({
    url: `/resource-specs/${id}`,
    method: 'delete'
  })
}

export function batchDeleteResourceSpecs(ids) {
  return request({
    url: '/resource-specs/batch-delete',
    method: 'post',
    data: { ids }
  })
}
