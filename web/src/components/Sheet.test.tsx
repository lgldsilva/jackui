import { afterEach, describe, it, expect, vi } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Sheet } from './Sheet'

// Nota sobre i18n no ambiente de teste:
// jsdom usa navigator.language="en-US" por padrão, então
// t('misc.close') → "Close" (não "Fechar").
// Usamos toHaveAccessibleName() e nomes em inglês para acertar.

afterEach(cleanup)

function renderSheet(overrides: Record<string, unknown> = {}) {
  return render(
    <Sheet open onClose={vi.fn()} title="Modal de teste" {...overrides}>
      <p>Conteudo do modal</p>
    </Sheet>,
  )
}

describe('Sheet — acessibilidade', () => {
  it('renderiza com role="dialog" e aria-modal="true"', () => {
    renderSheet()
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
  })

  it('aria-labelledby aponta para o id do título', () => {
    renderSheet()
    const dialog = screen.getByRole('dialog')
    const labelledby = dialog.getAttribute('aria-labelledby')
    expect(labelledby).toBeTruthy()
    const titleEl = document.getElementById(labelledby!)
    expect(titleEl).toBeInTheDocument()
    expect(titleEl).toHaveTextContent('Modal de teste')
  })

  it('botão de fechar tem aria-label traduzido', () => {
    renderSheet()
    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toBeInTheDocument()
    expect(closeBtn).toHaveAccessibleName('Close')
  })

  it('foca o primeiro elemento focável ao abrir', () => {
    renderSheet()
    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toHaveFocus()
  })

  it('restaura o foco ao elemento anterior quando fecha', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    const { rerender } = render(
      <>
        <button data-testid="trigger">Abrir</button>
        <Sheet open={false} onClose={onClose} title="Modal">
          <p>conteudo</p>
        </Sheet>
      </>,
    )

    const trigger = screen.getByTestId('trigger')
    await user.click(trigger)
    expect(trigger).toHaveFocus()

    // Abre o modal
    rerender(
      <>
        <button data-testid="trigger">Abrir</button>
        <Sheet open onClose={onClose} title="Modal">
          <p>conteudo</p>
        </Sheet>
      </>,
    )

    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toHaveFocus()

    // Fecha o modal
    rerender(
      <>
        <button data-testid="trigger">Abrir</button>
        <Sheet open={false} onClose={onClose} title="Modal">
          <p>conteudo</p>
        </Sheet>
      </>,
    )

    // Foco restaurou para o trigger
    expect(trigger).toHaveFocus()
  })

  it('cicla foco com Tab dentro do modal (focus trap)', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    render(
      <Sheet open onClose={onClose} title="Modal foco">
        <button data-testid="btn-1">Botão 1</button>
        <button data-testid="btn-2">Botão 2</button>
        <a data-testid="link-1" href="#">Link</a>
      </Sheet>,
    )

    const dialog = screen.getByRole('dialog')
    const closeBtn = within(dialog).getByRole('button', { name: 'Close' })

    // Foco começa no close button (primeiro focável)
    expect(closeBtn).toHaveFocus()

    // Tab → Botão 1
    await user.tab()
    expect(within(dialog).getByTestId('btn-1')).toHaveFocus()

    // Tab → Botão 2
    await user.tab()
    expect(within(dialog).getByTestId('btn-2')).toHaveFocus()

    // Tab → Link
    await user.tab()
    expect(within(dialog).getByTestId('link-1')).toHaveFocus()

    // Tab → volta ao close button (ciclo)
    await user.tab()
    expect(closeBtn).toHaveFocus()
  })

  it('Shift+Tab cicla foco reverso', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    render(
      <Sheet open onClose={onClose} title="Modal shift tab">
        <button data-testid="btn-1">Botão 1</button>
        <button data-testid="btn-2">Botão 2</button>
      </Sheet>,
    )

    const dialog = screen.getByRole('dialog')
    const closeBtn = within(dialog).getByRole('button', { name: 'Close' })

    // Shift+Tab no primeiro focável → vai pro último (btn-2)
    await user.tab({ shift: true })
    expect(within(dialog).getByTestId('btn-2')).toHaveFocus()

    // Shift+Tab → Botão 1
    await user.tab({ shift: true })
    expect(within(dialog).getByTestId('btn-1')).toHaveFocus()

    // Shift+Tab → close button
    await user.tab({ shift: true })
    expect(closeBtn).toHaveFocus()
  })

  it('não renderiza nada quando open=false', () => {
    const { container } = render(
      <Sheet open={false} onClose={vi.fn()} title="Invisivel">
        <p>nao deve aparecer</p>
      </Sheet>,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('não tem aria-labelledby quando hideHeader=true', () => {
    render(
      <Sheet open onClose={vi.fn()} title="Sem header" hideHeader>
        <p>conteudo</p>
      </Sheet>,
    )
    const dialog = screen.getByRole('dialog')
    expect(dialog).not.toHaveAttribute('aria-labelledby')
  })
})
