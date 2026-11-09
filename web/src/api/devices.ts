import { api } from './http'

// ── Device pairing (TV/Android, #201) ────────────────────────────────────────
// The web side only APPROVES pairings and manages already-paired devices; the
// TV-side endpoints (start/token/session/profiles) are called by the device.

export type PendingDevice = {
  userCode: string
  name: string
  model: string
  ip: string
  createdAt: string
  expiresAt: string
}

export type PairedDevice = {
  id: number
  userId: number
  username?: string
  deviceId: string
  name: string
  model: string
  hasPin: boolean
  createdAt: string
  lastUsedAt?: string
  lastIp: string
}

export const getPendingDevice = async (code: string): Promise<PendingDevice> => {
  const { data } = await api.get<PendingDevice>('/auth/device/pending', { params: { code } })
  return data
}

export const decideDevice = async (userCode: string, approve: boolean): Promise<void> => {
  await api.post('/auth/device/approve', { userCode, approve })
}

export const listDevices = async (): Promise<PairedDevice[]> => {
  const { data } = await api.get<{ devices: PairedDevice[] }>('/auth/devices')
  return data.devices || []
}

export const revokeDevice = async (id: number): Promise<void> => {
  await api.delete(`/auth/devices/${id}`)
}

// pin "" clears the profile PIN.
export const setDevicePin = async (id: number, pin: string): Promise<void> => {
  await api.post(`/auth/devices/${id}/pin`, { pin })
}
