import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import '../test-setup'

const getPendingDevice = vi.fn()
const decideDevice = vi.fn()
vi.mock('../api/devices', () => ({
  getPendingDevice: (...a: unknown[]) => getPendingDevice(...a),
  decideDevice: (...a: unknown[]) => decideDevice(...a),
}))

import LinkPage from './LinkPage'

afterEach(() => {
  cleanup()
  getPendingDevice.mockReset()
  decideDevice.mockReset()
})

const pending = {
  userCode: 'ABCD-EFGH', name: 'TV da sala', model: 'Shield', ip: '192.168.1.40',
  createdAt: '2026-11-09T12:00:00Z', expiresAt: '2026-11-09T12:10:00Z',
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/link" element={<LinkPage />} />
        <Route path="/" element={<div>home-page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('LinkPage', () => {
  it('shows who is asking and approves only on explicit confirm', async () => {
    getPendingDevice.mockResolvedValue(pending)
    decideDevice.mockResolvedValue(undefined)
    renderAt('/link?code=abcd-efgh')
    expect(await screen.findByText(/TV da sala/)).toBeInTheDocument()
    expect(screen.getByText(/192\.168\.1\.40/)).toBeInTheDocument()
    expect(getPendingDevice).toHaveBeenCalledWith('ABCD-EFGH')
    expect(decideDevice).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: /autorizar|authorize/i }))
    expect(decideDevice).toHaveBeenCalledWith('ABCD-EFGH', true)
    expect(await screen.findByText(/conectado|connected/i)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /início|home/i }))
    expect(await screen.findByText('home-page')).toBeInTheDocument()
  })

  it('denies', async () => {
    getPendingDevice.mockResolvedValue(pending)
    decideDevice.mockResolvedValue(undefined)
    renderAt('/link?code=ABCD-EFGH')
    await userEvent.click(await screen.findByRole('button', { name: /recusar|deny/i }))
    expect(decideDevice).toHaveBeenCalledWith('ABCD-EFGH', false)
    expect(await screen.findByText(/recusado|denied/i)).toBeInTheDocument()
  })

  it('asks for the code when the link has none, and reports unknown codes', async () => {
    getPendingDevice.mockRejectedValueOnce({ response: { status: 404, data: { error: 'unknown or expired code' } } })
    renderAt('/link')
    const input = screen.getByRole('textbox')
    expect(screen.getByRole('button', { name: /continuar|continue/i })).toBeDisabled()
    await userEvent.type(input, 'zzzz-zzzz')
    expect(input).toHaveValue('ZZZZ-ZZZZ')
    await userEvent.click(screen.getByRole('button', { name: /continuar|continue/i }))
    expect(await screen.findByText(/inválido|invalid/i)).toBeInTheDocument()

    getPendingDevice.mockResolvedValueOnce(pending)
    await userEvent.click(screen.getByRole('button', { name: /continuar|continue/i }))
    expect(await screen.findByText(/TV da sala/)).toBeInTheDocument()
  })

  it('surfaces backend errors (e.g. lockout) on lookup and on decide', async () => {
    getPendingDevice.mockRejectedValueOnce({ response: { status: 429, data: { error: 'muitas tentativas' } } })
    renderAt('/link?code=ABCD-EFGH')
    expect(await screen.findByText('muitas tentativas')).toBeInTheDocument()

    cleanup()
    getPendingDevice.mockResolvedValue(pending)
    decideDevice.mockRejectedValue({ response: { data: { error: 'expirou' } } })
    renderAt('/link?code=ABCD-EFGH')
    await userEvent.click(await screen.findByRole('button', { name: /autorizar|authorize/i }))
    expect(await screen.findByText('expirou')).toBeInTheDocument()
  })
})
