import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '../../test-setup'

const listDevices = vi.fn()
const revokeDevice = vi.fn()
const setDevicePin = vi.fn()
vi.mock('../../api/devices', () => ({
  listDevices: (...a: unknown[]) => listDevices(...a),
  revokeDevice: (...a: unknown[]) => revokeDevice(...a),
  setDevicePin: (...a: unknown[]) => setDevicePin(...a),
}))

import DevicesCard from './DevicesCard'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  listDevices.mockReset(); revokeDevice.mockReset(); setDevicePin.mockReset()
})

const tv = {
  id: 7, userId: 1, deviceId: 'tv-1', name: 'TV da sala', model: 'Shield', hasPin: true,
  createdAt: '2026-11-09T12:00:00Z', lastUsedAt: '2026-11-09T13:00:00Z', lastIp: '192.168.1.40',
}

describe('DevicesCard', () => {
  it('lists paired devices and shows the empty state', async () => {
    listDevices.mockResolvedValueOnce([tv])
    render(<DevicesCard />)
    expect(await screen.findByText('TV da sala')).toBeInTheDocument()
    expect(screen.getByText(/192\.168\.1\.40/)).toBeInTheDocument()
    cleanup()
    listDevices.mockResolvedValueOnce([])
    render(<DevicesCard />)
    expect(await screen.findByText(/nenhum|no connected/i)).toBeInTheDocument()
  })

  it('revokes after confirmation and reloads', async () => {
    listDevices.mockResolvedValueOnce([tv]).mockResolvedValueOnce([])
    revokeDevice.mockResolvedValue(undefined)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    render(<DevicesCard />)
    const btn = await screen.findByRole('button', { name: /remover dispositivo|remove device/i })
    await userEvent.click(btn)
    expect(revokeDevice).not.toHaveBeenCalled()
    await userEvent.click(btn)
    expect(revokeDevice).toHaveBeenCalledWith(7)
    await waitFor(() => expect(listDevices).toHaveBeenCalledTimes(2))
    expect(confirm).toHaveBeenCalledTimes(2)
  })

  it('sets and clears the profile PIN (digits only)', async () => {
    listDevices.mockResolvedValue([tv])
    setDevicePin.mockResolvedValue(undefined)
    render(<DevicesCard />)
    await userEvent.click(await screen.findByRole('button', { name: /pin do perfil|profile pin/i }))
    const input = screen.getByRole('textbox')
    await userEvent.type(input, '12a34')
    expect(input).toHaveValue('1234')
    await userEvent.click(screen.getByRole('button', { name: /salvar pin|save pin/i }))
    expect(setDevicePin).toHaveBeenCalledWith(7, '1234')

    await userEvent.click(await screen.findByRole('button', { name: /pin do perfil|profile pin/i }))
    await userEvent.click(screen.getByRole('button', { name: /remover pin|remove pin/i }))
    expect(setDevicePin).toHaveBeenLastCalledWith(7, '')
  })

  it('shows API errors', async () => {
    listDevices.mockRejectedValueOnce({ response: { data: { error: 'boom' } } })
    render(<DevicesCard />)
    expect(await screen.findByText('boom')).toBeInTheDocument()
    cleanup()
    listDevices.mockResolvedValue([tv])
    setDevicePin.mockRejectedValue({ response: { data: { error: 'pin must be 4 to 6 digits' } } })
    render(<DevicesCard />)
    await userEvent.click(await screen.findByRole('button', { name: /pin do perfil|profile pin/i }))
    await userEvent.type(screen.getByRole('textbox'), '1234')
    await userEvent.click(screen.getByRole('button', { name: /salvar pin|save pin/i }))
    expect(await screen.findByText('pin must be 4 to 6 digits')).toBeInTheDocument()
  })
})
