import { api } from './http'

// ─── Web Push + in-app notification feed ───────────────────────────────────

export type AppNotification = {
  id: number
  userId: number
  title: string
  body: string
  magnet?: string
  read: boolean
  createdAt: string
}

export type NotificationsResponse = {
  items: AppNotification[]
  unread: number
}

export const pushVapidKey = async (): Promise<string | null> => {
  const r = await api.get<{ key: string }>('/push/vapid', { validateStatus: () => true })
  return r.status === 200 ? r.data.key : null
}

export const pushSubscribe = async (sub: PushSubscriptionJSON): Promise<void> => {
  await api.post('/push/subscribe', sub)
}

export const pushUnsubscribe = async (endpoint: string): Promise<void> => {
  await api.post('/push/unsubscribe', { endpoint })
}

export const notificationsList = async (limit = 50): Promise<NotificationsResponse> => {
  const { data } = await api.get<NotificationsResponse>(`/notifications?limit=${limit}`)
  return { items: data.items || [], unread: data.unread || 0 }
}

export const notificationsMarkRead = async (): Promise<void> => {
  await api.post('/notifications/read')
}
