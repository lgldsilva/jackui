import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AsyncState } from './AsyncState'
import { StatusBanner } from './StatusBanner'
import { RetryPanel } from './RetryPanel'
import '../test-setup'

afterEach(() => cleanup())

describe('AsyncState', () => {
  it('mostra loading quando loading=true', () => {
    render(
      <AsyncState loading loadingLabel="Carregando…">
        <p>conteúdo</p>
      </AsyncState>,
    )
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText('conteúdo')).toBeNull()
  })

  it('mostra erro com retry', async () => {
    const onRetry = vi.fn()
    render(
      <AsyncState error="falhou" onRetry={onRetry}>
        <p>conteúdo</p>
      </AsyncState>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('falhou')
    await userEvent.click(screen.getByRole('button', { name: /try again/i }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('renderiza children no sucesso', () => {
    render(<AsyncState><p>ok</p></AsyncState>)
    expect(screen.getByText('ok')).toBeTruthy()
  })
})

describe('StatusBanner', () => {
  it('usa role=alert para variant error', () => {
    render(<StatusBanner variant="error" title="Erro">detalhe</StatusBanner>)
    const el = screen.getByRole('alert')
    expect(el).toHaveTextContent('Erro')
    expect(el).toHaveTextContent('detalhe')
  })
})

describe('RetryPanel', () => {
  it('dispara onRetry', async () => {
    const fn = vi.fn()
    render(<RetryPanel onRetry={fn} label="Tentar de novo" />)
    await userEvent.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(fn).toHaveBeenCalledOnce()
  })
})
