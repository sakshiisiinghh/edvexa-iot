import type { Alert, Device } from './types'

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path)
  if (!res.ok) {
    throw new Error(`${path}: ${res.status}`)
  }
  return res.json() as Promise<T>
}

async function post<T>(path: string): Promise<T> {
  const res = await fetch(path, { method: 'POST' })
  if (!res.ok) {
    throw new Error(`${path}: ${res.status}`)
  }
  return res.json() as Promise<T>
}

export function listDevices(): Promise<{ devices: Device[] }> {
  return get('/v1/devices')
}

export function listAlerts(): Promise<{ alerts: Alert[] }> {
  return get('/v1/alerts')
}

export function acknowledgeAlert(id: number): Promise<Alert> {
  return post(`/v1/alerts/${id}/acknowledge`)
}

export function resolveAlert(id: number): Promise<Alert> {
  return post(`/v1/alerts/${id}/resolve`)
}
