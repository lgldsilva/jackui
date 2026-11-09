import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound, Loader2, Trash2, Tv } from 'lucide-react'
import { listDevices, revokeDevice, setDevicePin, type PairedDevice } from '../../api/devices'
import { errMessage } from '../../lib/errMessage'

// DevicesCard — Settings → Conta → "Dispositivos": TVs/devices paired via the
// QR flow (/link). Each row is one (device, user) key: revoke it, or set/clear
// the profile PIN the TV asks before opening this profile.

function PinEditor({ device, onDone }: { readonly device: PairedDevice; readonly onDone: () => void }) {
  const { t } = useTranslation()
  const [pin, setPin] = useState('')
  const [error, setError] = useState('')
  const save = async (value: string) => {
    setError('')
    try { await setDevicePin(device.id, value); onDone() } catch (err) { setError(errMessage(err)) }
  }
  return (
    <form className="flex flex-wrap items-center gap-2 mt-2" onSubmit={e => { e.preventDefault(); void save(pin) }}>
      <input
        className="input-field w-28 font-mono" inputMode="numeric" pattern="[0-9]{4,6}" maxLength={6}
        placeholder={t('devices.pin_placeholder')} aria-label={t('devices.pin_placeholder')}
        value={pin} onChange={e => setPin(e.target.value.replace(/\D/g, ''))}
      />
      <button type="submit" className="btn-primary text-xs" disabled={pin.length < 4}>{t('devices.pin_save')}</button>
      {device.hasPin && <button type="button" className="btn-secondary text-xs" onClick={() => void save('')}>{t('devices.pin_clear')}</button>}
      {error && <span className="text-xs text-red-400 w-full">{error}</span>}
    </form>
  )
}

function DeviceRow({ device, onChanged }: { readonly device: PairedDevice; readonly onChanged: () => void }) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState(false)
  const [error, setError] = useState('')
  const remove = async () => {
    if (!window.confirm(t('devices.revoke_confirm', { name: device.name }))) return
    setError('')
    try { await revokeDevice(device.id); onChanged() } catch (err) { setError(errMessage(err)) }
  }
  return (
    <li className="py-2 border-b border-default last:border-0">
      <div className="flex items-center gap-2">
        <Tv className="w-4 h-4 text-text-muted flex-shrink-0" />
        <div className="flex-1 min-w-0 text-sm">
          <p className="text-text-primary truncate">{device.name}{device.model && <span className="text-text-muted"> · {device.model}</span>}</p>
          <p className="text-xs text-text-muted">
            {device.lastUsedAt ? t('devices.last_used', { when: new Date(device.lastUsedAt).toLocaleString() }) : t('devices.never_used')}
            {device.lastIp && ` · ${device.lastIp}`}
            {device.hasPin && ` · ${t('devices.has_pin')}`}
          </p>
        </div>
        <button className="text-text-muted hover:text-text-primary p-1" title={t('devices.pin_edit')} aria-label={t('devices.pin_edit')} onClick={() => setEditing(v => !v)}>
          <KeyRound className="w-4 h-4" />
        </button>
        <button className="text-text-muted hover:text-red-400 p-1" title={t('devices.revoke')} aria-label={t('devices.revoke')} onClick={() => void remove()}>
          <Trash2 className="w-4 h-4" />
        </button>
      </div>
      {error && <p className="text-xs text-red-400">{error}</p>}
      {editing && <PinEditor device={device} onDone={() => { setEditing(false); onChanged() }} />}
    </li>
  )
}

export default function DevicesCard() {
  const { t } = useTranslation()
  const [devices, setDevices] = useState<PairedDevice[] | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    try { setDevices(await listDevices()); setError('') } catch (err) { setError(errMessage(err)); setDevices([]) }
  }, [])
  useEffect(() => { void load() }, [load])

  return (
    <section className="card">
      <h2 className="text-sm font-semibold text-text-primary flex items-center gap-2 mb-1"><Tv className="w-4 h-4" />{t('devices.title')}</h2>
      <p className="text-xs text-text-muted mb-2">{t('devices.hint')}</p>
      {devices === null && <Loader2 className="w-4 h-4 animate-spin" aria-label={t('devices.loading')} />}
      {error && <p className="text-xs text-red-400">{error}</p>}
      {devices?.length === 0 && !error && <p className="text-sm text-text-muted">{t('devices.empty')}</p>}
      {devices && devices.length > 0 && (
        <ul>{devices.map(d => <DeviceRow key={d.id} device={d} onChanged={() => void load()} />)}</ul>
      )}
    </section>
  )
}
