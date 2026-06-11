import { pushVapidKey, pushSubscribe, pushUnsubscribe } from '../api/client'

// urlBase64ToUint8Array converts the VAPID public key (base64url) into the
// Uint8Array PushManager.subscribe expects. Pure — covered by vitest.
export function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const normalized = (base64 + padding).replaceAll('-', '+').replaceAll('_', '/')
  const raw = atob(normalized)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.codePointAt(i)!
  return out
}

export function isPushSupported(): boolean {
  return 'serviceWorker' in navigator && 'PushManager' in globalThis && 'Notification' in globalThis
}

// registerServiceWorker is idempotent — called once at boot (main.tsx).
export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null
  try {
    return await navigator.serviceWorker.register('/sw.js')
  } catch {
    return null
  }
}

// enablePush walks the whole flow: permission → SW → PushManager.subscribe →
// register the subscription with the backend. Returns false when any step is
// denied/unavailable.
export async function enablePush(): Promise<boolean> {
  if (!isPushSupported()) return false
  const permission = await Notification.requestPermission()
  if (permission !== 'granted') return false
  const reg = (await navigator.serviceWorker.getRegistration()) ?? (await registerServiceWorker())
  if (!reg) return false
  const key = await pushVapidKey()
  if (!key) return false
  try {
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(key).buffer as ArrayBuffer,
    })
    await pushSubscribe(sub.toJSON())
    return true
  } catch {
    return false
  }
}

// disablePush undoes enablePush for this browser.
export async function disablePush(): Promise<void> {
  const reg = await navigator.serviceWorker?.getRegistration()
  const sub = await reg?.pushManager.getSubscription()
  if (!sub) return
  await pushUnsubscribe(sub.endpoint)
  await sub.unsubscribe()
}

// isPushEnabled reports whether this browser currently has a live subscription.
export async function isPushEnabled(): Promise<boolean> {
  if (!isPushSupported() || Notification.permission !== 'granted') return false
  const reg = await navigator.serviceWorker.getRegistration()
  return Boolean(await reg?.pushManager.getSubscription())
}
